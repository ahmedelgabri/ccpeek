package scan

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func TestRunScansAdditionalSourceTypes(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}

	projectID, err := db.InsertProject(ctx, tx, "test-project", "test-project")
	if err != nil {
		t.Fatal(err)
	}
	sess := model.SessionEntry{
		SessionID:    "scan-session",
		FirstPrompt:  "test",
		MessageCount: 1,
		Created:      "2025-01-01T00:00:00Z",
		Modified:     "2025-01-01T00:00:00Z",
	}
	sessionDBID, err := db.InsertSession(ctx, tx, projectID, sess, "/src/scan-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	blocks := []model.ContentBlock{
		{Type: "tool_result", Content: json.RawMessage(`"AKIA2OGYBAH6QLHAMZXB"`)},
		{Type: "tool_use", Name: "Write", Input: json.RawMessage(`{"file_path":"/tmp/test.txt","content":"xoxb-123456789012-1234567890123-AbCdEfGhIjKlMnOpQrStUvWx"}`)},
	}
	content, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	messages := []model.ConversationMessage{{
		Type:      "assistant",
		Timestamp: "2025-01-01T00:00:00Z",
		Message: model.MessagePayload{
			Role:    "assistant",
			Content: content,
		},
	}}
	if err := db.InsertMessages(ctx, tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}

	res, err := tx.ExecContext(ctx, `INSERT INTO todos (file_name, item_count, statuses, source_path) VALUES (?, ?, ?, ?)`, "todo.json", 1, "{}", "/src/todo.json")
	if err != nil {
		t.Fatal(err)
	}
	todoID, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO todo_items (todo_id, seq, content, status, active_form) VALUES (?, ?, ?, ?, ?)`, todoID, 0, "AKIA2OGYBAH6QLHAMZXB", "pending", ""); err != nil {
		t.Fatal(err)
	}

	res, err = tx.ExecContext(ctx, `INSERT INTO task_groups (dir_name, item_count, statuses, source_path) VALUES (?, ?, ?, ?)`, "task-dir", 1, "{}", "/src/task-dir")
	if err != nil {
		t.Fatal(err)
	}
	taskGroupID, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_items (task_group_id, seq, item_id, subject, description, active_form, status, blocks, blocked_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, taskGroupID, 0, "1", "subject", "AKIA2OGYBAH6QLHAMZXB", "", "pending", "[]", "[]"); err != nil {
		t.Fatal(err)
	}

	res, err = tx.ExecContext(ctx, `INSERT INTO file_history (conversation_id, file_count, source_path) VALUES (?, ?, ?)`, "conv-1", 1, "/src/file-history/conv-1")
	if err != nil {
		t.Fatal(err)
	}
	fhID, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO file_versions (file_history_id, hash, version, content) VALUES (?, ?, ?, ?)`, fhID, "hash1", 1, "AKIA2OGYBAH6QLHAMZXB"); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_facets (session_id_text, underlying_goal, outcome, helpfulness, session_type, primary_success, brief_summary, friction_detail, goal_categories, satisfaction, friction_counts, source_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "usage-session", "goal", "outcome", "helpful", "type", "success", "AKIA2OGYBAH6QLHAMZXB", "", "{}", "{}", "{}", "/src/usage.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_report (content, source_path) VALUES (?, ?)`, "AKIA2OGYBAH6QLHAMZXB", "/src/report.html"); err != nil {
		t.Fatal(err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	scanner, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := scanner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}

	found := map[string]bool{}
	for _, f := range findings {
		found[f.SourceType] = true
	}
	for _, want := range []string{"message", "todo", "task", "file_history", "usage_facet", "usage_report"} {
		if !found[want] {
			t.Errorf("expected finding for source type %q", want)
		}
	}
}

func TestRunFailedRescanPreservesExistingFindings(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := db.InsertProject(context.Background(), tx, "test-proj", "Test")
	sess := model.SessionEntry{SessionID: "s1", FirstPrompt: "test", MessageCount: 1, Created: "2025-01-01T00:00:00Z", Modified: "2025-01-01T00:00:00Z"}
	sessionDBID, _ := db.InsertSession(context.Background(), tx, projectID, sess, "")
	messages := []model.ConversationMessage{{
		Type: "human", Timestamp: "2025-01-01T00:00:00Z",
		Message: model.MessagePayload{Role: "user", Content: []byte(`"AKIA2OGYBAH6QLHAMZXB"`)},
	}}
	if err := db.InsertMessages(context.Background(), tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	scanner, _ := New(db)
	if _, err := scanner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := db.ScanFindingCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanner.Run(ctx); err == nil {
		t.Fatal("expected canceled rescan to fail")
	}

	after, err := db.ScanFindingCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("expected findings to be preserved after failed rescan, got %d before and %d after", before, after)
	}
}
