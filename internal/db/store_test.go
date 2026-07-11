package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v2.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestOpenFreshCreatesLatestSchema(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != schemaVersion {
		t.Fatalf("fresh db at v%d, want v%d", v, schemaVersion)
	}

	for _, table := range []string{
		"agents", "sessions", "session_relations", "pending_relations",
		"messages", "message_usage", "tool_calls", "artifacts",
		"artifact_sessions", "workspaces", "session_workspaces", "history",
		"source_files", "ingest_runs", "ingest_issues", "rollup_usage_daily",
		"scan_findings", "pricing", "user_annotations", "search_docs", "search_fts",
	} {
		var name string
		err := s.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (slug) VALUES ('claude-code')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s.Close()

	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	var n int
	if err := s2.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agents`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("agents after reopen = %d (err %v), want 1", n, err)
	}
}

// TestMigrateV1AddsStatSig proves a schema-v1 database (the v2 preview
// builds) upgrades in place: stat_sig appears with the never-matching
// empty default so every source re-hashes exactly once.
func TestMigrateV1AddsStatSig(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()

	// Downgrade the fresh database to a v1 shape: rebuild the tables later
	// migrations touch without their added columns, and stamp version 1.
	stmts := []string{
		`DROP TABLE source_files`,
		`CREATE TABLE source_files (
			path TEXT PRIMARY KEY,
			agent_id INTEGER NOT NULL REFERENCES agents(id),
			content_hash TEXT NOT NULL,
			indexed_at TEXT NOT NULL
		)`,
		`DROP TABLE rollup_usage_daily`,
		`CREATE TABLE rollup_usage_daily (
			day TEXT NOT NULL,
			agent_id INTEGER NOT NULL,
			workspace_id INTEGER NOT NULL DEFAULT 0,
			model TEXT NOT NULL DEFAULT '',
			sessions INTEGER NOT NULL DEFAULT 0,
			messages INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			cache_write_tokens INTEGER NOT NULL DEFAULT 0,
			cost_usd REAL NOT NULL DEFAULT 0,
			priced INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (day, agent_id, workspace_id, model)
		)`,
		`INSERT INTO agents (id, slug) VALUES (1, 'claude-code')`,
		`INSERT INTO source_files (path, agent_id, content_hash, indexed_at)
		 VALUES ('/x/s.jsonl', 1, 'abc', '2026-01-01T00:00:00Z')`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("shaping v1 db: %v", err)
		}
	}
	if err := s.writeVersion(ctx, 1); err != nil {
		t.Fatalf("writeVersion: %v", err)
	}
	s.Close()

	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen with migration: %v", err)
	}
	defer s2.Close()

	if v, err := s2.SchemaVersion(ctx); err != nil || v != schemaVersion {
		t.Fatalf("version after migration = %d (err %v), want %d", v, err, schemaVersion)
	}
	sigs, err := s2.SourceSigs(ctx)
	if err != nil {
		t.Fatalf("SourceSigs: %v", err)
	}
	got, ok := sigs["/x/s.jsonl"]
	if !ok {
		t.Fatal("migrated row missing")
	}
	if got.ContentHash != "abc" || got.StatSig != "" {
		t.Fatalf("migrated row = %+v, want hash abc + empty stat", got)
	}
}

// TestReadsDontQueueBehindWrites is the serve-first responsiveness
// contract: with the writer connection held by an open transaction (a
// watch-mode ingest, a rollup regen, the secret scan's write phase), the
// read pool must still answer immediately.
func TestReadsDontQueueBehindWrites(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (slug) VALUES ('claude-code')`); err != nil {
		t.Fatal(err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agents (slug) VALUES ('pi')`); err != nil {
		t.Fatal(err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var n int
	if err := s.ReadDB().QueryRowContext(readCtx,
		`SELECT COUNT(*) FROM agents`).Scan(&n); err != nil {
		t.Fatalf("read blocked behind open write tx: %v", err)
	}
	// WAL snapshot isolation: the uncommitted insert is invisible.
	if n != 1 {
		t.Fatalf("read saw %d agents, want 1 (committed state only)", n)
	}
}

func TestOpenFutureSchemaFails(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	if err := s.writeVersion(ctx, schemaVersion+10); err != nil {
		t.Fatalf("writeVersion: %v", err)
	}
	s.Close()

	_, err := Open(ctx, path)
	if !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("err = %v, want ErrFutureSchema", err)
	}
}

func TestCascadeDeleteSessionOwnsChildren(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO agents (id, slug) VALUES (1, 'claude-code')`)
	mustExec(`INSERT INTO sessions (id, agent_id, external_id) VALUES (10, 1, 'sess-a')`)
	mustExec(`INSERT INTO messages (id, session_id, seq, role) VALUES (100, 10, 0, 'assistant')`)
	mustExec(`INSERT INTO message_usage (message_id, input_tokens, output_tokens) VALUES (100, 5, 7)`)
	mustExec(`INSERT INTO tool_calls (session_id, seq, name) VALUES (10, 0, 'Bash')`)

	mustExec(`DELETE FROM sessions WHERE id = 10`)

	for _, q := range []string{
		`SELECT COUNT(*) FROM messages`,
		`SELECT COUNT(*) FROM message_usage`,
		`SELECT COUNT(*) FROM tool_calls`,
	} {
		var n int
		if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 0 {
			t.Errorf("%s = %d after session delete, want 0", q, n)
		}
	}
}

func TestResetDerivedPreservesUserState(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, slug) VALUES (1, 'claude-code')`); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (agent_id, external_id) VALUES (1, 'sess-a')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
		 VALUES ('scan_finding', 'claude-code/sess-a/rule-x', 'scan_ignore', '{}', '2026-07-10T00:00:00Z')`); err != nil {
		t.Fatalf("seed annotation: %v", err)
	}

	if err := s.ResetDerived(ctx); err != nil {
		t.Fatalf("ResetDerived: %v", err)
	}

	var sessions, annotations int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_annotations`).Scan(&annotations); err != nil {
		t.Fatalf("count annotations: %v", err)
	}
	if sessions != 0 {
		t.Errorf("sessions = %d after reset, want 0", sessions)
	}
	if annotations != 1 {
		t.Errorf("user_annotations = %d after reset, want 1 (must survive rebuild)", annotations)
	}

	// The store must be fully usable after a reset.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, slug) VALUES (1, 'claude-code')`); err != nil {
		t.Fatalf("reinsert after reset: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO search_docs (doc_type, seq, title, text_content) VALUES ('m', 0, 't', 'hello world')`); err != nil {
		t.Fatalf("search doc insert after reset: %v", err)
	}
}

func TestFTSRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO search_docs (doc_type, seq, title, text_content)
		 VALUES ('message', 3, 'greeting', 'hello session world')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var docType, snip string
	var seq int
	err := s.db.QueryRowContext(ctx, `
		SELECT d.doc_type, d.seq, snippet(search_fts, 0, '[', ']', '…', 5)
		FROM search_fts
		JOIN search_docs d ON d.id = search_fts.rowid
		WHERE search_fts MATCH 'session' ORDER BY rank LIMIT 1`).
		Scan(&docType, &seq, &snip)
	if err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if docType != "message" || seq != 3 {
		t.Errorf("locator = %s/%d", docType, seq)
	}
	if snip == "" {
		t.Error("empty snippet")
	}

	// Trigger-maintained consistency: deleting the doc removes the hit.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM search_docs`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'session'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("fts hits after delete = %d, want 0", n)
	}
}
