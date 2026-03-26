package index

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func TestRunRecordsParseFailuresAndUnresolvedLinks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	projectDir := filepath.Join(root, "projects", "test-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")
	sessionContent := "{\"type\":\"user\",\"timestamp\":\"2025-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"hello\"}}\nnot json\n"
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	todosDir := filepath.Join(root, "todos")
	if err := os.MkdirAll(todosDir, 0o755); err != nil {
		t.Fatal(err)
	}
	missingTodoPath := filepath.Join(todosDir, "11111111-2222-3333-4444-555555555555-agent-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.json")
	if err := os.WriteFile(missingTodoPath, []byte(`[{"content":"todo item","status":"pending"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := Run(ctx, root, db, false, io.Discard); err != nil {
		t.Fatal(err)
	}

	run, err := db.GetLatestIngestRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run == nil {
		t.Fatal("expected ingest run")
	}
	if run.Status != "partial" {
		t.Fatalf("expected partial ingest status, got %q", run.Status)
	}
	if run.ParseFailures == 0 {
		t.Fatal("expected parse failures to be recorded")
	}
	if run.UnresolvedLinks == 0 {
		t.Fatal("expected unresolved links to be recorded")
	}
	if run.WarningCount < 2 {
		t.Fatalf("expected at least 2 warnings, got %d", run.WarningCount)
	}

	issues, err := db.ListIngestIssues(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundParse := false
	foundLink := false
	for _, issue := range issues {
		switch issue.Category {
		case "parse_failure":
			foundParse = true
		case "unresolved_link":
			foundLink = true
		}
	}
	if !foundParse {
		t.Fatal("expected parse_failure ingest issue")
	}
	if !foundLink {
		t.Fatal("expected unresolved_link ingest issue")
	}
}
