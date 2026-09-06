package ingest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/opencode"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func TestSQLiteSourceReconcilesDeletedSessions(t *testing.T) {
	runner, store := newRunner(t)
	runner.adapters = append(runner.adapters, opencode.New())
	root := t.TempDir()
	source, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	for _, q := range []string{
		`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`,
		`CREATE TABLE session(id TEXT PRIMARY KEY,title TEXT,directory TEXT,time_created INTEGER,time_updated INTEGER,parent_id TEXT)`,
		`CREATE TABLE message(id TEXT PRIMARY KEY,session_id TEXT,data TEXT)`,
		`CREATE TABLE part(id TEXT PRIMARY KEY,session_id TEXT,message_id TEXT,data TEXT)`,
		`INSERT INTO session VALUES('ses_1','SQLite session','/project',1751443200000,1751443200000,NULL)`,
		`INSERT INTO message VALUES('msg_1','ses_1','{"role":"user"}')`,
		`INSERT INTO part VALUES('prt_1','ses_1','msg_1','{"type":"text","text":"WAL text"}')`,
	} {
		if _, err := source.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{ConfigRoots: map[canon.AgentSlug][]string{opencode.Slug: {root}}, Home: "/nonexistent-home", Getenv: func(string) string { return "" }}
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Fatalf("sessions=%d", n)
	}
	for _, q := range []string{`DELETE FROM part`, `DELETE FROM message`, `DELETE FROM session`} {
		if _, err := source.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Fatalf("stale sessions=%d", n)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM search_docs`); n != 0 {
		t.Fatalf("stale search docs=%d", n)
	}
}
