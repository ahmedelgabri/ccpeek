package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/spf13/cobra"
)

// newScanTestCommand builds the flag set runScan reads. `ccpeek scan`
// indexes before scanning, so every agent root is pinned inside the
// test's temp directories — claudeDir is the only one a test writes to.
func newScanTestCommand(t *testing.T, dataFile, claudeDir, format string) *cobra.Command {
	t.Helper()
	// Pre-create the store so the engine treats it as past its first run:
	// what the tests exercise is the incremental pass, not the bootstrap.
	store, err := db.Open(context.Background(), storeDBPath(dataFile))
	if err != nil {
		t.Fatal(err)
	}
	markInitialized(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := pinRoots(t, dataFile, claudeDir)
	cmd.Flags().StringArray("root", nil, "")
	cmd.Flags().String("format", format, "")
	cmd.Flags().Bool("full", false, "")
	cmd.Flags().Bool("no-index", false, "")
	return cmd
}

func TestRunScanRejectsInvalidFormat(t *testing.T) {
	cmd := newScanTestCommand(t, filepath.Join(t.TempDir(), "ccpeek.db"), t.TempDir(), "wat")

	err := runScan(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid format error")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}
}

func TestRunScanNoFindingsShowsCleanOutput(t *testing.T) {
	cmd := newScanTestCommand(t, filepath.Join(t.TempDir(), "ccpeek.db"), t.TempDir(), "text")

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

// leakySessionPrompt is a Slack bot token shape — gitleaks' default rules
// match it including the entropy check (synthetic AWS/GitHub example keys
// are allowlisted). Assembled at runtime so the source file itself does
// not trip secret scanners.
func leakySessionPrompt() string {
	return "deploy with " + "xoxb-3336494366" + "76-7992618528" + "69-clFJVVIaoJahpORboa3Ba2al"
}

// `ccpeek scan` is the CI gate: exit 2 means "secrets found". It used to
// scan the index as-is, so anything written since the last index pass —
// including the session that just leaked a key — was invisible to it and
// the gate reported success. The scan now indexes first, incrementally,
// the way `ccpeek query` does.
func TestRunScanIndexesBeforeScanning(t *testing.T) {
	claudeDir := t.TempDir()
	cmd := newScanTestCommand(t, filepath.Join(t.TempDir(), "ccpeek.db"), claudeDir, "text")

	// Written AFTER the store exists and with no indexing run in between:
	// this is the source no separate `ccpeek` invocation has ever seen.
	writeClaudeSession(t, claudeDir, "33333333-3333-3333-3333-333333333333", leakySessionPrompt())

	var runErr error
	stdout, _ := captureOutputPair(t, func() error {
		runErr = runScan(cmd, nil)
		return nil
	})
	if code := exitCodeOf(t, runErr); code != exitScanFindings {
		t.Fatalf("exit code = %d, want %d (the fresh session was never scanned)", code, exitScanFindings)
	}
	if !strings.Contains(stdout, "non-ignored finding") {
		t.Errorf("scan output does not report the finding:\n%s", stdout)
	}
}

// --no-index keeps the old behaviour available for anyone who wants the
// index exactly as it stands (and does not want a pass over every root).
func TestRunScanNoIndexScansTheIndexAsItStands(t *testing.T) {
	claudeDir := t.TempDir()
	cmd := newScanTestCommand(t, filepath.Join(t.TempDir(), "ccpeek.db"), claudeDir, "text")
	if err := cmd.Flags().Set("no-index", "true"); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, claudeDir, "44444444-4444-4444-4444-444444444444", leakySessionPrompt())

	var runErr error
	stdout, _ := captureOutputPair(t, func() error {
		runErr = runScan(cmd, nil)
		return nil
	})
	if runErr != nil {
		t.Fatalf("--no-index scan = %v, want a clean exit over the empty index", runErr)
	}
	if !strings.Contains(stdout, "No secrets detected") {
		t.Errorf("--no-index indexed anyway:\n%s", stdout)
	}
}
