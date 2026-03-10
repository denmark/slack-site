package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/denmark/slack-site/db"
	"github.com/denmark/slack-site/models"
	"github.com/denmark/slack-site/search"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"
)

const (
	dbBatchSize       = 2000 // Bun bulk insert chunk size
	bleveBatchSize    = 500  // Bleve batch index size (recommended 100-1000)
	messageProgressAt = 50000 // Print message progress every N messages
)

var (
	inputDir  string
	outputDir string
)

func init() {
	ingestCmd := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest a Slack export into SQLite and Bleve",
		Long:  "Reads a Slack export from --input, creates slack.db and slack.bleve in --output.",
		RunE:  runIngest,
	}
	ingestCmd.Flags().StringVar(&inputDir, "input", "", "Path to Slack export directory (e.g. .../chairish-slack)")
	ingestCmd.Flags().StringVar(&outputDir, "output", "", "Path to output directory (slack.db and slack.bleve will be created here)")
	_ = ingestCmd.MarkFlagRequired("input")
	_ = ingestCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(ingestCmd)
}

type convInfo struct {
	id   string
	ctype string
}

func runIngest(cmd *cobra.Command, args []string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	dbPath := filepath.Join(outputDir, "slack.db")
	database, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	bleveIdx, err := search.NewIndex(outputDir)
	if err != nil {
		return fmt.Errorf("create bleve index: %w", err)
	}
	defer search.Close(bleveIdx)

	ctx := context.Background()
	convMap := make(map[string]convInfo)

	fmt.Println("Starting ingest...")
	if err := ingestUsers(ctx, database, bleveIdx, inputDir); err != nil {
		return fmt.Errorf("ingest users: %w", err)
	}
	if err := ingestChannels(ctx, database, inputDir, convMap); err != nil {
		return fmt.Errorf("ingest channels: %w", err)
	}
	if err := ingestGroups(ctx, database, inputDir, convMap); err != nil {
		return fmt.Errorf("ingest groups: %w", err)
	}
	if err := ingestDMs(ctx, database, inputDir, convMap); err != nil {
		return fmt.Errorf("ingest dms: %w", err)
	}
	if err := ingestMPIMs(ctx, database, inputDir, convMap); err != nil {
		return fmt.Errorf("ingest mpims: %w", err)
	}
	if err := ingestMessages(ctx, database, bleveIdx, inputDir, convMap); err != nil {
		return fmt.Errorf("ingest messages: %w", err)
	}

	fmt.Println("Ingest complete: slack.db and slack.bleve created in", outputDir)
	return nil
}

func readJSON(path string, out interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func ingestUsers(ctx context.Context, database *bun.DB, idx bleve.Index, inputDir string) error {
	var users []models.User
	if err := readJSON(filepath.Join(inputDir, "users.json"), &users); err != nil {
		return err
	}
	rows := make([]models.UserRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, models.UserRow{
			ID:          u.ID,
			TeamID:      u.TeamID,
			Name:        u.Name,
			Deleted:     u.Deleted,
			RealName:    u.Profile.RealName,
			DisplayName: u.Profile.DisplayName,
			Email:       u.Profile.Email,
			IsBot:       u.IsBot,
			IsAppUser:   u.IsAppUser,
			Updated:     u.Updated,
		})
	}
	if _, err := database.NewInsert().Model(&rows).Exec(ctx); err != nil {
		return err
	}
	for i := range users {
		if err := search.IndexUser(idx, &users[i]); err != nil {
			return err
		}
	}
	fmt.Printf("  users: %d\n", len(rows))
	return nil
}

