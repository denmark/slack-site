package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/denmark/slack-site/db"
	"github.com/denmark/slack-site/internal/mirror"
	"github.com/denmark/slack-site/internal/urlpath"
	"github.com/denmark/slack-site/models"
	"github.com/spf13/cobra"
)

var (
	mirrorDataDir     string
	mirrorMirror      string
	mirrorConcurrency int
	mirrorInit        bool
	mirrorDryRun      bool
	mirrorSlackToken  string
	mirrorAWSProfile  string
)

func init() {
	mirrorCmd := &cobra.Command{
		Use:   "mirror-files",
		Short: "Mirror message files to a local directory or S3",
		Long:  "Reads message_files from the DB in --data, downloads each file from url_private, and writes to --mirror (file:// or s3://). Uses mirrored_files table for re-entrancy. Use --init to clear state and re-mirror all.",
		RunE:  runMirror,
	}
	mirrorCmd.Flags().StringVar(&mirrorDataDir, "data", "", "Path to directory containing "+db.DBFileName)
	mirrorCmd.Flags().StringVar(&mirrorMirror, "mirror", "", "Destination: file:///path or s3://bucket/prefix")
	mirrorCmd.Flags().IntVar(&mirrorConcurrency, "concurrency", 2, "Number of concurrent download/upload workers")
	mirrorCmd.Flags().BoolVar(&mirrorInit, "init", false, "Clear mirror state table before running (full re-mirror)")
	mirrorCmd.Flags().BoolVar(&mirrorDryRun, "dry-run", false, "Only log what would be done; no download, write, or DB updates")
	mirrorCmd.Flags().StringVar(&mirrorSlackToken, "slack-token", "", "Slack token for url_private requests (or set SLACK_TOKEN)")
	mirrorCmd.Flags().StringVar(&mirrorAWSProfile, "aws-profile", "", "AWS config profile to use for S3 (e.g. SSO profile name); uses default profile if not set")
	_ = mirrorCmd.MarkFlagRequired("data")
	_ = mirrorCmd.MarkFlagRequired("mirror")
	rootCmd.AddCommand(mirrorCmd)
}

// progressFieldWidth returns the number of decimal digits needed to display n (minimum 1).
func progressFieldWidth(n int) int {
	if n < 1 {
		return 1
	}
	w := 0
	for t := n; t > 0; t /= 10 {
		w++
	}
	return w
}

// formatProgressBracket returns "[00042] " with seq zero-padded to width digits.
func formatProgressBracket(seq int64, width int) string {
	s := strconv.FormatInt(seq, 10)
	if len(s) < width {
		s = strings.Repeat("0", width-len(s)) + s
	}
	return "[" + s + "] "
}

