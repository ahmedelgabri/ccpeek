package index

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func TestIndexedToolCallQueries(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	testdataDir := filepath.Join("..", "..", "testdata")
	if err := Run(ctx, testdataDir, "", s, true, io.Discard); err != nil {
		t.Fatal(err)
	}

	commands, err := s.GetSessionCommands(ctx, "test-project", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0].Command != "ls -la" {
		t.Fatalf("unexpected commands: %+v", commands)
	}

	calls, err := s.GetSessionToolCalls(ctx, "test-project", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].Name != "Bash" || calls[0].Detail != "ls -la" {
		t.Fatalf("unexpected first tool call: %+v", calls[0])
	}

	stats, err := s.GetSessionToolStats(ctx, "test-project", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 tool stats rows, got %d", len(stats))
	}

	codeOps, err := s.GetSessionCodeOperations(ctx, "test-project", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	if len(codeOps) != 1 {
		t.Fatalf("expected 1 code operation, got %d", len(codeOps))
	}
	if codeOps[0].Tool != "Write" || codeOps[0].FilePath != "/tmp/test.go" {
		t.Fatalf("unexpected code operation: %+v", codeOps[0])
	}

	timeline, err := s.GetToolTimeline(ctx, "test-project", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 2 {
		t.Fatalf("expected 2 tool timeline entries, got %d", len(timeline))
	}
}
