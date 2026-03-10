package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "slack-site",
	Short: "Slack export ingestion and search",
	Long:  "CLI for ingesting Slack workspace exports into SQLite and Bleve search.",
}

func Execute() error {
	return rootCmd.Execute()
}
