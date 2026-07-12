package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/spf13/cobra"
)

func newScanTestCommand(t *testing.T, dataFile, format string) *cobra.Command {
	t.Helper()
	// Pre-create the store so the engine skips the first-run bootstrap
	// ingest (which would scan real agent roots).
	store, err := db.Open(context.Background(), storeDBPath(dataFile))
	if err != nil {
		t.Fatal(err)
	}
	markInitialized(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("data-file", dataFile, "")
	cmd.Flags().String("claude-dir", "", "")
	cmd.Flags().String("format", format, "")
	return cmd
}

func TestRunScanRejectsInvalidFormat(t *testing.T) {
	cmd := newScanTestCommand(t, filepath.Join(t.TempDir(), "ccpeek.db"), "wat")

	err := runScan(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid format error")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}
}

func TestRunScanNoFindingsShowsCleanOutput(t *testing.T) {
	cmd := newScanTestCommand(t, filepath.Join(t.TempDir(), "ccpeek.db"), "text")

	stdout, stderr := captureOutputPair(t, func() error {
		return runScan(cmd, nil)
	})
	if !strings.Contains(stderr, "Scanning for secrets...") {
		t.Fatalf("expected scan progress message, got %q", stderr)
	}
	if !strings.Contains(stdout, "No secrets detected") {
		t.Fatalf("expected clean scan output, got %q", stdout)
	}
}
