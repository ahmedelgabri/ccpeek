package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newScanTestCommand(dataFile, format string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("data-file", dataFile, "")
	cmd.Flags().String("format", format, "")
	return cmd
}

func TestRunScanRejectsInvalidFormat(t *testing.T) {
	cmd := newScanTestCommand(filepath.Join(t.TempDir(), "ccpeek.db"), "wat")

	err := runScan(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid format error")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}
}

func TestRunScanNoFindingsShowsCleanOutput(t *testing.T) {
	cmd := newScanTestCommand(filepath.Join(t.TempDir(), "ccpeek.db"), "text")

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
