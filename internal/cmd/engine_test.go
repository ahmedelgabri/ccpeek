package cmd

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
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
	if v, _, _ := eng.store.GetMeta(ctx, "v1_import_state"); v != "success" {
		t.Errorf("v1_import_state = %q, want success", v)
	}
	if _, ok, _ := eng.store.GetMeta(ctx, "v1_imported_at"); !ok {
		t.Error("v1_imported_at meta missing")
	}

	queryInt := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := eng.store.DB().QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}

	// Both v1 sessions came over: the ghost (source gone) and the "live"
	// one — its file exists on disk but OUTSIDE the configured roots, so
	// the ingest never saw it and only the import can rescue it. Skipping
	// is keyed on what v2 actually holds, not on file existence.
	if n := queryInt(`SELECT COUNT(*) FROM sessions WHERE external_id = 'ghost-session' AND origin = 'imported-v1'`); n != 1 {
		t.Errorf("imported ghost sessions = %d, want 1", n)
	}
	if n := queryInt(`SELECT COUNT(*) FROM sessions WHERE external_id = 'live-session' AND origin = 'imported-v1'`); n != 1 {
		t.Errorf("live-session (outside roots) imported = %d rows, want 1", n)
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
	if n := queryInt(`SELECT COUNT(*) FROM sessions WHERE origin = 'imported-v1'`); n != 2 {
		t.Errorf("imported sessions after reopen = %d, want 2 (no duplicate import)", n)
	}
}

