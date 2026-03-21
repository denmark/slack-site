package cmd

import (
	"bytes"
	"context"
	"errors"
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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

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
	mirrorSyncCT      bool
)

func init() {
	mirrorCmd := &cobra.Command{
		Use:   "mirror-files",
		Short: "Mirror message files to a local directory or S3",
		Long:  "Reads message_files from the DB in --data, downloads each file from url_private, and writes to --mirror (file:// or s3://). Uses mirrored_files table for re-entrancy. Use --init to clear state and re-mirror all. Use --sync-ct with s3:// to HEAD each url_private and set S3 Content-Type on existing objects (does not use mirrored_files; cannot be combined with --init).",
		RunE:  runMirror,
	}
	mirrorCmd.Flags().StringVar(&mirrorDataDir, "data", "", "Path to directory containing "+db.DBFileName)
	mirrorCmd.Flags().StringVar(&mirrorMirror, "mirror", "", "Destination: file:///path or s3://bucket/prefix")
	mirrorCmd.Flags().IntVar(&mirrorConcurrency, "concurrency", 2, "Number of concurrent download/upload workers")
	mirrorCmd.Flags().BoolVar(&mirrorInit, "init", false, "Clear mirror state table before running (full re-mirror)")
	mirrorCmd.Flags().BoolVar(&mirrorDryRun, "dry-run", false, "Only log what would be done; no download, write, or DB updates")
	mirrorCmd.Flags().StringVar(&mirrorSlackToken, "slack-token", "", "Slack token for url_private requests (or set SLACK_TOKEN)")
	mirrorCmd.Flags().StringVar(&mirrorAWSProfile, "aws-profile", "", "AWS config profile to use for S3 (e.g. SSO profile name); uses default profile if not set")
	mirrorCmd.Flags().BoolVar(&mirrorSyncCT, "sync-ct", false, "HEAD each url_private and update Content-Type on matching S3 objects (requires s3:// --mirror)")
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
	if mirrorSyncCT && mirrorInit {
		return fmt.Errorf("--init cannot be used with --sync-ct")
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
	s3Writer, mirrorIsS3 := writer.(*mirror.S3Writer)

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

	var files []models.MessageFileRow
	err = database.NewSelect().Model(&files).Column("url_private", "name").Scan(ctx)
	if err != nil {
		return fmt.Errorf("select message_files: %w", err)
	}
	progressWidth := progressFieldWidth(len(files))
	var progressSeq int64

	if mirrorSyncCT {
		if !mirrorIsS3 || s3Writer == nil {
			return fmt.Errorf("--sync-ct requires an s3:// --mirror destination")
		}
		return runMirrorSyncContentTypes(ctx, s3Writer, slackToken, files, progressWidth, &progressSeq)
	}

	// Stream message_files and feed rows that are not already mirrored into the worker pool
	var skipped, mirrored int64

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

func isS3ObjectMissing(err error) bool {
	var nk *types.NoSuchKey
	if errors.As(err, &nk) {
		return true
	}
	var nf *types.NotFound
	return errors.As(err, &nf)
}

func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// runMirrorSyncContentTypes HEADs each Slack url_private and updates the corresponding S3 object's Content-Type.
func runMirrorSyncContentTypes(ctx context.Context, s3w *mirror.S3Writer, slackToken string, files []models.MessageFileRow, progressWidth int, progressSeq *int64) error {
	var skipped, updated, unchanged int64

	type work struct {
		urlPrivate string
		name       string
	}
	workCh := make(chan work, mirrorConcurrency*2)
	var wg sync.WaitGroup

	for i := 0; i < mirrorConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range workCh {
				relPath, pathErr := urlpath.RelativePath(w.urlPrivate, w.name)
				if pathErr != nil {
					n := atomic.AddInt64(progressSeq, 1)
					fmt.Printf("%sskip %s: %v\n", formatProgressBracket(n, progressWidth), w.urlPrivate, pathErr)
					atomic.AddInt64(&skipped, 1)
					continue
				}

				req, err := http.NewRequestWithContext(ctx, http.MethodHead, w.urlPrivate, nil)
				if err != nil {
					n := atomic.AddInt64(progressSeq, 1)
					fmt.Printf("%sskip %s: %v\n", formatProgressBracket(n, progressWidth), relPath, err)
					atomic.AddInt64(&skipped, 1)
					continue
				}
				if slackToken != "" {
					req.Header.Set("Authorization", "Bearer "+slackToken)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					n := atomic.AddInt64(progressSeq, 1)
					fmt.Printf("%sskip %s: HEAD: %v\n", formatProgressBracket(n, progressWidth), relPath, err)
					atomic.AddInt64(&skipped, 1)
					continue
				}
				contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
				drainAndClose(resp)

				if resp.StatusCode != http.StatusOK {
					n := atomic.AddInt64(progressSeq, 1)
					fmt.Printf("%sskip %s: HEAD HTTP %d\n", formatProgressBracket(n, progressWidth), relPath, resp.StatusCode)
					atomic.AddInt64(&skipped, 1)
					continue
				}
				if contentType == "" {
					n := atomic.AddInt64(progressSeq, 1)
					fmt.Printf("%sskip %s: empty Content-Type from HEAD\n", formatProgressBracket(n, progressWidth), relPath)
					atomic.AddInt64(&skipped, 1)
					continue
				}

				s3Key := s3w.ObjectKey(relPath)
				if mirrorDryRun {
					headS3, err := s3w.Client.HeadObject(ctx, &s3.HeadObjectInput{
						Bucket: aws.String(s3w.Bucket),
						Key:    aws.String(s3Key),
					})
					if err != nil {
						n := atomic.AddInt64(progressSeq, 1)
						if isS3ObjectMissing(err) {
							fmt.Printf("%sskip %s: S3 object missing\n", formatProgressBracket(n, progressWidth), relPath)
						} else {
							fmt.Printf("%sskip %s: S3 HeadObject: %v\n", formatProgressBracket(n, progressWidth), relPath, err)
						}
						atomic.AddInt64(&skipped, 1)
						continue
					}
					if strings.TrimSpace(aws.ToString(headS3.ContentType)) == contentType {
						n := atomic.AddInt64(progressSeq, 1)
						fmt.Printf("%sskip (content-type already %q) %s\n", formatProgressBracket(n, progressWidth), contentType, relPath)
						atomic.AddInt64(&unchanged, 1)
						continue
					}
					n := atomic.AddInt64(progressSeq, 1)
					fmt.Printf("%swould set content-type %q on %s\n", formatProgressBracket(n, progressWidth), contentType, relPath)
					atomic.AddInt64(&updated, 1)
					continue
				}

				didUpdate, err := s3w.SyncContentType(ctx, relPath, contentType)
				if err != nil {
					n := atomic.AddInt64(progressSeq, 1)
					if isS3ObjectMissing(err) {
						fmt.Printf("%sskip %s: S3 object missing\n", formatProgressBracket(n, progressWidth), relPath)
					} else {
						fmt.Printf("%sskip %s: %v\n", formatProgressBracket(n, progressWidth), relPath, err)
					}
					atomic.AddInt64(&skipped, 1)
					continue
				}
				if didUpdate {
					n := atomic.AddInt64(progressSeq, 1)
					fmt.Printf("%supdated content-type %q %s\n", formatProgressBracket(n, progressWidth), contentType, relPath)
					atomic.AddInt64(&updated, 1)
				} else {
					n := atomic.AddInt64(progressSeq, 1)
					fmt.Printf("%sskip (content-type already %q) %s\n", formatProgressBracket(n, progressWidth), contentType, relPath)
					atomic.AddInt64(&unchanged, 1)
				}
			}
		}()
	}

	for _, f := range files {
		if f.URLPrivate == "" {
			n := atomic.AddInt64(progressSeq, 1)
			fmt.Printf("%sskip (empty url)\n", formatProgressBracket(n, progressWidth))
			atomic.AddInt64(&skipped, 1)
			continue
		}
		workCh <- work{urlPrivate: f.URLPrivate, name: f.Name}
	}
	close(workCh)
	wg.Wait()

	prefix := "sync-ct: "
	if mirrorDryRun {
		prefix = "sync-ct (dry-run): "
	}
	fmt.Printf("%supdated %d, unchanged %d, skipped %d\n", prefix, updated, unchanged, skipped)
	return nil
}
