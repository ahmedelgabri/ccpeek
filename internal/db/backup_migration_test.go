package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/sqliteutil"
)

func TestBackupFileDoesNotMigrateSource(t *testing.T) {
	ctx := context.Background()
	store, path := openTemp(t)
	if err := store.SetMeta(ctx, "schema_version", "16"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "before-upgrade.db")
	if err := BackupFile(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, backup} {
		db, err := sql.Open("sqlite", sqliteutil.URI(file, "mode=ro"))
		if err != nil {
			t.Fatal(err)
		}
		var version int
		err = db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version)
		db.Close()
		if err != nil || version != 16 {
			t.Fatalf("%s: version=%d err=%v", file, version, err)
		}
	}
}