// TestV1ImportFailureRetries proves the outcome split: a failing v1
// import leaves the engine usable (bootstrap done, migrated_at stamped)
// but records state=failed with the error retained and no
// v1_imported_at; the next start retries, and once the import succeeds
// the error clears, v1_imported_at is stamped, and the data is in.
// Because retry keys on v1_import_state alone, this is also the path a
// database stamped before the split takes: migrated_at no longer stops
// the import.
func TestV1ImportFailureRetries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "ccpeek.db")

	// A v1 "database" sqlite cannot read.
	if err := os.WriteFile(dataFile, []byte("this is not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}

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
		t.Fatalf("a failing v1 import must not fail the engine: %v", err)
	}
	meta := func(e *engine, key string) (string, bool) {
		t.Helper()
		v, ok, err := e.store.GetMeta(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		return v, ok
	}
	if _, ok := meta(eng, "migrated_at"); !ok {
		t.Error("migrated_at missing: bootstrap did not complete")
	}
	if v, _ := meta(eng, "v1_import_state"); v != "failed" {
		t.Errorf("v1_import_state = %q, want failed", v)
	}
	if v, _ := meta(eng, "v1_import_error"); v == "" {
		t.Error("v1_import_error empty: the failure is invisible")
	}
	if _, ok := meta(eng, "v1_imported_at"); ok {
		t.Error("v1_imported_at stamped despite failure")
	}
	eng.Close()

	// Fix the v1 database; the next start retries without any flag.
	if err := os.Remove(dataFile); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "live.jsonl")
	if err := os.WriteFile(live, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedV1DB(t, dataFile, live)

	eng2, err := openEngine(ctx, cmd, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if v, _ := meta(eng2, "v1_import_state"); v != "success" {
		t.Errorf("v1_import_state after retry = %q, want success", v)
	}
	if v, _ := meta(eng2, "v1_import_error"); v != "" {
		t.Errorf("v1_import_error not cleared: %q", v)
	}
	if _, ok := meta(eng2, "v1_imported_at"); !ok {
		t.Error("v1_imported_at missing after successful retry")
	}
	var n int
	if err := eng2.store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE origin = 'imported-v1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("imported sessions after retry = %d, want 2", n)
	}

	// A third start must not import again.
	eng3, err := openEngine(ctx, cmd, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer eng3.Close()
	if n := func() int {
		var n int
		if err := eng3.store.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessions WHERE origin = 'imported-v1'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}(); n != 2 {
		t.Errorf("imported sessions after third start = %d, want 2", n)
	}
}

// TestV1ImportStatErrorIsFailure proves an UNREACHABLE legacy file (a
// stat error other than not-exist) classifies as a failed attempt —
// recorded and retried — never as no-legacy-db, which is permanent.
func TestV1ImportStatErrorIsFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not bind root")
	}
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	dataFile := filepath.Join(locked, "ccpeek.db")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	maybeImportV1(ctx, store, dataFile, io.Discard)
	meta := func(key string) (string, bool) {
		t.Helper()
		v, ok, err := store.GetMeta(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		return v, ok
	}
	if v, _ := meta("v1_import_state"); v != "failed" {
		t.Fatalf("v1_import_state = %q, want failed (a stat error is not no-legacy-db)", v)
	}
	if v, _ := meta("v1_import_error"); v == "" {
		t.Error("v1_import_error empty for the unreachable legacy file")
	}

	// Reachable again: the same call retries and succeeds.
	if err := os.Chmod(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(locked, "live.jsonl")
	if err := os.WriteFile(live, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedV1DB(t, dataFile, live)
	maybeImportV1(ctx, store, dataFile, io.Discard)
	if v, _ := meta("v1_import_state"); v != "success" {
		t.Errorf("v1_import_state after retry = %q, want success", v)
	}
	if v, _ := meta("v1_import_error"); v != "" {
		t.Errorf("v1_import_error not cleared: %q", v)
	}

	// A truly absent file (fresh store) records no-legacy-db.
	store2, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	maybeImportV1(ctx, store2, filepath.Join(t.TempDir(), "nope.db"), io.Discard)
	if v, _, _ := store2.GetMeta(ctx, "v1_import_state"); v != "no-legacy-db" {
		t.Errorf("v1_import_state for missing file = %q, want no-legacy-db", v)
	}

	// failed → absent: the terminal state must not carry the stale error.
	store3, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store3.Close()
	goneDir := t.TempDir()
	garbage := filepath.Join(goneDir, "ccpeek.db")
	if err := os.WriteFile(garbage, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	maybeImportV1(ctx, store3, garbage, io.Discard)
	if v, _, _ := store3.GetMeta(ctx, "v1_import_state"); v != "failed" {
		t.Fatalf("v1_import_state for garbage file = %q, want failed", v)
	}
	if err := os.Remove(garbage); err != nil {
		t.Fatal(err)
	}
	maybeImportV1(ctx, store3, garbage, io.Discard)
	if v, _, _ := store3.GetMeta(ctx, "v1_import_state"); v != "no-legacy-db" {
		t.Errorf("v1_import_state after removal = %q, want no-legacy-db", v)
	}
	if v, _, _ := store3.GetMeta(ctx, "v1_import_error"); v != "" {
		t.Errorf("v1_import_error stale after failed→absent transition: %q", v)
	}
}

// TestDataFileIsolation: two explicit --data-file paths in one
// directory must open two different stores — the old mapping reduced
// every path to <dir>/ccpeek2.db, silently aliasing independent
// profiles — while the default name keeps its historical sibling.
func TestDataFileIsolation(t *testing.T) {
	if got := storeDBPath("/x/ccpeek.db"); got != "/x/ccpeek2.db" {
		t.Errorf("default mapping = %q, want /x/ccpeek2.db", got)
	}
	if got := storeDBPath("/x/a.db"); got != "/x/a.v2.db" {
		t.Errorf("explicit mapping = %q, want /x/a.v2.db", got)
	}
	if a, b := storeDBPath("/x/a.db"), storeDBPath("/x/b.db"); a == b {
		t.Fatalf("two names alias one store: %q", a)
	}

	// End to end: data written through profile A must not appear in B.
	ctx := context.Background()
	dir := t.TempDir()
	emptyRoot := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", emptyRoot)
	t.Setenv("CODEX_HOME", emptyRoot)
	t.Setenv("OPENCODE_DATA_DIR", emptyRoot)
	t.Setenv("CCPEEK_CURSOR_DIR", emptyRoot)
	open := func(name string) *engine {
		t.Helper()
		cmd := &cobra.Command{}
		cmd.Flags().String("data-file", filepath.Join(dir, name), "")
		cmd.Flags().String("claude-dir", "", "")
		if err := cmd.Flags().Set("claude-dir", emptyRoot); err != nil {
			t.Fatal(err)
		}
		eng, err := openEngine(ctx, cmd, false, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		return eng
	}
	engA := open("a.db")
	if err := engA.store.SetMeta(ctx, "profile", "A"); err != nil {
		t.Fatal(err)
	}
	engA.Close()
	engB := open("b.db")
	defer engB.Close()
	if v, ok, _ := engB.store.GetMeta(ctx, "profile"); ok {
		t.Errorf("profile B sees profile A's data (%q): stores alias", v)
	}
}
