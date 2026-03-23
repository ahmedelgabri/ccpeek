package store

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

func TestMigrateV15ToV16AddsColumnsAndIsIdempotent(t *testing.T) {
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(initialSchema); err != nil {
		t.Fatalf("apply initial schema: %v", err)
	}

	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateV15ToV16(context.Background(), tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("first migrateV15ToV16 failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateV15ToV16(context.Background(), tx2); err != nil {
		_ = tx2.Rollback()
		t.Fatalf("second migrateV15ToV16 should be idempotent, got: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		table  string
		column string
	}{
		{"projects", "source"},
		{"projects", "updated_at_ms"},
		{"plans", "updated_at_ms"},
		{"plans", "source"},
		{"todos", "updated_at_ms"},
		{"todos", "source"},
		{"file_history", "updated_at_ms"},
		{"file_history", "source"},
		{"history", "project_dir"},
		{"history", "source"},
		{"commands", "source"},
		{"memories", "source"},
		{"sessions", "metadata_only"},
		{"sessions", "model_name"},
		{"sessions", "source"},
		{"shell_snapshots", "kind"},
		{"shell_snapshots", "project_path"},
		{"shell_snapshots", "commit_hash"},
		{"shell_snapshots", "detail_file"},
		{"shell_snapshots", "source"},
		{"file_versions", "file_path"},
		{"file_versions", "change_kind"},
		{"file_versions", "patch"},
		{"file_versions", "timestamp"},
	}
	tx3, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx3.Rollback()
	for _, c := range checks {
		ok, err := columnExists(tx3, c.table, c.column)
		if err != nil {
			t.Fatalf("columnExists(%s.%s): %v", c.table, c.column, err)
		}
		if !ok {
			t.Fatalf("expected column %s.%s to exist after v16 migration", c.table, c.column)
		}
	}
}

func TestOpenEnsuresV16ColumnsPresent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	required := []struct {
		table  string
		column string
	}{
		{"sessions", "metadata_only"},
		{"sessions", "model_name"},
		{"sessions", "source"},
		{"projects", "updated_at_ms"},
		{"plans", "updated_at_ms"},
		{"file_versions", "file_path"},
		{"shell_snapshots", "kind"},
	}
	for _, c := range required {
		ok, err := columnExists(tx, c.table, c.column)
		if err != nil {
			t.Fatalf("columnExists(%s.%s): %v", c.table, c.column, err)
		}
		if !ok {
			t.Fatalf("missing required v16 column %s.%s", c.table, c.column)
		}
	}
}

func columnExists(tx *sqlx.Tx, table, column string) (bool, error) {
	var count int
	q := "SELECT COUNT(*) FROM pragma_table_info('" + table + "') WHERE name = ?"
	if err := tx.Get(&count, q, column); err != nil {
		return false, err
	}
	return count > 0, nil
}
