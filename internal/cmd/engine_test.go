package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/migrate"
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

// pinRoots points every agent but Claude Code at an empty directory, so
// no test can ingest the developer's real data. Claude Code is pinned by
// the returned command's --claude-dir.
func pinRoots(t *testing.T, dataFile, claudeDir string) *cobra.Command {
	t.Helper()
	emptyRoot := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", emptyRoot)
	t.Setenv("CODEX_HOME", emptyRoot)
	t.Setenv("OPENCODE_DATA_DIR", emptyRoot)
	t.Setenv("CCPEEK_CURSOR_DIR", emptyRoot)

	cmd := &cobra.Command{}
	cmd.Flags().String("data-file", dataFile, "")
	cmd.Flags().String("claude-dir", "", "")
	if err := cmd.Flags().Set("claude-dir", claudeDir); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// writeHistoryFile writes a Claude Code history.jsonl holding exactly the
// given prompts.
func writeHistoryFile(t *testing.T, root string, entries ...v1HistoryRow) {
	t.Helper()
	body := ""
	for _, e := range entries {
		body += `{"display":` + strconv.Quote(e.display) +
			`,"timestamp":` + strconv.FormatInt(e.ts, 10) + "}\n"
	}
	if err := os.WriteFile(filepath.Join(root, "history.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeClaudeSession writes a minimal transcript under a Claude Code
// root's projects/ tree, the shape the adapter discovers and parses.
func writeClaudeSession(t *testing.T, root, sessionID, prompt string) string {
	t.Helper()
	dir := filepath.Join(root, "projects", "-home-u-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	body := `{"parentUuid":null,"isSidechain":false,"userType":"external","cwd":"/home/u/proj","sessionId":"` +
		sessionID + `","version":"2.1.0","gitBranch":"main","type":"user","message":{"role":"user","content":` +
		strconv.Quote(prompt) + `},"uuid":"u-1","timestamp":"2026-07-01T10:00:00.000Z"}
{"parentUuid":"u-1","isSidechain":false,"userType":"external","cwd":"/home/u/proj","sessionId":"` +
		sessionID + `","version":"2.1.0","gitBranch":"main","type":"assistant","requestId":"req-1","message":{"id":"msg-1","type":"message","role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"on it"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":4}},"uuid":"a-1","timestamp":"2026-07-01T10:00:05.000Z"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type v1HistoryRow struct {
	display string
	ts      int64
}

// v1Skeleton is the minimum every v1 vintage has: the sessions/messages
// pair ImportV1 recognises a v1 database by, and the projects table its
// session query joins.
const v1Skeleton = `
	CREATE TABLE projects (id INTEGER PRIMARY KEY, dir_name TEXT,
		display_name TEXT, canonical_path TEXT);
	CREATE TABLE sessions (
		id INTEGER PRIMARY KEY, session_id TEXT, project_id INTEGER,
		first_prompt TEXT, message_count INTEGER, created_at TEXT,
		modified_at TEXT, git_branch TEXT, project_path TEXT,
		source_path TEXT);
	CREATE TABLE messages (
		id INTEGER PRIMARY KEY, session_id INTEGER, seq INTEGER, type TEXT,
		role TEXT, timestamp TEXT, uuid TEXT, content TEXT, cwd TEXT,
		git_branch TEXT);`

// seedV1HistoryDB writes a v1 database holding nothing but prompt
// history, every row carrying sourcePath — which on a same-machine
// upgrade is the live history.jsonl v1 read them from.
func seedV1HistoryDB(t *testing.T, path, sourcePath string, entries ...v1HistoryRow) {
	t.Helper()
	v1, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer v1.Close()
	if _, err := v1.Exec(v1Skeleton + `
		CREATE TABLE history (id INTEGER PRIMARY KEY, source_id INTEGER,
			display TEXT, timestamp INTEGER, project TEXT, source_path TEXT);`); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := v1.Exec(`INSERT INTO history (display, timestamp, source_path)
			VALUES (?, ?, ?)`, e.display, e.ts, sourcePath); err != nil {
			t.Fatal(err)
		}
	}
}

// importReport reads back the report the bootstrap import recorded.
func importReport(t *testing.T, eng *engine) migrate.Report {
	t.Helper()
	raw, ok, err := eng.store.GetMeta(context.Background(), "v1_import_report")
	if err != nil || !ok {
		t.Fatalf("v1_import_report meta missing (ok=%v err=%v)", ok, err)
	}
	var rep migrate.Report
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decoding v1_import_report %q: %v", raw, err)
	}
	return rep
}

// Retained history entries — prompts v1 kept after the live history.jsonl
// stopped listing them — are the whole point of importing history, and on
// the ordinary same-machine upgrade v1 recorded them under the path that
// IS that live file. Importing them under that path fed them straight to
// the next parse of it, which deletes a source's rows before re-inserting
// the file's current contents; with v1_import_state=success they never
// came back.
func TestV1RetainedHistorySurvivesLiveIngest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "ccpeek.db")
	claudeRoot := t.TempDir()
	livePath := filepath.Join(claudeRoot, "history.jsonl")

	live := v1HistoryRow{"still in the file", 1751364000000}
	retained := v1HistoryRow{"rotated out of the file", 1740000000000}
	writeHistoryFile(t, claudeRoot, live)
	// v1 read both from the live file and kept the one the file dropped.
	seedV1HistoryDB(t, dataFile, livePath, live, retained)

	cmd := pinRoots(t, dataFile, claudeRoot)
	count := func(eng *engine, display string) int {
		t.Helper()
		var n int
		if err := eng.store.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM history WHERE display = ?`, display).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	eng, err := openEngine(ctx, cmd, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// Ingest ran first, so the entry the live file still holds was already
	// in v2 and the import skipped it: only the retained one is new.
	if rep := importReport(t, eng); rep.HistoryEntries != 1 {
		t.Errorf("imported history entries = %d, want 1 (the live entry is ingest's)", rep.HistoryEntries)
	}
	if n := count(eng, retained.display); n != 1 {
		t.Fatalf("retained entry after first run = %d, want 1", n)
	}
	if n := count(eng, live.display); n != 1 {
		t.Errorf("live entry after first run = %d, want 1", n)
	}
	eng.Close()

	// The live file changes, so the next pass re-parses it — clearing that
	// source's rows before re-inserting. The retained entry is not that
	// source's and must survive.
	appended := v1HistoryRow{"typed after the upgrade", 1751450400000}
	writeHistoryFile(t, claudeRoot, live, appended)

	eng2, err := openEngine(ctx, cmd, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if n := count(eng2, retained.display); n != 1 {
		t.Errorf("retained entry after the second pass = %d, want 1 (the re-parse deleted the imported rescue)", n)
	}
	if n := count(eng2, live.display); n != 1 {
		t.Errorf("live entry after the second pass = %d, want 1 (duplicated)", n)
	}
	if n := count(eng2, appended.display); n != 1 {
		t.Errorf("appended entry = %d, want 1", n)
	}
}

// TestFirstRunIndexesBeforeImportingV1 pins the bootstrap order. Every
// importer skip test asks whether v2 ALREADY HOLDS a row, and against an
// empty first-run store they all answer no — so importing before the
// ingest pass turns the entire v1 database into "orphans", doubling the
// first run's work and letting v1's lossier copies of live sessions in.
func TestFirstRunIndexesBeforeImportingV1(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "ccpeek.db")
	claudeRoot := t.TempDir()

	const liveID = "11111111-aaaa-bbbb-cccc-111111111111"
	livePath := writeClaudeSession(t, claudeRoot, liveID, "index me from disk")

	v1, err := sql.Open("sqlite", "file:"+dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v1.Exec(v1Skeleton); err != nil {
		t.Fatal(err)
	}
	// One session the pipeline indexes from disk, one whose source is gone.
	if _, err := v1.Exec(`INSERT INTO sessions
		(id, session_id, first_prompt, created_at, modified_at, project_path, source_path)
		VALUES (1, ?, 'v1 copy', '2026-06-01T10:00:00Z', '2026-06-01T11:00:00Z', '/home/u/proj', ?)`,
		liveID, livePath); err != nil {
		t.Fatal(err)
	}
	if _, err := v1.Exec(`INSERT INTO sessions
		(id, session_id, first_prompt, created_at, modified_at, project_path, source_path)
		VALUES (2, 'ghost-session', 'lost to time', '2026-05-01T10:00:00Z', '2026-05-01T12:00:00Z', '/home/u/proj', '/gone/ghost.jsonl')`); err != nil {
		t.Fatal(err)
	}
	if _, err := v1.Exec(`INSERT INTO messages (session_id, seq, type, role, timestamp, uuid, content)
		VALUES (1, 0, 'user', 'user', '2026-06-01T10:00:00Z', 'v1-1', '{"role":"user","content":"v1 copy"}')`); err != nil {
		t.Fatal(err)
	}
	v1.Close()

	cmd := pinRoots(t, dataFile, claudeRoot)
	eng, err := openEngine(ctx, cmd, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if rep := importReport(t, eng); rep.OrphanSessions != 1 || rep.OrphanMessages != 0 {
		t.Errorf("import report = %d orphan sessions / %d messages, want 1/0: the indexed session is not an orphan",
			rep.OrphanSessions, rep.OrphanMessages)
	}
	var origin string
	if err := eng.store.DB().QueryRowContext(ctx,
		`SELECT origin FROM sessions WHERE external_id = ?`, liveID).Scan(&origin); err != nil {
		t.Fatal(err)
	}
	if origin != "ingest" {
		t.Errorf("indexed session origin = %q, want ingest", origin)
	}
	// The rescued session's workspace membership is rebuilt after the
	// import, which lands past the pass that regenerates the facet.
	var n int
	if err := eng.store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session_workspaces sw
		JOIN sessions s ON s.id = sw.session_id
		WHERE s.external_id = 'ghost-session'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("imported session's workspace links = %d, want 1", n)
	}
}

// The v1 import retries on every engine open, --skip-index included. It
// used to hang off the bootstrap closure, which is nil when indexing is
// skipped — so the documented "retried on every start until it succeeds"
// contract silently did not hold for anyone who runs --skip-index, and
// the UI banner stayed red forever.
func TestV1ImportRetriesUnderSkipIndex(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "ccpeek.db")
	liveSource := filepath.Join(t.TempDir(), "live.jsonl")

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

	// First open: an unreadable legacy file, so the import fails.
	if err := os.WriteFile(dataFile, []byte("this is not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng, err := openEngine(ctx, cmd, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := eng.store.GetMeta(ctx, "v1_import_state")
	if err != nil {
		t.Fatal(err)
	}
	if state != v1ImportFailed {
		t.Fatalf("precondition: v1_import_state = %q, want failed", state)
	}
	eng.Close()

	// Replace it with a readable one and reopen with --skip-index. The
	// import must be attempted anyway.
	if err := os.Remove(dataFile); err != nil {
		t.Fatal(err)
	}
	seedV1DB(t, dataFile, liveSource)

	eng, err = openEngine(ctx, cmd, true /* skipIndex */, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	state, _, err = eng.store.GetMeta(ctx, "v1_import_state")
	if err != nil {
		t.Fatal(err)
	}
	if state != v1ImportSuccess {
		t.Errorf("v1_import_state = %q after a --skip-index open, want success", state)
	}
	if v, _, _ := eng.store.GetMeta(ctx, "v1_import_error"); v != "" {
		t.Errorf("v1_import_error = %q, want it cleared after success", v)
	}
	if _, ok, _ := eng.store.GetMeta(ctx, "v1_imported_at"); !ok {
		t.Error("v1_imported_at not stamped")
	}
}
