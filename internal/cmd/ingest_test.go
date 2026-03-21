package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
	"github.com/spf13/cobra"
)

func TestRunIngestLatestShowsDiagnostics(t *testing.T) {
	ctx := context.Background()
	dataFile := filepath.Join(t.TempDir(), "ccpeek.db")
	db, err := store.Open(ctx, dataFile)
	if err != nil {
		t.Fatal(err)
	}
	run := &model.IngestRun{
		Mode:            "incremental",
		Status:          "partial",
		ClaudeDir:       "/tmp/.claude",
		StartedAt:       "2025-01-01T00:00:00Z",
		FinishedAt:      "2025-01-01T00:00:01Z",
		DurationMS:      1000,
		FilesSeen:       10,
		FilesChanged:    2,
		RecordsIndexed:  4,
		SkippedFiles:    1,
		SkippedRows:     1,
		ParseFailures:   1,
		UnresolvedLinks: 1,
		WarningCount:    2,
	}
	issues := []model.IngestIssue{{
		Severity:   "warning",
		Category:   "parse_failure",
		SourceType: "session",
		SourcePath: "/tmp/.claude/projects/p/s.jsonl",
		LineNumber: 2,
		Detail:     "invalid JSON",
		CreatedAt:  "2025-01-01T00:00:00Z",
	}}
	if err := db.SaveIngestRun(ctx, run, issues); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("data-file", dataFile, "")
	cmd.Flags().String("format", "text", "")
	cmd.Flags().Int("limit", 10, "")
	cmd.Flags().Bool("latest", true, "")
	cmd.Flags().Int64("run-id", 0, "")

	output := captureStdout(t, func() {
		if err := runIngest(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "Run ") || !strings.Contains(output, "Diagnostics:") {
		t.Fatalf("expected ingest detail output, got:\n%s", output)
	}
	if !strings.Contains(output, "parse_failure") {
		t.Fatalf("expected parse_failure in output, got:\n%s", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
