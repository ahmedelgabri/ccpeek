package cmd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/spf13/cobra"
)

func seedIngestDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	dataFile := filepath.Join(t.TempDir(), "ccpeek.db")
	store, err := db.Open(ctx, v2DBPath(dataFile))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	markInitialized(t, store)

	runID, err := store.StartRun(ctx, "incremental", `["/tmp/.claude"]`)
	if err != nil {
		t.Fatal(err)
	}
	counts := db.RunCounts{
		FilesSeen:       10,
		FilesChanged:    2,
		RecordsIndexed:  4,
		SkippedRows:     1,
		ParseFailures:   1,
		UnresolvedLinks: 1,
		WarningCount:    2,
	}
	if err := store.FinishRun(ctx, runID, "partial", time.Now(), counts, ""); err != nil {
		t.Fatal(err)
	}
	issues := []canon.Issue{{
		Agent:      "claude-code",
		Severity:   "warning",
		Category:   "parse_failure",
		SourcePath: "/tmp/.claude/projects/p/s.jsonl",
		Line:       2,
		Detail:     "invalid JSON",
	}}
	if err := store.InsertIssues(ctx, runID, issues); err != nil {
		t.Fatal(err)
	}

	return dataFile
}

func newIngestTestCommand(dataFile string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("data-file", dataFile, "")
	cmd.Flags().String("claude-dir", "", "")
	cmd.Flags().String("format", "text", "")
	cmd.Flags().Int("limit", 10, "")
	cmd.Flags().Bool("latest", false, "")
	cmd.Flags().Int64("run-id", 0, "")
	return cmd
}

func TestRunIngestLatestShowsDiagnostics(t *testing.T) {
	dataFile := seedIngestDB(t)
	cmd := newIngestTestCommand(dataFile)
	if err := cmd.Flags().Set("latest", "true"); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureOutputPair(t, func() error {
		return runIngest(cmd, nil)
	})
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Run ") || !strings.Contains(stdout, "Diagnostics:") {
		t.Fatalf("expected ingest detail output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "parse_failure") {
		t.Fatalf("expected parse_failure in output, got:\n%s", stdout)
	}
}

func TestRunIngestLatestJSONShowsDetails(t *testing.T) {
	dataFile := seedIngestDB(t)
	cmd := newIngestTestCommand(dataFile)
	if err := cmd.Flags().Set("latest", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("format", "json"); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureOutputPair(t, func() error {
		return runIngest(cmd, nil)
	})
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}

	var payload struct {
		Run    *db.IngestRun    `json:"run"`
		Issues []db.IngestIssue `json:"issues"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("expected valid json output, got error %v and output %q", err, stdout)
	}
	if payload.Run == nil || payload.Run.Status != "partial" {
		t.Fatalf("expected partial ingest run in payload, got %+v", payload.Run)
	}
	if len(payload.Issues) != 1 || payload.Issues[0].Category != "parse_failure" {
		t.Fatalf("unexpected issues payload: %+v", payload.Issues)
	}
}

func TestRunIngestRejectsInvalidFormat(t *testing.T) {
	cmd := newIngestTestCommand(filepath.Join(t.TempDir(), "ccpeek.db"))
	if err := cmd.Flags().Set("format", "wat"); err != nil {
		t.Fatal(err)
	}

	err := runIngest(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid format error")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}
}

func TestRunIngestRejectsLatestAndRunID(t *testing.T) {
	cmd := newIngestTestCommand(filepath.Join(t.TempDir(), "ccpeek.db"))
	if err := cmd.Flags().Set("latest", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("run-id", "12"); err != nil {
		t.Fatal(err)
	}

	err := runIngest(cmd, nil)
	if err == nil {
		t.Fatal("expected mutually exclusive flag error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive flag error, got %v", err)
	}
}

func TestRunIngestLatestWithNoRunsShowsMessage(t *testing.T) {
	ctx := context.Background()
	dataFile := filepath.Join(t.TempDir(), "ccpeek.db")
	store, err := db.Open(ctx, v2DBPath(dataFile))
	if err != nil {
		t.Fatal(err)
	}
	markInitialized(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := newIngestTestCommand(dataFile)
	if err := cmd.Flags().Set("latest", "true"); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureOutputPair(t, func() error {
		return runIngest(cmd, nil)
	})
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "No ingest runs found.") {
		t.Fatalf("expected no-runs message, got %q", stderr)
	}
}
