package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunRejectsInvalidWatchInterval(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Int("port", 3000, "")
	cmd.Flags().String("claude-dir", t.TempDir(), "")
	cmd.Flags().String("data-file", filepath.Join(t.TempDir(), "ccpeek.db"), "")
	cmd.Flags().Bool("skip-index", true, "")
	cmd.Flags().Bool("index-only", false, "")
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Bool("watch", false, "")
	cmd.Flags().Bool("rebuild", false, "")
	cmd.Flags().Bool("prune", false, "")
	cmd.Flags().Bool("skip-scan", true, "")
	cmd.Flags().Bool("quiet", true, "")
	cmd.Flags().Int("watch-interval", 0, "")

	err := run(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid watch interval error")
	}
	if !strings.Contains(err.Error(), "watch interval") {
		t.Fatalf("expected watch interval error, got %v", err)
	}
}
