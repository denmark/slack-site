package cmd

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/denmark/slack-site/db"
	"github.com/denmark/slack-site/internal/server"
	"github.com/denmark/slack-site/search"
	"github.com/spf13/cobra"
)

var (
	serveDataDir   string
	serveAddr      string
	serveMirrorBase string
)

func init() {
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the ingested Slack export in a browser",
		Long:  "Starts an HTTP server and opens the browser. Requires --data pointing to a directory containing " + db.DBFileName + " and " + search.IndexDir + " (same as ingest --data).",
		RunE:  runServe,
	}
	serveCmd.Flags().StringVar(&serveDataDir, "data", "", "Path to directory containing "+db.DBFileName+" and "+search.IndexDir+" (same directory as ingest --data)")
	serveCmd.Flags().StringVar(&serveAddr, "addr", ":8080", "Listen address (e.g. :8080 or localhost:8080)")
	serveCmd.Flags().StringVar(&serveMirrorBase, "mirror", "", "Base URL for message file links (e.g. https://cdn.example.com/files); if set, file URLs are base + path from url_private")
	_ = serveCmd.MarkFlagRequired("data")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	dbPath := filepath.Join(serveDataDir, db.DBFileName)
	database, err := db.OpenReadOnly(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	indexPath := search.IndexPath(serveDataDir)
	bleveIdx, err := search.OpenExisting(indexPath)
	if err != nil {
		// Index optional for browse; search will show empty
		bleveIdx = nil
	} else {
		defer search.Close(bleveIdx)
	}

	srv, err := server.New(database, bleveIdx, "", serveMirrorBase)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)

	listener, err := net.Listen("tcp", serveAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	url := "http://" + listener.Addr().String()

	fmt.Println("Serving at", url)
	openBrowser(url)

	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return httpServer.Serve(listener)
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if c != nil {
		_ = c.Start()
	}
}
