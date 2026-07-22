package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/secrets"
	_ "modernc.org/sqlite"
)

// buildV1DB creates a minimal v1-schema database with: one session whose
// source file still exists (must be skipped), one orphaned session with
// messages (must be imported), one orphaned plan, and one ignored scan
// finding.
func buildV1DB(t *testing.T, liveSourcePath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ccpeek.db")
	v1, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer v1.Close()

	schema := `
	CREATE TABLE projects (id INTEGER PRIMARY KEY, dir_name TEXT, display_name TEXT, canonical_path TEXT);
	CREATE TABLE sessions (
		id INTEGER PRIMARY KEY, session_id TEXT, project_id INTEGER,
		first_prompt TEXT, message_count INTEGER, created_at TEXT,
		modified_at TEXT, git_branch TEXT, project_path TEXT,
		source_path TEXT);
	CREATE TABLE messages (
		id INTEGER PRIMARY KEY, session_id INTEGER, seq INTEGER, type TEXT,
		role TEXT, timestamp TEXT, uuid TEXT, content TEXT, cwd TEXT,
		git_branch TEXT);
	CREATE TABLE plans (id INTEGER PRIMARY KEY, file_name TEXT, title TEXT,
		size_bytes INTEGER, content TEXT, source_path TEXT);
	CREATE TABLE tool_calls (id INTEGER PRIMARY KEY, session_id INTEGER,
		seq INTEGER, timestamp TEXT, tool_name TEXT, tool_kind TEXT,
		input_json TEXT, result_text TEXT, file_path TEXT, searchable_text TEXT);
	CREATE TABLE history (id INTEGER PRIMARY KEY, source_id INTEGER,
		display TEXT, timestamp INTEGER, project TEXT, source_path TEXT);
	CREATE TABLE todos (id INTEGER PRIMARY KEY, file_name TEXT,
		session_id INTEGER, source_path TEXT);
	CREATE TABLE todo_items (id INTEGER PRIMARY KEY, todo_id INTEGER,
		seq INTEGER, content TEXT, status TEXT, active_form TEXT);
	CREATE TABLE scan_findings (id INTEGER PRIMARY KEY, rule_id TEXT,
		description TEXT, source_type TEXT, source_id TEXT,
		match_redacted TEXT, line_number INTEGER, scanned_at TEXT,
		ignored INTEGER DEFAULT 0);
	`
	if _, err := v1.Exec(schema); err != nil {
		t.Fatal(err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := v1.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO projects (id, dir_name, canonical_path) VALUES (1, '-home-u-proj', '/home/u/proj')`)
	// Live session: source still exists → skip.
	exec(`INSERT INTO sessions (id, session_id, project_id, first_prompt, created_at, modified_at, source_path)
	      VALUES (1, 'live-session', 1, 'still here', '2026-06-01T10:00:00Z', '2026-06-01T11:00:00Z', ?)`,
		liveSourcePath)
	// Orphan session: source deleted → import.
	exec(`INSERT INTO sessions (id, session_id, project_id, first_prompt, created_at, modified_at, git_branch, project_path, source_path)
	      VALUES (2, 'ghost-session', 1, 'lost to time', '2026-05-01T10:00:00Z', '2026-05-01T12:00:00Z', 'main', '/home/u/proj', '/gone/ghost.jsonl')`)
	// The ghost session's first message carries a detectable secret so the
	// test can prove the imported ignore re-attaches to a fresh v2 scan.
	slackToken := "xoxb-3336494366" + "76-7992618528" + "69-clFJVVIaoJahpORboa3Ba2al"
	exec(`INSERT INTO messages (session_id, seq, type, role, timestamp, uuid, content)
	      VALUES (2, 0, 'user', 'user', '2026-05-01T10:00:00Z', 'g-1', ?)`,
		`{"role":"user","content":"remember the auth fix? token `+slackToken+`"}`)
	exec(`INSERT INTO messages (session_id, seq, type, role, timestamp, uuid, content)
	      VALUES (2, 1, 'assistant', 'assistant', '2026-05-01T10:01:00Z', 'g-2', '{"role":"assistant","content":[{"type":"text","text":"yes, we patched the token check"}]}')`)
	// Orphan plan.
	exec(`INSERT INTO plans (file_name, title, content, source_path)
	      VALUES ('old-plan.md', 'Old plan', '# Old plan\ndo things', '/gone/plans/old-plan.md')`)
	// Retained rows the ghost session can't re-derive: a tool call
	// (v1 "task" kind maps to v2 "subagent") and a history entry.
	exec(`INSERT INTO tool_calls (session_id, seq, timestamp, tool_name, tool_kind, input_json, result_text, file_path)
	      VALUES (2, 0, '2026-05-01T10:00:30Z', 'Task', 'task', '{"prompt":"audit"}', 'done', '')`)
	exec(`INSERT INTO history (display, timestamp, source_path)
	      VALUES ('remember the auth fix?', 1746093600000, '/gone/history.jsonl')`)
	// A retained todo list with items (structured metadata + parked link).
	exec(`INSERT INTO todos (id, file_name, source_path)
	      VALUES (1, '99999999-9999-4999-8999-999999999999-agent-x.json', '/gone/todos/x.json')`)
	exec(`INSERT INTO todo_items (todo_id, seq, content, status, active_form)
	      VALUES (1, 0, 'ship the importer', 'in_progress', 'shipping the importer')`)
	// Ignored findings in the real v1 identity shapes: messages are
	// "<session>@<timestamp>" with detector line numbers, file history is
	// the bare session/dir id. One non-ignored row must not import.
	exec(`INSERT INTO scan_findings (rule_id, source_type, source_id, line_number, ignored)
	      VALUES ('slack-bot-token', 'message', 'ghost-session@2026-05-01T10:00:00.224Z', 5, 1)`)
	exec(`INSERT INTO scan_findings (rule_id, source_type, source_id, line_number, ignored)
	      VALUES ('aws-access-token', 'file_history', 'ghost-session', 116, 1)`)
	exec(`INSERT INTO scan_findings (rule_id, source_type, source_id, line_number, ignored)
	      VALUES ('gh-token', 'message', 'live-session@2026-06-01T10:00:00Z', 7, 0)`)
	return path
}

func TestImportV1(t *testing.T) {
	ctx := context.Background()

	// A "still on disk" source file.
	live := filepath.Join(t.TempDir(), "live.jsonl")
	if err := os.WriteFile(live, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v1Path := buildV1DB(t, live)

	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// The pipeline ingested the live session before the import runs (the
	// real first-run order); import must skip exactly what v2 already
	// holds — not whatever happens to exist on disk.
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "live-session",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	report, err := ImportV1(ctx, store, v1Path)
	if err != nil {
		t.Fatalf("ImportV1: %v", err)
	}
	if report.OrphanSessions != 1 || report.OrphanMessages != 2 ||
		report.OrphanToolCalls != 1 || report.OrphanArtifacts != 2 ||
		report.HistoryEntries != 1 || report.IgnoreFlags != 2 {
		t.Fatalf("report = %+v", report)
	}

	q := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := store.DB().QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}

	// Ghost session imported with origin tag; the v2-held one skipped.
	if n := q(`SELECT COUNT(*) FROM sessions WHERE origin = 'imported-v1'`); n != 1 {
		t.Errorf("imported sessions = %d, want 1", n)
	}
	if n := q(`SELECT COUNT(*) FROM sessions`); n != 2 {
		t.Errorf("total sessions = %d, want 2 (ingested + imported)", n)
	}
	if n := q(`SELECT COUNT(*) FROM messages`); n != 2 {
		t.Errorf("messages = %d, want 2", n)
	}
	// The tool call imported with its kind translated.
	if n := q(`SELECT COUNT(*) FROM tool_calls WHERE kind = 'subagent' AND name = 'Task'`); n != 1 {
		t.Errorf("imported tool calls = %d, want 1 subagent", n)
	}
	// The retained history entry landed with provenance.
	if n := q(`SELECT COUNT(*) FROM history WHERE source_path = '/gone/history.jsonl'`); n != 1 {
		t.Errorf("imported history = %d, want 1", n)
	}
	// The todo list rebuilt with structured metadata and a parked link
	// (its filename uuid names a session v2 does not hold).
	if n := q(`SELECT COUNT(*) FROM artifacts WHERE kind = 'todo_list'`); n != 1 {
		t.Errorf("todo artifacts = %d, want 1", n)
	}
	if n := q(`SELECT COUNT(*) FROM artifacts WHERE kind = 'todo_list' AND metadata_json LIKE '%in_progress%'`); n != 1 {
		t.Error("todo metadata lost its items")
	}
	if n := q(`SELECT COUNT(*) FROM pending_artifact_links`); n != 1 {
		t.Errorf("pending links = %d, want 1 (todo's session uuid unknown)", n)
	}

	// Imported content is searchable.
	if n := q(`SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'patched'`); n != 1 {
		t.Errorf("search hits for imported message = %d, want 1", n)
	}

	// The message ignore translated to the v2 key: the v1 timestamp
	// resolved to the imported message's seq (0), agent-qualified.
	if n := q(`SELECT COUNT(*) FROM user_annotations
	           WHERE kind = 'scan_ignore'
	             AND natural_key = 'message/claude-code/ghost-session/slack-bot-token/0'`); n != 1 {
		t.Errorf("translated message ignore = %d, want 1", n)
	}
	// The file-history ignore became a rule-scoped wildcard (v1 line
	// numbering has no v2 equivalent).
	if n := q(`SELECT COUNT(*) FROM user_annotations
	           WHERE kind = 'scan_ignore'
	             AND natural_key = 'artifact/claude-code/file_history/ghost-session/aws-access-token/*'`); n != 1 {
		t.Errorf("wildcard file-history ignore = %d, want 1", n)
	}

	// The guarantee that matters: a fresh v2 scan detects the imported
	// message's secret AND resolves it as ignored.
	sc, err := secrets.New(store)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, err := sc.Run(ctx)
	if err != nil {
		t.Fatalf("scan after import: %v", err)
	}
	reattached := false
	for _, f := range findings {
		if f.NaturalKey == "message/claude-code/ghost-session" && f.RuleID == "slack-bot-token" {
			reattached = true
			if !f.Ignored {
				t.Errorf("imported ignore did not re-attach: %+v", f)
			}
		}
	}
	if !reattached {
		t.Fatal("scan found no finding for the imported message's secret")
	}

	// Idempotent: running again must not duplicate.
	report2, err := ImportV1(ctx, store, v1Path)
	if err != nil {
		t.Fatal(err)
	}
	if n := q(`SELECT COUNT(*) FROM sessions`); n != 2 {
		t.Errorf("sessions after re-import = %d, want 2", n)
	}
	if n := q(`SELECT COUNT(*) FROM history`); n != 1 {
		t.Errorf("history after re-import = %d, want 1 (no duplicates)", n)
	}
	if n := q(`SELECT COUNT(*) FROM messages`); n != 2 {
		t.Errorf("messages after re-import = %d, want 2 (children cleared, not duplicated)", n)
	}
	if n := q(`SELECT COUNT(*) FROM user_annotations`); n != 2 {
		t.Errorf("annotations after re-import = %d, want 2 (no duplicates)", n)
	}
	_ = report2
}

func TestImportV1MissingDB(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := ImportV1(ctx, store, filepath.Join(t.TempDir(), "nope.db")); err == nil {
		t.Fatal("missing v1 db must error, caller decides whether that's fatal")
	}
}
