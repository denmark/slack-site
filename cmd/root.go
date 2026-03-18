package cmd

import (
	"github.com/spf13/cobra"
)

// Data file names used under --data/--output directories. Shared by ingest, serve, reindex.
const (
	DBFileName    = "slack.db"
	BleveIndexDir = "slack.bleve"
)

var rootCmd = &cobra.Command{
	Use:   "slack-site",
	Short: "Slack export ingestion and search",
	Long:  "CLI for ingesting Slack workspace exports into SQLite and Bleve search.",
}

func Execute() error {
	return rootCmd.Execute()
}
