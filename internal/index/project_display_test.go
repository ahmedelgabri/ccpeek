package index

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func TestProjectDisplayNameUsesProjectPathWhenAvailable(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "-Users-me-my-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(`{
  "entries": [
    {
      "sessionId": "11111111-1111-1111-1111-111111111111",
      "created": "2024-01-01T00:00:00Z",
      "modified": "2024-01-01T00:00:01Z",
      "projectPath": "/Users/me/my-project"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	jsonl := `{"type":"user","timestamp":"2024-01-01T00:00:00Z","uuid":"u1","message":{"role":"user","content":"hello"},"sessionId":"11111111-1111-1111-1111-111111111111"}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "11111111-1111-1111-1111-111111111111.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(context.Background(), root, s, true, io.Discard); err != nil {
		t.Fatal(err)
	}

	project, err := s.GetProject(context.Background(), "-Users-me-my-project")
	if err != nil {
		t.Fatal(err)
	}
	if project.DisplayName != "/Users/me/my-project" {
		t.Fatalf("expected display name to use projectPath, got %q", project.DisplayName)
	}
	if project.CanonicalPath != "/Users/me/my-project" {
		t.Fatalf("expected canonical path to use projectPath, got %q", project.CanonicalPath)
	}
}

func TestProjectDisplayNameFallsBackToRawDirName(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "my-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	jsonl := `{"type":"user","timestamp":"2024-01-01T00:00:00Z","uuid":"u1","message":{"role":"user","content":"hello"},"sessionId":"22222222-2222-2222-2222-222222222222"}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "22222222-2222-2222-2222-222222222222.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(context.Background(), root, s, true, io.Discard); err != nil {
		t.Fatal(err)
	}

	project, err := s.GetProject(context.Background(), "my-project")
	if err != nil {
		t.Fatal(err)
	}
	if project.DisplayName != "my-project" {
		t.Fatalf("expected raw dir name fallback, got %q", project.DisplayName)
	}
	if project.CanonicalPath != "" {
		t.Fatalf("expected empty canonical path fallback, got %q", project.CanonicalPath)
	}
}