func ingestChannels(ctx context.Context, database *bun.DB, inputDir string, convMap map[string]convInfo) error {
	var channels []models.Channel
	if err := readJSON(filepath.Join(inputDir, "channels.json"), &channels); err != nil {
		return err
	}
	rows := make([]models.ChannelRow, 0, len(channels))
	members := make([]models.ChannelMemberRow, 0)
	for _, c := range channels {
		convMap[c.ID] = convInfo{id: c.ID, ctype: "channel"}
		convMap[c.Name] = convInfo{id: c.ID, ctype: "channel"}
		rows = append(rows, models.ChannelRow{
			ID:             c.ID,
			Name:           c.Name,
			Created:        c.Created,
			Creator:        c.Creator,
			IsArchived:     c.IsArchived,
			IsGeneral:      c.IsGeneral,
			TopicValue:     c.Topic.Value,
			TopicCreator:   c.Topic.Creator,
			TopicLastSet:   c.Topic.LastSet,
			PurposeValue:   c.Purpose.Value,
			PurposeCreator: c.Purpose.Creator,
			PurposeLastSet: c.Purpose.LastSet,
		})
		for _, mid := range c.Members {
			members = append(members, models.ChannelMemberRow{ChannelID: c.ID, UserID: mid})
		}
	}
	if _, err := database.NewInsert().Model(&rows).Exec(ctx); err != nil {
		return err
	}
	for i := 0; i < len(members); i += dbBatchSize {
		end := i + dbBatchSize
		if end > len(members) {
			end = len(members)
		}
		chunk := members[i:end]
		if _, err := database.NewInsert().Model(&chunk).Ignore().Exec(ctx); err != nil {
			return err
		}
	}
	fmt.Printf("  channels: %d, channel members: %d\n", len(rows), len(members))
	return nil
}

func ingestGroups(ctx context.Context, database *bun.DB, inputDir string, convMap map[string]convInfo) error {
	var groups []models.Group
	if err := readJSON(filepath.Join(inputDir, "groups.json"), &groups); err != nil {
		return err
	}
	rows := make([]models.GroupRow, 0, len(groups))
	members := make([]models.GroupMemberRow, 0)
	for _, g := range groups {
		convMap[g.ID] = convInfo{id: g.ID, ctype: "group"}
		convMap[g.Name] = convInfo{id: g.ID, ctype: "group"}
		rows = append(rows, models.GroupRow{
			ID:             g.ID,
			Name:           g.Name,
			Created:        g.Created,
			Creator:        g.Creator,
			IsArchived:     g.IsArchived,
			TopicValue:     g.Topic.Value,
			TopicCreator:   g.Topic.Creator,
			TopicLastSet:   g.Topic.LastSet,
			PurposeValue:   g.Purpose.Value,
			PurposeCreator: g.Purpose.Creator,
			PurposeLastSet: g.Purpose.LastSet,
		})
		for _, mid := range g.Members {
			members = append(members, models.GroupMemberRow{GroupID: g.ID, UserID: mid})
		}
	}
	if _, err := database.NewInsert().Model(&rows).Exec(ctx); err != nil {
		return err
	}
	for i := 0; i < len(members); i += dbBatchSize {
		end := i + dbBatchSize
		if end > len(members) {
			end = len(members)
		}
		chunk := members[i:end]
		if _, err := database.NewInsert().Model(&chunk).Ignore().Exec(ctx); err != nil {
			return err
		}
	}
	fmt.Printf("  groups: %d, group members: %d\n", len(rows), len(members))
	return nil
}

func ingestDMs(ctx context.Context, database *bun.DB, inputDir string, convMap map[string]convInfo) error {
	var dms []models.DM
	if err := readJSON(filepath.Join(inputDir, "dms.json"), &dms); err != nil {
		return err
	}
	rows := make([]models.DMRow, 0, len(dms))
	members := make([]models.DMMemberRow, 0)
	for _, d := range dms {
		convMap[d.ID] = convInfo{id: d.ID, ctype: "dm"}
		rows = append(rows, models.DMRow{ID: d.ID, Created: d.Created})
		for _, mid := range d.Members {
			members = append(members, models.DMMemberRow{DMID: d.ID, UserID: mid})
		}
	}
	if _, err := database.NewInsert().Model(&rows).Exec(ctx); err != nil {
		return err
	}
	for i := 0; i < len(members); i += dbBatchSize {
		end := i + dbBatchSize
		if end > len(members) {
			end = len(members)
		}
		chunk := members[i:end]
		if _, err := database.NewInsert().Model(&chunk).Ignore().Exec(ctx); err != nil {
			return err
		}
	}
	fmt.Printf("  dms: %d, dm members: %d\n", len(rows), len(members))
	return nil
}

