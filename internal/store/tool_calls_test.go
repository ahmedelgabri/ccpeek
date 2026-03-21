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
	projectID, err := s.InsertProject(ctx, tx, "proj", "Project")
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
	projectID, err := s.InsertProject(ctx, tx, "proj", "Project")
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
