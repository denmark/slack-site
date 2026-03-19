package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/denmark/slack-site/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
)

const (
	// BatchSize is the chunk size for Bun bulk inserts. Use for batching rows (messages, members, etc.) before insert.
	BatchSize = 2000
	// DBFileName is the SQLite database filename under a data directory (e.g. --output / --data).
	DBFileName = "slack.db"
)

// Open opens a SQLite database at path and returns a Bun DB. If path exists it is truncated (re-initialized).
func Open(path string) (*bun.DB, error) {
	_ = os.Remove(path)
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+path+"?cache=shared&mode=rwc")
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	if err := createSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return db, nil
}

// OpenReadOnly opens an existing SQLite database at path for read-only access. The file must already exist (e.g. created by ingest).
func OpenReadOnly(path string) (*bun.DB, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("database not found: %s (run ingest first)", path)
		}
		return nil, fmt.Errorf("stat database: %w", err)
	}
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+path+"?cache=shared&mode=ro")
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}
	return bun.NewDB(sqldb, sqlitedialect.New()), nil
}

// OpenReadWrite opens an existing SQLite database at path for read-write access without truncating. The file must already exist.
// It ensures the mirrored_files table exists (for mirror-files sub-command state).
func OpenReadWrite(path string) (*bun.DB, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("database not found: %s (run ingest first)", path)
		}
		return nil, fmt.Errorf("stat database: %w", err)
	}
	sqldb, err := sql.Open(sqliteshim.ShimName, "file:"+path+"?cache=shared&mode=rwc")
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	if err := ensureMirroredFilesTable(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensure mirrored_files table: %w", err)
	}
	return db, nil
}

// ensureMirroredFilesTable creates the mirrored_files table if it does not exist (for mirror-files re-entrancy state).
func ensureMirroredFilesTable(db *bun.DB) error {
	ctx := context.Background()
	_, err := db.NewCreateTable().Model((*models.MirroredFileRow)(nil)).IfNotExists().Exec(ctx)
	return err
}

func createSchema(db *bun.DB) error {
	ctx := context.Background()
	// Create tables in dependency order
	_, err := db.NewCreateTable().Model((*models.UserRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.ChannelRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.ChannelMemberRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.GroupRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.GroupMemberRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.DMRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.DMMemberRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.MPIMRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.MPIMMemberRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.MessageRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.MessageFileRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	// Unique index so message_files.message_conversation_id + message_ts can reference a single message
	_, err = db.NewCreateIndex().Model((*models.MessageRow)(nil)).Index("idx_messages_conversation_ts").Column("conversation_id", "ts").Unique().Exec(ctx)
	if err != nil {
		return err
	}
	// Unique index so duplicate file attachments are skipped when the same message is seen again
	_, err = db.NewCreateIndex().Model((*models.MessageFileRow)(nil)).Index("idx_message_files_message_file").Column("message_conversation_id", "message_ts", "slack_file_id").Unique().Exec(ctx)
	if err != nil {
		return err
	}
	_, err = db.NewCreateTable().Model((*models.MessageAttachmentRow)(nil)).Exec(ctx)
	if err != nil {
		return err
	}
	// Unique index so duplicate attachment rows are skipped when the same message is seen again
	_, err = db.NewCreateIndex().Model((*models.MessageAttachmentRow)(nil)).Index("idx_message_attachments_message_pos").Column("message_conversation_id", "message_ts", "position").Unique().Exec(ctx)
	if err != nil {
		return err
	}
	return nil
}
