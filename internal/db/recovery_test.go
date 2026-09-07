package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func TestUsageClaimSurvivesOwnerReparseAndPrune(t *testing.T) {
	ctx := context.Background()
	store, _ := openTemp(t)
	put := func(name string, tokens int64) int64 {
		t.Helper()
		w, err := store.BeginWrite(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Rollback()
		id, err := w.UpsertSession(canon.Session{Agent: "claude-code", ExternalID: name, SourcePath: "/" + name}, "h")
		if err != nil {
			t.Fatal(err)
		}
		if err := w.ClearSessionChildren(id); err != nil {
			t.Fatal(err)
		}
		if err := w.WriteMessage(id, "claude-code", canon.Message{Seq: 0, ContentID: "shared", Role: canon.RoleAssistant, Text: "retained text", Usage: &canon.Usage{InputTokens: tokens, OutputTokens: 10, RequestID: "req"}}); err != nil {
			t.Fatal(err)
		}
		if err := w.RecordSourceFile("/"+name, "claude-code", "h", "", "", 1); err != nil {
			t.Fatal(err)
		}
		if err := w.Commit(); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owner := put("owner", 100)
	put("duplicate", 200)
	put("owner", 100)
	if n := count(t, store, `SELECT SUM(input_tokens) FROM message_usage`); n != 200 {
		t.Fatalf("reparse lost rich observation: %d", n)
	}
	if _, err := store.PruneMissingSources(ctx, func(path string) bool { return path != "/owner" }); err != nil {
		t.Fatal(err)
	}
	if n := count(t, store, `SELECT SUM(input_tokens) FROM message_usage`); n != 200 {
		t.Fatalf("prune lost usage: %d", n)
	}
	if n := count(t, store, `SELECT COUNT(*) FROM messages WHERE session_id=?`, owner); n != 0 {
		t.Fatal("owner was not pruned")
	}
	if n := count(t, store, `SELECT COUNT(*) FROM message_usage`); n != 1 {
		t.Fatalf("duplicate billing: %d", n)
	}
}

func TestMaintenanceLocksSeparateHandlesWithoutBlockingReaders(t *testing.T) {
	a, path := openTemp(t)
	b, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx, unlock, err := a.LockMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, nested, err := a.LockMaintenance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nested()
	deadline, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, release, err := b.LockMaintenance(deadline); err == nil {
		release()
		t.Fatal("second handle acquired held maintenance lock")
	}
	if n := count(t, b, `SELECT COUNT(*) FROM meta`); n == 0 {
		t.Fatal("reader unavailable")
	}
}

func TestBackupRestoreIncludesWALAndUserState(t *testing.T) {
	ctx := context.Background()
	store, _ := openTemp(t)
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO user_annotations(entity_type,natural_key,kind,created_at) VALUES('test','key','ignore','now')`); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	backup := filepath.Join(directory, "backup?#.db")
	if err := store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := store.Backup(ctx, backup); err == nil {
		t.Fatal("backup overwrote existing file")
	}
	restored := filepath.Join(directory, "restored.db")
	if err := Restore(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, backup, restored); err == nil {
		t.Fatal("restore overwrote existing archive")
	}
	db, err := Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := count(t, db, `SELECT COUNT(*) FROM user_annotations`); n != 1 {
		t.Fatalf("annotations=%d", n)
	}
	if info, err := os.Stat(backup); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("backup permissions: %v %v", info, err)
	}
	bad := filepath.Join(directory, "bad.db")
	if err := os.WriteFile(bad, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, bad, filepath.Join(directory, "should-not-exist.db")); err == nil {
		t.Fatal("invalid backup accepted")
	}
}