func runMirror(cmd *cobra.Command, args []string) error {
	if mirrorConcurrency < 1 {
		return fmt.Errorf("--concurrency must be at least 1")
	}

	dbPath := filepath.Join(mirrorDataDir, db.DBFileName)
	database, err := db.OpenReadWrite(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx := context.Background()
	// Prefer --aws-profile; fall back to AWS_PROFILE so SSO/profile credentials are used.
	awsProfile := mirrorAWSProfile
	if awsProfile == "" {
		awsProfile = os.Getenv("AWS_PROFILE")
	}
	// For S3, clear env static keys so the SDK uses shared config (profile/SSO) instead of invalid env creds.
	if strings.HasPrefix(mirrorMirror, "s3://") {
		mirror.UnsetEnvCredsForProfile()
	}
	writer, mirrorRoot, err := mirror.ParseMirrorURL(ctx, mirrorMirror, awsProfile)
	if err != nil {
		return fmt.Errorf("parse --mirror: %w", err)
	}
	if writer == nil {
		return fmt.Errorf("unsupported --mirror scheme (use file:// or s3://)")
	}

	if mirrorInit && !mirrorDryRun {
		_, err = database.NewDelete().Model((*models.MirroredFileRow)(nil)).Where("mirror_root = ?", mirrorRoot).Exec(ctx)
		if err != nil {
			return fmt.Errorf("clear mirrored_files: %w", err)
		}
		log.Printf("Cleared mirror state for %s", mirrorRoot)
	}

	slackToken := mirrorSlackToken
	if slackToken == "" {
		slackToken = os.Getenv("SLACK_TOKEN")
	}

	// Stream message_files and feed rows that are not already mirrored into the worker pool
	var skipped, mirrored int64
	var files []models.MessageFileRow
	err = database.NewSelect().Model(&files).Column("url_private", "name").Scan(ctx)
	if err != nil {
		return fmt.Errorf("select message_files: %w", err)
	}
	progressWidth := progressFieldWidth(len(files))
	var progressSeq int64

	type work struct {
		urlPrivate string
		name       string
	}
	workCh := make(chan work, mirrorConcurrency*2)
	var wg sync.WaitGroup

	// Workers
	for i := 0; i < mirrorConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range workCh {
				relPath, pathErr := urlpath.RelativePath(w.urlPrivate, w.name)
				if pathErr != nil {
					n := atomic.AddInt64(&progressSeq, 1)
					fmt.Printf("%sskip %s: %v\n", formatProgressBracket(n, progressWidth), w.urlPrivate, pathErr)
					atomic.AddInt64(&skipped, 1)
					continue
				}
				if mirrorDryRun {
					n := atomic.AddInt64(&progressSeq, 1)
					fmt.Printf("%swould mirror -> %s\n", formatProgressBracket(n, progressWidth), relPath)
					atomic.AddInt64(&mirrored, 1)
					continue
				}
				// Download
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.urlPrivate, nil)
				if err != nil {
					n := atomic.AddInt64(&progressSeq, 1)
					fmt.Printf("%sskip %s: %v\n", formatProgressBracket(n, progressWidth), relPath, err)
					atomic.AddInt64(&skipped, 1)
					continue
				}
				if slackToken != "" {
					req.Header.Set("Authorization", "Bearer "+slackToken)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					n := atomic.AddInt64(&progressSeq, 1)
					fmt.Printf("%sskip %s: %v\n", formatProgressBracket(n, progressWidth), relPath, err)
					atomic.AddInt64(&skipped, 1)
					continue
				}
				if resp.StatusCode != http.StatusOK {
					resp.Body.Close()
					n := atomic.AddInt64(&progressSeq, 1)
					fmt.Printf("%sskip %s: HTTP %d\n", formatProgressBracket(n, progressWidth), relPath, resp.StatusCode)
					atomic.AddInt64(&skipped, 1)
					continue
				}
				contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
				contentLength := resp.ContentLength
				var body io.Reader = resp.Body
				if contentLength < 0 {
					// Chunked or unknown length; buffer so S3 gets Content-Length.
					buf, readErr := io.ReadAll(resp.Body)
					resp.Body.Close()
					if readErr != nil {
						n := atomic.AddInt64(&progressSeq, 1)
						fmt.Printf("%sskip %s: read: %v\n", formatProgressBracket(n, progressWidth), relPath, readErr)
						atomic.AddInt64(&skipped, 1)
						continue
					}
					body = bytes.NewReader(buf)
					contentLength = int64(len(buf))
				}
				err = writer.Write(ctx, relPath, body, contentLength, contentType)
				if contentLength < 0 {
					// body was already closed above
				} else {
					resp.Body.Close()
				}
				if err != nil {
					n := atomic.AddInt64(&progressSeq, 1)
					fmt.Printf("%sskip %s: write: %v\n", formatProgressBracket(n, progressWidth), relPath, err)
					atomic.AddInt64(&skipped, 1)
					continue
				}
				_, err = database.NewInsert().Model(&models.MirroredFileRow{MirrorRoot: mirrorRoot, URLPrivate: w.urlPrivate, StoredPath: relPath}).Exec(ctx)
				if err != nil {
					log.Printf("warning: inserted file but DB record failed %q: %v", w.urlPrivate, err)
				}
				n := atomic.AddInt64(&progressSeq, 1)
				fmt.Printf("%smirrored -> %s\n", formatProgressBracket(n, progressWidth), relPath)
				atomic.AddInt64(&mirrored, 1)
			}
		}()
	}

	// Producer: check mirrored_files and send work
	for _, f := range files {
		if f.URLPrivate == "" {
			n := atomic.AddInt64(&progressSeq, 1)
			fmt.Printf("%sskip (empty url)\n", formatProgressBracket(n, progressWidth))
			atomic.AddInt64(&skipped, 1)
			continue
		}
		exists, err := database.NewSelect().Model((*models.MirroredFileRow)(nil)).Where("mirror_root = ? AND url_private = ?", mirrorRoot, f.URLPrivate).Exists(ctx)
		if err != nil {
			return fmt.Errorf("check mirrored_files: %w", err)
		}
		if exists {
			n := atomic.AddInt64(&progressSeq, 1)
			relPath, _ := urlpath.RelativePath(f.URLPrivate, f.Name)
			if relPath != "" {
				fmt.Printf("%sskip (already mirrored) %s\n", formatProgressBracket(n, progressWidth), relPath)
			} else {
				fmt.Printf("%sskip (already mirrored) %s\n", formatProgressBracket(n, progressWidth), f.URLPrivate)
			}
			atomic.AddInt64(&skipped, 1)
			continue
		}
		workCh <- work{urlPrivate: f.URLPrivate, name: f.Name}
	}
	close(workCh)
	wg.Wait()

	if mirrorDryRun {
		fmt.Printf("Dry-run: would mirror %d, skip %d\n", mirrored, skipped)
	} else {
		fmt.Printf("Mirrored %d, skipped %d\n", mirrored, skipped)
	}
	return nil
}
