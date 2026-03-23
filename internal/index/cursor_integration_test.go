package index

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func TestRunCursorOnlyJSONL(t *testing.T) {
	ctx := context.Background()
	claudeDir := t.TempDir()
	cursorDir := writeCursorFixture(t)

	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(ctx, claudeDir, cursorDir, s, true, io.Discard); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 cursor project, got %d", len(projects))
	}
	if projects[0].Source != model.SourceCursor {
		t.Fatalf("expected cursor project source, got %q", projects[0].Source)
	}
	if len(projects[0].Sessions) != 1 {
		t.Fatalf("expected 1 cursor session, got %d", len(projects[0].Sessions))
	}
	if projects[0].Sessions[0].Source != model.SourceCursor {
		t.Fatalf("expected cursor session source, got %q", projects[0].Sessions[0].Source)
	}
	if projects[0].Sessions[0].MetadataOnly {
		t.Fatal("expected transcript session to not be metadata-only")
	}

	plans, err := s.ListPlans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Source != model.SourceCursor {
		t.Fatalf("expected one cursor plan, got %+v", plans)
	}

	todos, err := s.ListTodos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 1 || todos[0].Source != model.SourceCursor {
		t.Fatalf("expected one cursor todo, got %+v", todos)
	}
}

func TestRunMixedClaudeAndCursor(t *testing.T) {
	ctx := context.Background()
	claudeDir := filepath.Join("..", "..", "testdata")
	cursorDir := writeCursorFixture(t)

	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(ctx, claudeDir, cursorDir, s, true, io.Discard); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	plans, err := s.ListPlans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hasClaude, hasCursor bool
	for _, p := range plans {
		switch p.Source {
		case model.SourceCursor:
			hasCursor = true
		case "", model.SourceClaudeCode:
			hasClaude = true
		}
	}
	if !hasClaude || !hasCursor {
		t.Fatalf("expected mixed plan sources (claude=%v cursor=%v)", hasClaude, hasCursor)
	}
}

func writeCursorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	projectDir := filepath.Join(root, "projects", "test-project", "agent-transcripts")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"role":"user","message":{"role":"user","content":"hello from cursor"}}
{"role":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"echo hi"}}]}}`
	if err := os.WriteFile(filepath.Join(projectDir, "session-1.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	plansDir := filepath.Join(root, "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := `---
name: Cursor Fixture Plan
overview: test fixture
todos:
  - id: t1
    content: do thing
    status: pending
---
# Cursor Fixture Plan
`
	if err := os.WriteFile(filepath.Join(plansDir, "fixture.plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	return root
}
