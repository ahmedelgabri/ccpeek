package cmd

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// seedV1DB writes a minimal v1-schema database at path: one session whose
// source file still exists (skipped by the importer), one orphaned
// session, and one ignored scan finding.
func seedV1DB(t *testing.T, path, liveSourcePath string) {
	t.Helper()
	v1, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer v1.Close()

	stmts := []string{
		`CREATE TABLE projects (id INTEGER PRIMARY KEY, dir_name TEXT, display_name TEXT, canonical_path TEXT)`,
		`CREATE TABLE sessions (
			id INTEGER PRIMARY KEY, session_id TEXT, project_id INTEGER,
			first_prompt TEXT, message_count INTEGER, created_at TEXT,
			modified_at TEXT, git_branch TEXT, project_path TEXT,
			source_path TEXT)`,
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY, session_id INTEGER, seq INTEGER, type TEXT,
			role TEXT, timestamp TEXT, uuid TEXT, content TEXT, cwd TEXT,
			git_branch TEXT)`,
		`CREATE TABLE plans (id INTEGER PRIMARY KEY, file_name TEXT, title TEXT,
			size_bytes INTEGER, content TEXT, source_path TEXT)`,
		`CREATE TABLE scan_findings (id INTEGER PRIMARY KEY, rule_id TEXT,
			description TEXT, source_type TEXT, source_id TEXT,
			match_redacted TEXT, line_number INTEGER, scanned_at TEXT,
			ignored INTEGER DEFAULT 0)`,
		`INSERT INTO projects (id, dir_name, canonical_path) VALUES (1, '-home-u-proj', '/home/u/proj')`,
		`INSERT INTO sessions (id, session_id, project_id, first_prompt, created_at, modified_at, source_path)
		 VALUES (1, 'live-session', 1, 'still here', '2026-06-01T10:00:00Z', '2026-06-01T11:00:00Z', '` + liveSourcePath + `')`,
		`INSERT INTO sessions (id, session_id, project_id, first_prompt, created_at, modified_at, git_branch, project_path, source_path)
		 VALUES (2, 'ghost-session', 1, 'lost to time', '2026-05-01T10:00:00Z', '2026-05-01T12:00:00Z', 'main', '/home/u/proj', '/gone/ghost.jsonl')`,
		`INSERT INTO messages (session_id, seq, type, role, timestamp, uuid, content)
		 VALUES (2, 0, 'user', 'user', '2026-05-01T10:00:00Z', 'g-1', '{"role":"user","content":"remember the auth fix?"}')`,
		`INSERT INTO scan_findings (rule_id, source_type, source_id, line_number, ignored)
		 VALUES ('aws-key', 'message', 'ghost-session', 42, 1)`,
	}
	for _, q := range stmts {
		if _, err := v1.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// TestFirstRunBootstrapImportsV1 covers the upgrade path end to end at
// the CLI-wiring layer (docs/v2-plan.md §8.1): a first run against a
// data-file that has a v1 database next to it must build the v2 store,
// import the v1 orphans and ignore flags, and stamp the migration meta —
// with zero flags.
func TestFirstRunBootstrapImportsV1(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "ccpeek.db")

	live := filepath.Join(dir, "live.jsonl")
	if err := os.WriteFile(live, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedV1DB(t, dataFile, live)

	// Pin every agent root away from the developer's real data: Claude via
	// the flag, the rest via their env overrides.
	emptyRoot := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", emptyRoot)
	t.Setenv("CODEX_HOME", emptyRoot)
	t.Setenv("OPENCODE_DATA_DIR", emptyRoot)
	t.Setenv("CCPEEK_CURSOR_DIR", emptyRoot)

	cmd := &cobra.Command{}
	cmd.Flags().String("data-file", dataFile, "")
	cmd.Flags().String("claude-dir", "", "")
	if err := cmd.Flags().Set("claude-dir", emptyRoot); err != nil {
		t.Fatal(err)
	}

	eng, err := openEngine(ctx, cmd, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if _, ok, err := eng.store.GetMeta(ctx, "migrated_at"); err != nil || !ok {
		t.Fatalf("migrated_at meta missing (ok=%v err=%v)", ok, err)
	}
	if _, ok, err := eng.store.GetMeta(ctx, "v1_import_report"); err != nil || !ok {
		t.Fatalf("v1_import_report meta missing (ok=%v err=%v)", ok, err)
	}

	queryInt := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := eng.store.DB().QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}

	// The orphan came over as imported-v1; the live session did not (its
	// source file still exists and would be re-ingested from disk).
	if n := queryInt(`SELECT COUNT(*) FROM sessions WHERE external_id = 'ghost-session' AND origin = 'imported-v1'`); n != 1 {
		t.Errorf("imported ghost sessions = %d, want 1", n)
	}
	if n := queryInt(`SELECT COUNT(*) FROM sessions WHERE external_id = 'live-session'`); n != 0 {
		t.Errorf("live-session imported = %d rows, want 0 (source still on disk)", n)
	}
	if n := queryInt(`SELECT COUNT(*) FROM user_annotations WHERE kind = 'scan_ignore'`); n != 1 {
		t.Errorf("imported ignore flags = %d, want 1", n)
	}

	// A second open of the same data-file must not re-import (idempotent
	// upgrade: migrated_at gates the first-run path).
	eng2, err := openEngine(ctx, cmd, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if n := queryInt(`SELECT COUNT(*) FROM sessions WHERE origin = 'imported-v1'`); n != 1 {
		t.Errorf("imported sessions after reopen = %d, want 1 (no duplicate import)", n)
	}
}
