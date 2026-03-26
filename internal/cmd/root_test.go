package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newRootTestCommand(t *testing.T) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{}
	cmd.Flags().Int("port", 3000, "")
	cmd.Flags().String("claude-dir", t.TempDir(), "")
	cmd.Flags().String("data-file", filepath.Join(t.TempDir(), "ccpeek.db"), "")
	cmd.Flags().Bool("skip-index", false, "")
	cmd.Flags().Bool("index-only", true, "")
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Bool("watch", false, "")
	cmd.Flags().Bool("rebuild", false, "")
	cmd.Flags().Bool("prune", false, "")
	cmd.Flags().Bool("skip-scan", true, "")
	cmd.Flags().Bool("quiet", true, "")
	cmd.Flags().Int("watch-interval", 30, "")
	return cmd
}

func TestRunRejectsInvalidWatchInterval(t *testing.T) {
	cmd := newRootTestCommand(t)
	if err := cmd.Flags().Set("watch-interval", "0"); err != nil {
		t.Fatal(err)
	}

	err := run(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid watch interval error")
	}
	if !strings.Contains(err.Error(), "watch interval") {
		t.Fatalf("expected watch interval error, got %v", err)
	}
}

func TestRunRejectsInvalidPort(t *testing.T) {
	cmd := newRootTestCommand(t)
	if err := cmd.Flags().Set("port", "65536"); err != nil {
		t.Fatal(err)
	}

	err := run(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid port error")
	}
	if !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("expected invalid port error, got %v", err)
	}
}

func TestRunRejectsSkipIndexAndIndexOnly(t *testing.T) {
	cmd := newRootTestCommand(t)
	if err := cmd.Flags().Set("skip-index", "true"); err != nil {
		t.Fatal(err)
	}

	err := run(cmd, nil)
	if err == nil {
		t.Fatal("expected mutually exclusive flag error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive error, got %v", err)
	}
}

func TestRunRejectsSkipIndexAndRebuild(t *testing.T) {
	cmd := newRootTestCommand(t)
	if err := cmd.Flags().Set("skip-index", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("index-only", "false"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("rebuild", "true"); err != nil {
		t.Fatal(err)
	}

	err := run(cmd, nil)
	if err == nil {
		t.Fatal("expected mutually exclusive flag error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive error, got %v", err)
	}
}

func TestRunRejectsMissingClaudeDirWhenIndexing(t *testing.T) {
	cmd := newRootTestCommand(t)
	if err := cmd.Flags().Set("claude-dir", filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}

	err := run(cmd, nil)
	if err == nil {
		t.Fatal("expected missing claude dir error")
	}
	if !strings.Contains(err.Error(), "claude data directory not found") {
		t.Fatalf("expected missing claude dir error, got %v", err)
	}
}
