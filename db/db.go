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
	return nil
}
