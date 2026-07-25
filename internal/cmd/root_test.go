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

// --watch-interval is a v1 leftover: v2 re-indexes on filesystem events,
// so the value is ignored. It must not be REJECTED either — failing on a
// setting that changes nothing is the worst of both, and it broke scripts
// carrying the flag over from v1.
func TestRunAcceptsDeprecatedWatchInterval(t *testing.T) {
	for _, value := range []string{"0", "-5", "30"} {
		cmd := newRootTestCommand(t)
		if err := cmd.Flags().Set("watch-interval", value); err != nil {
			t.Fatal(err)
		}
		if err := run(cmd, nil); err != nil {
			t.Errorf("--watch-interval=%s failed the run: %v", value, err)
		}
	}
}

// The flag is marked deprecated so cobra prints a notice and keeps it out
// of the advertised help, rather than documenting behaviour it no longer
// has.
func TestWatchIntervalIsDeprecated(t *testing.T) {
	f := rootCmd.Flags().Lookup("watch-interval")
	if f == nil {
		t.Fatal("--watch-interval was removed; it must stay accepted for v1 scripts")
	}
	if f.Deprecated == "" {
		t.Error("--watch-interval is not marked deprecated")
	}
	if !f.Hidden {
		t.Error("deprecated flags should not appear in help output")
	}
	// --watch's own help must not still promise periodic re-indexing.
	w := rootCmd.Flags().Lookup("watch")
	if w == nil {
		t.Fatal("--watch is missing")
	}
	if strings.Contains(strings.ToLower(w.Usage), "periodic") {
		t.Errorf("--watch help still says %q", w.Usage)
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
