package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
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
	exec(`INSERT INTO messages (session_id, seq, type, role, timestamp, uuid, content)
	      VALUES (2, 0, 'user', 'user', '2026-05-01T10:00:00Z', 'g-1', '{"role":"user","content":"remember the auth fix?"}')`)
	exec(`INSERT INTO messages (session_id, seq, type, role, timestamp, uuid, content)
	      VALUES (2, 1, 'assistant', 'assistant', '2026-05-01T10:01:00Z', 'g-2', '{"role":"assistant","content":[{"type":"text","text":"yes, we patched the token check"}]}')`)
	// Orphan plan.
	exec(`INSERT INTO plans (file_name, title, content, source_path)
	      VALUES ('old-plan.md', 'Old plan', '# Old plan\ndo things', '/gone/plans/old-plan.md')`)
	// Ignored finding (and one not ignored, which must not import).
	exec(`INSERT INTO scan_findings (rule_id, source_type, source_id, line_number, ignored)
	      VALUES ('aws-key', 'message', 'ghost-session', 42, 1)`)
	exec(`INSERT INTO scan_findings (rule_id, source_type, source_id, line_number, ignored)
	      VALUES ('gh-token', 'message', 'live-session', 7, 0)`)
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

	report, err := ImportV1(ctx, store, v1Path)
	if err != nil {
		t.Fatalf("ImportV1: %v", err)
	}
	if report.OrphanSessions != 1 || report.OrphanMessages != 2 ||
		report.OrphanArtifacts != 1 || report.IgnoreFlags != 1 {
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

	// Ghost session imported with origin tag; live one skipped.
	if n := q(`SELECT COUNT(*) FROM sessions WHERE origin = 'imported-v1'`); n != 1 {
		t.Errorf("imported sessions = %d, want 1", n)
	}
	if n := q(`SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Errorf("total sessions = %d, want 1 (live session must be skipped)", n)
	}
	if n := q(`SELECT COUNT(*) FROM messages`); n != 2 {
		t.Errorf("messages = %d, want 2", n)
	}

	// Imported content is searchable.
	if n := q(`SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'patched'`); n != 1 {
		t.Errorf("search hits for imported message = %d, want 1", n)
	}

	// Ignore flag on natural key.
	if n := q(`SELECT COUNT(*) FROM user_annotations
	           WHERE kind = 'scan_ignore' AND natural_key = 'message/ghost-session/aws-key/42'`); n != 1 {
		t.Errorf("ignore annotations = %d, want 1", n)
	}

	// Idempotent: running again must not duplicate.
	report2, err := ImportV1(ctx, store, v1Path)
	if err != nil {
		t.Fatal(err)
	}
	if n := q(`SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Errorf("sessions after re-import = %d, want 1", n)
	}
	if n := q(`SELECT COUNT(*) FROM messages`); n != 2 {
		t.Errorf("messages after re-import = %d, want 2 (children cleared, not duplicated)", n)
	}
	if n := q(`SELECT COUNT(*) FROM user_annotations`); n != 1 {
		t.Errorf("annotations after re-import = %d, want 1", n)
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
