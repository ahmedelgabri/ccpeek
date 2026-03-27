package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

func TestBackfillToolCallsFromMessages(t *testing.T) {
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
	projectID, err := s.InsertProject(ctx, tx, "proj", "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionDBID, err := s.InsertSession(ctx, tx, projectID, model.SessionEntry{SessionID: "sess-1", FirstPrompt: "hi"}, "/src/sess-1.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	toolUseContent, err := json.Marshal([]model.ContentBlock{{
		Type:  "tool_use",
		ID:    "tu1",
		Name:  "Bash",
		Input: json.RawMessage(`{"command":"echo hi"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	toolResultContent, err := json.Marshal([]model.ContentBlock{{
		Type:      "tool_result",
		ToolUseID: "tu1",
		Content:   json.RawMessage(`"ok"`),
	}})
	if err != nil {
		t.Fatal(err)
	}

	messages := []model.ConversationMessage{
		{
			Type:      "assistant",
			Timestamp: "2025-01-01T00:00:00Z",
			Message: model.MessagePayload{
				Role:    "assistant",
				Content: toolUseContent,
			},
		},
		{
			Type:      "user",
			Timestamp: "2025-01-01T00:00:01Z",
			Message: model.MessagePayload{
				Role:    "user",
				Content: toolResultContent,
			},
		},
	}
	if err := s.InsertMessages(ctx, tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := s.backfillToolCalls(ctx); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM tool_calls`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 tool call after backfill, got %d", count)
	}

	var resultText string
	if err := s.db.GetContext(ctx, &resultText, `SELECT result_text FROM tool_calls LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText, "ok") {
		t.Fatalf("expected backfilled result text to contain ok, got %q", resultText)
	}
}

func TestListCommandsUsesToolCalls(t *testing.T) {
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
	projectID, err := s.InsertProject(ctx, tx, "proj", "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionDBID, err := s.InsertSession(ctx, tx, projectID, model.SessionEntry{SessionID: "sess-2", FirstPrompt: "hello"}, "/src/sess-2.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	toolUseContent, err := json.Marshal([]model.ContentBlock{{
		Type:  "tool_use",
		ID:    "tu2",
		Name:  "Bash",
		Input: json.RawMessage(`{"command":"ls -la"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	messages := []model.ConversationMessage{{
		Type:      "assistant",
		Timestamp: "2025-01-01T00:00:00Z",
		Message:   model.MessagePayload{Role: "assistant", Content: toolUseContent},
	}}
	if err := s.InsertMessages(ctx, tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertToolCalls(ctx, tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	commands, total, err := s.ListCommands(ctx, 10, 0, CommandFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("expected total 1 command, got %d", total)
	}
	if len(commands) != 1 || commands[0].Command != "ls -la" {
		t.Fatalf("unexpected command rows: %+v", commands)
	}
}

func TestBackfillToolCallsRepairsPartialDerivedData(t *testing.T) {
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
	projectID, err := s.InsertProject(ctx, tx, "proj", "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionDBID, err := s.InsertSession(ctx, tx, projectID, model.SessionEntry{
		SessionID:     "sess-partial",
		FirstPrompt:   "hello",
		ToolUseCounts: map[string]int{"Bash": 2},
	}, "/src/sess-partial.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	toolUseContent, err := json.Marshal([]model.ContentBlock{
		{Type: "tool_use", ID: "tu-a", Name: "Bash", Input: json.RawMessage(`{"command":"echo a"}`)},
		{Type: "tool_use", ID: "tu-b", Name: "Bash", Input: json.RawMessage(`{"command":"echo b"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := []model.ConversationMessage{{
		Type:      "assistant",
		Timestamp: "2025-01-01T00:00:00Z",
		Message:   model.MessagePayload{Role: "assistant", Content: toolUseContent},
	}}
	if err := s.InsertMessages(ctx, tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tool_calls (session_id, seq, timestamp, tool_name, tool_kind, input_json, result_text, file_path, searchable_text)
		VALUES (?, 0, '2025-01-01T00:00:00Z', 'Bash', 'shell', '{"command":"echo a"}', '', '', 'Bash\necho a')`, sessionDBID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := s.backfillToolCalls(ctx); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM tool_calls WHERE session_id = ?`, sessionDBID); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected repaired tool call count 2, got %d", count)
	}
}

func TestGetSessionCodeOperationsSupportsMultiEdit(t *testing.T) {
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
	projectID, err := s.InsertProject(ctx, tx, "proj", "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionDBID, err := s.InsertSession(ctx, tx, projectID, model.SessionEntry{
		SessionID:     "sess-code",
		FirstPrompt:   "edit code",
		ToolUseCounts: map[string]int{"MultiEdit": 1},
	}, "/src/sess-code.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	toolUseContent, err := json.Marshal([]model.ContentBlock{{
		Type: "tool_use",
		ID:   "tu-me",
		Name: "MultiEdit",
		Input: json.RawMessage(`{
			"file_path":"/tmp/demo.go",
			"edits":[
				{"old_string":"old a","new_string":"new a"},
				{"old_string":"old b","new_string":"new b"}
			]
		}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	messages := []model.ConversationMessage{{
		Type:      "assistant",
		Timestamp: "2025-01-01T00:00:00Z",
		Message:   model.MessagePayload{Role: "assistant", Content: toolUseContent},
	}}
	if err := s.InsertMessages(ctx, tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertToolCalls(ctx, tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	ops, err := s.GetSessionCodeOperations(ctx, "proj", "sess-code")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 MultiEdit operations, got %d", len(ops))
	}
	if ops[0].Tool != "MultiEdit" || ops[0].FilePath != "/tmp/demo.go" || ops[0].OldString != "old a" || ops[0].Content != "new a" {
		t.Fatalf("unexpected first MultiEdit operation: %+v", ops[0])
	}
	if ops[1].OldString != "old b" || ops[1].Content != "new b" {
		t.Fatalf("unexpected second MultiEdit operation: %+v", ops[1])
	}
}
