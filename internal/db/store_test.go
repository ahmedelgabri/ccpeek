package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
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
		"scan_findings", "pricing", "user_annotations", "search_fts",
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
		`INSERT INTO search_fts (doc_type, title, url, text_content) VALUES ('m', 't', '/x', 'hello world')`); err != nil {
		t.Fatalf("fts insert after reset: %v", err)
	}
}

func TestFTSRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO search_fts (doc_type, title, url, text_content)
		 VALUES ('message', 'greeting', '/sessions/x/', 'hello session world')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var url, snip string
	err := s.db.QueryRowContext(ctx,
		`SELECT url, snippet(search_fts, 3, '[', ']', '…', 5)
		 FROM search_fts WHERE search_fts MATCH 'session' ORDER BY rank LIMIT 1`).
		Scan(&url, &snip)
	if err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if url != "/sessions/x/" {
		t.Errorf("url = %q", url)
	}
	if snip == "" {
		t.Error("empty snippet")
	}
}
