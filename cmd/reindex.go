package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/denmark/slack-site/db"
	"github.com/denmark/slack-site/models"
	"github.com/denmark/slack-site/search"
	"github.com/spf13/cobra"
)

var reindexDataDir string

func init() {
	reindexCmd := &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the Bleve index from an existing database",
		Long:  "Reads messages from " + db.DBFileName + " in --data and builds a new " + search.IndexDir + " index (overwrites existing index).",
		RunE:  runReindex,
	}
	reindexCmd.Flags().StringVar(&reindexDataDir, "data", "", "Path to directory containing "+db.DBFileName)
	_ = reindexCmd.MarkFlagRequired("data")
	rootCmd.AddCommand(reindexCmd)
}

func runReindex(cmd *cobra.Command, args []string) error {
	dbPath := filepath.Join(reindexDataDir, db.DBFileName)
	database, err := db.OpenReadOnly(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	indexPath := search.IndexPath(reindexDataDir)
	idx, err := search.NewIndex(reindexDataDir)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer search.Close(idx)

	ctx := context.Background()
	var total int64
	var lastConvID, lastTs string
	for {
		var batch []models.MessageRow
		q := database.NewSelect().Model(&batch).
			Column("conversation_id", "conversation_type", "user_id", "type", "ts", "text", "user_profile_name", "team").
			Order("conversation_id", "ts").
			Limit(search.MessageIndexBatchSize)
		if lastConvID != "" || lastTs != "" {
			// Keyset pagination: (conversation_id, ts) > (last, last) uses the unique index, O(1) seek per batch
			q = q.Where("(conversation_id, ts) > (?, ?)", lastConvID, lastTs)
		}
		err := q.Scan(ctx)
		if err != nil {
			return fmt.Errorf("scan messages: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		docs := make([]*models.SearchDocument, len(batch))
		for i := range batch {
			docs[i] = search.SearchDocumentForMessageRow(&batch[i])
		}
		if err := search.BatchIndexMessages(idx, docs); err != nil {
			return fmt.Errorf("index batch: %w", err)
		}
		total += int64(len(batch))
		if total%50000 < int64(len(batch)) {
			fmt.Printf("  indexed %d messages...\n", total)
		}
		lastConvID = batch[len(batch)-1].ConversationID
		lastTs = batch[len(batch)-1].Ts
		if len(batch) < search.MessageIndexBatchSize {
			break
		}
	}

	fmt.Printf("Reindex complete: %d messages in %s\n", total, indexPath)
	return nil
}
