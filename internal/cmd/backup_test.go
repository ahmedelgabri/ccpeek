package cmd

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/sqliteutil"
	"github.com/spf13/cobra"
)

func archiveCommand(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "ccpeek", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("data-file", filepath.Join(t.TempDir(), "unused-legacy.db"), "")
	root.PersistentFlags().String("index-file", "", "")
	root.AddCommand(newBackupCommand(), newRestoreCommand())
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	return output.String(), err
}

func TestBackupRestoreCommandsPreserveWALAndUserState(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source?#.db")
	backup := filepath.Join(dir, "backup.db")
	restored := filepath.Join(dir, "restored.db")
	store, err := db.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta(ctx, "command-test", "committed in WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO user_annotations(entity_type,natural_key,kind,value_json,created_at) VALUES('session','synthetic','note','{}','2026-07-01')`); err != nil {
		t.Fatal(err)
	}
	if stat, err := os.Stat(source + "-wal"); err != nil || stat.Size() == 0 {
		t.Fatalf("fixture has no WAL: %v", err)
	}
	out, err := archiveCommand(t, ctx, "--index-file", source, "backup", backup)
	if err != nil || !strings.Contains(out, "Backup written to "+backup) {
		t.Fatalf("backup: %s %v", out, err)
	}
	out, err = archiveCommand(t, ctx, "restore", backup, "--index-file", restored)
	if err != nil || !strings.Contains(out, "Archive restored to "+restored) {
		t.Fatalf("restore: %s %v", out, err)
	}
	other, err := db.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	value, ok, err := other.GetMeta(ctx, "command-test")
	if err != nil || !ok || value != "committed in WAL" {
		t.Fatalf("restored meta=%q %v", value, err)
	}
	var notes int
	if err := other.ReadDB().QueryRow(`SELECT COUNT(*) FROM user_annotations WHERE kind='note'`).Scan(&notes); err != nil || notes != 1 {
		t.Fatalf("notes=%d %v", notes, err)
	}
	for _, args := range [][]string{{"--index-file", source, "backup", backup}, {"restore", backup, "--index-file", source}} {
		if _, err := archiveCommand(t, ctx, args...); err == nil {
			t.Fatalf("overwrote existing path: %v", args)
		}
	}
	value, _, err = store.GetMeta(ctx, "command-test")
	if err != nil || value != "committed in WAL" {
		t.Fatalf("live archive changed: %q %v", value, err)
	}
}

func TestArchiveCommandsRejectInvalidInputs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.db")
	target := filepath.Join(dir, "new.db")
	if err := os.WriteFile(bad, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"backup"},
		{"restore"},
		{"restore", bad},
		{"--index-file", filepath.Join(dir, "missing.db"), "backup", target},
		{"restore", bad, "--index-file", target},
	} {
		if _, err := archiveCommand(t, ctx, args...); err == nil {
			t.Errorf("accepted invalid arguments: %v", args)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("created destination on error: %v", err)
		}
	}
}

func TestBackupCommandDoesNotMigrateSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "old.db")
	target := filepath.Join(dir, "backup.db")
	store, err := db.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB().Exec(`DROP TABLE usage_claim_versions; UPDATE meta SET value='17' WHERE key='schema_version'`)
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archiveCommand(t, ctx, "--index-file", source, "backup", target); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{source, target} {
		sqlDB, err := sql.Open("sqlite", sqliteutil.URI(path, "mode=ro"))
		if err != nil {
			t.Fatal(err)
		}
		var version string
		err = sqlDB.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version)
		sqlDB.Close()
		if err != nil || version != "17" {
			t.Fatalf("%s version=%q %v", path, version, err)
		}
	}
}

func TestBackupCommandCancellationStopsLockWait(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	target := filepath.Join(dir, "backup.db")
	store, err := db.Open(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, unlock, err := store.LockMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := archiveCommand(t, ctx, "--index-file", source, "backup", target); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("published canceled backup: %v", err)
	}
}