func ingestMPIMs(ctx context.Context, database *bun.DB, inputDir string, convMap map[string]convInfo) error {
	var mpims []models.MPIM
	if err := readJSON(filepath.Join(inputDir, "mpims.json"), &mpims); err != nil {
		return err
	}
	rows := make([]models.MPIMRow, 0, len(mpims))
	members := make([]models.MPIMMemberRow, 0)
	for _, m := range mpims {
		convMap[m.ID] = convInfo{id: m.ID, ctype: "mpim"}
		convMap[m.Name] = convInfo{id: m.ID, ctype: "mpim"}
		rows = append(rows, models.MPIMRow{
			ID:             m.ID,
			Name:           m.Name,
			Created:        m.Created,
			Creator:        m.Creator,
			IsArchived:     m.IsArchived,
			TopicValue:     m.Topic.Value,
			TopicCreator:   m.Topic.Creator,
			TopicLastSet:   m.Topic.LastSet,
			PurposeValue:   m.Purpose.Value,
			PurposeCreator: m.Purpose.Creator,
			PurposeLastSet: m.Purpose.LastSet,
		})
		for _, mid := range m.Members {
			members = append(members, models.MPIMMemberRow{MPIMID: m.ID, UserID: mid})
		}
	}
	if _, err := database.NewInsert().Model(&rows).Exec(ctx); err != nil {
		return err
	}
	for i := 0; i < len(members); i += dbBatchSize {
		end := i + dbBatchSize
		if end > len(members) {
			end = len(members)
		}
		chunk := members[i:end]
		if _, err := database.NewInsert().Model(&chunk).Ignore().Exec(ctx); err != nil {
			return err
		}
	}
	fmt.Printf("  mpims: %d, mpim members: %d\n", len(rows), len(members))
	return nil
}

func ingestMessages(ctx context.Context, database *bun.DB, idx bleve.Index, inputDir string, convMap map[string]convInfo) error {
	messageRows := make([]models.MessageRow, 0, dbBatchSize)
	searchDocs := make([]*models.SearchDocument, 0, bleveBatchSize)
	var totalMessages int64

	flushMessages := func() error {
		if len(messageRows) == 0 {
			return nil
		}
		if _, err := database.NewInsert().Model(&messageRows).Exec(ctx); err != nil {
			return err
		}
		totalMessages += int64(len(messageRows))
		messageRows = messageRows[:0]
		return nil
	}
	flushBleve := func() error {
		if len(searchDocs) == 0 {
			return nil
		}
		if err := search.BatchIndexMessages(idx, searchDocs); err != nil {
			return err
		}
		searchDocs = searchDocs[:0]
		return nil
	}

	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		info, ok := convMap[name]
		if !ok {
			continue
		}
		dirPath := filepath.Join(inputDir, name)
		subs, err := os.ReadDir(dirPath)
		if err != nil {
			return err
		}
		for _, sub := range subs {
			if sub.IsDir() || !strings.HasSuffix(sub.Name(), ".json") {
				continue
			}
			var messages []models.Message
			if err := readJSON(filepath.Join(dirPath, sub.Name()), &messages); err != nil {
				return err
			}
			for _, msg := range messages {
				userProfileName := ""
				if msg.UserProfile != nil {
					userProfileName = msg.UserProfile.Name
				}
				messageRows = append(messageRows, models.MessageRow{
					ConversationID:   info.id,
					ConversationType: info.ctype,
					UserID:           msg.User,
					Type:             msg.Type,
					Ts:               msg.Ts,
					ClientMsgID:      msg.ClientMsgID,
					Text:             msg.Text,
					UserProfileName:  userProfileName,
					Team:             msg.Team,
					UserTeam:         msg.UserTeam,
					SourceTeam:       msg.SourceTeam,
				})
				searchDocs = append(searchDocs, search.SearchDocumentForMessage(info.id, info.ctype, msg.Ts, &msg))
				if len(messageRows) >= dbBatchSize {
					if err := flushMessages(); err != nil {
						return err
					}
					// Print progress every messageProgressAt messages (e.g. 50k)
					if totalMessages > 0 && totalMessages%messageProgressAt < int64(dbBatchSize) {
						fmt.Printf("  messages: %d...\n", totalMessages)
					}
					// Flush Bleve in smaller chunks (recommended 100-1000)
					for i := 0; i < len(searchDocs); i += bleveBatchSize {
						end := i + bleveBatchSize
						if end > len(searchDocs) {
							end = len(searchDocs)
						}
						if err := search.BatchIndexMessages(idx, searchDocs[i:end]); err != nil {
							return err
						}
					}
					searchDocs = searchDocs[:0]
				}
			}
		}
	}
	if err := flushMessages(); err != nil {
		return err
	}
	if err := flushBleve(); err != nil {
		return err
	}
	fmt.Printf("  messages: %d\n", totalMessages)
	return nil
}
