package store

import (
	"context"
	"testing"
)

func TestResetDropsAllTables(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert data into tables that depend on sessions (commands, messages)
	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('p', 'P')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions (session_id, project_id, source_path) VALUES ('s1', 1, '/src')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO commands (session_id, seq, command, timestamp) VALUES (1, 0, 'echo hi', '2025-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages (session_id, seq, type, content) VALUES (1, 0, 'user', '"hello"')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Reset should succeed (commands table dropped before sessions)
	if err := s.Reset(ctx); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify tables are recreated and empty
	var count int
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM sessions`); err != nil {
		t.Fatal("sessions table missing after Reset:", err)
	}
	if count != 0 {
		t.Errorf("expected 0 sessions after Reset, got %d", count)
	}
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM commands`); err != nil {
		t.Fatal("commands table missing after Reset:", err)
	}
}

func TestMigrateRecoversMissingTables(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Simulate corruption: drop a table but leave schema version intact
	if _, err := s.db.ExecContext(ctx, `DROP TABLE messages`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE messages_fts`); err != nil {
		t.Fatal(err)
	}

	// Re-running migrate should recover the missing tables
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("migrate failed to recover: %v", err)
	}

	// Verify the table was recreated
	var count int
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM messages`); err != nil {
		t.Fatal("messages table not recovered:", err)
	}
}

func TestDeleteSessionCascade(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Set up: project, session, messages, commands, todo linked to session
	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('p', 'P')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions (session_id, project_id, source_path) VALUES ('s1', 1, '/src/s1.jsonl')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages (session_id, seq, type, content) VALUES (1, 0, 'user', '"hello"')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages_fts (rowid, text_content) VALUES (1, 'hello')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO commands (session_id, seq, command, timestamp) VALUES (1, 0, 'ls', '2025-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO todos (file_name, session_id, item_count, statuses, source_path) VALUES ('t.json', 1, 1, '{}', '/src/t.json')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Delete the session cascade
	tx2, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSessionCascade(ctx, tx2, "/src/s1.jsonl"); err != nil {
		t.Fatalf("DeleteSessionCascade failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	var count int
	// Session should be gone
	s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM sessions`)
	if count != 0 {
		t.Errorf("expected 0 sessions, got %d", count)
	}
	// Messages should be gone
	s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM messages`)
	if count != 0 {
		t.Errorf("expected 0 messages, got %d", count)
	}
	// Commands should be gone
	s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM commands`)
	if count != 0 {
		t.Errorf("expected 0 commands, got %d", count)
	}
	// Todo should be unlinked (session_id = NULL) but still exist
	var sessID *int64
	s.db.Get(&sessID, `SELECT session_id FROM todos WHERE file_name = 't.json'`)
	if sessID != nil {
		t.Errorf("todo should have NULL session_id after cascade, got %v", *sessID)
	}
}

func TestGetScanFinding(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert a finding
	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO scan_findings (rule_id, description, source_type, source_id, match_redacted, scanned_at)
		 VALUES ('test-rule', 'Test finding', 'plan', 'test.md', 's3cr****', '2025-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	f, err := s.GetScanFinding(ctx, 1)
	if err != nil {
		t.Fatal("GetScanFinding failed:", err)
	}
	if f.RuleID != "test-rule" {
		t.Errorf("expected rule_id 'test-rule', got %q", f.RuleID)
	}
	if f.SourceType != "plan" {
		t.Errorf("expected source_type 'plan', got %q", f.SourceType)
	}

	// Non-existent ID should return error
	_, err = s.GetScanFinding(ctx, 999)
	if err == nil {
		t.Error("expected error for non-existent finding")
	}
}

func TestListProjectNames(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('b-proj', 'Beta')`)
	_, _ = tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('a-proj', 'Alpha')`)
	tx.Commit()

	names, err := s.ListProjectNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(names))
	}
	// Should be sorted by display_name
	if names[0].DisplayName != "Alpha" {
		t.Errorf("expected first project 'Alpha', got %q", names[0].DisplayName)
	}
}
