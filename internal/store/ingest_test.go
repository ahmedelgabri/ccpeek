package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

func TestSaveAndLoadIngestRun(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	run := &model.IngestRun{
		Mode:            "incremental",
		Status:          "partial",
		ClaudeDir:       "/tmp/.claude",
		StartedAt:       "2025-01-01T00:00:00Z",
		FinishedAt:      "2025-01-01T00:00:01Z",
		DurationMS:      1000,
		FilesSeen:       12,
		FilesChanged:    2,
		RecordsIndexed:  8,
		SkippedFiles:    1,
		SkippedRows:     2,
		ParseFailures:   1,
		UnresolvedLinks: 1,
		WarningCount:    3,
	}
	issues := []model.IngestIssue{
		{
			Severity:   "warning",
			Category:   "parse_failure",
			SourceType: "session",
			SourcePath: "/tmp/.claude/projects/p/s.jsonl",
			LineNumber: 3,
			Detail:     "invalid JSON",
			CreatedAt:  "2025-01-01T00:00:00Z",
		},
	}

	if err := s.SaveIngestRun(ctx, run, issues); err != nil {
		t.Fatal(err)
	}
	if run.ID == 0 {
		t.Fatal("expected run ID to be set")
	}

	latest, err := s.GetLatestIngestRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil {
		t.Fatal("expected latest ingest run")
	}
	if latest.WarningCount != 3 {
		t.Fatalf("expected warning count 3, got %d", latest.WarningCount)
	}

	storedIssues, err := s.ListIngestIssues(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedIssues) != 1 {
		t.Fatalf("expected 1 ingest issue, got %d", len(storedIssues))
	}
	if storedIssues[0].RunID != run.ID {
		t.Fatalf("expected issue run ID %d, got %d", run.ID, storedIssues[0].RunID)
	}
}

func TestOpenTightensFilePermissions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ccpeek.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("expected %s permissions 0600, got %o", filepath.Base(p), got)
		}
	}
}
