package cmd

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
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

// ingest.Options.ConfigRoots has always been per-agent, and
// agent.ResolveRoots applies it to all five adapters — but --claude-dir
// was the only flag feeding it, so users of the other four could relocate
// their data only through each agent's own environment variable.
func TestRootFlagOverridesAnyAgent(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want map[canon.AgentSlug][]string
	}{
		{
			name: "no overrides leaves the env in charge",
			args: nil,
			want: nil,
		},
		{
			name: "claude-dir still works",
			args: []string{"--claude-dir", "/backup/claude"},
			want: map[canon.AgentSlug][]string{"claude-code": {"/backup/claude"}},
		},
		{
			name: "root covers the other adapters",
			args: []string{"--root", "codex=/backup/codex"},
			want: map[canon.AgentSlug][]string{"codex": {"/backup/codex"}},
		},
		{
			name: "root repeats",
			args: []string{"--root", "pi=/a", "--root", "cursor=/b"},
			want: map[canon.AgentSlug][]string{"pi": {"/a"}, "cursor": {"/b"}},
		},
		{
			name: "root and claude-dir compose",
			args: []string{"--claude-dir", "/c", "--root", "opencode=/o"},
			want: map[canon.AgentSlug][]string{"claude-code": {"/c"}, "opencode": {"/o"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().String("claude-dir", "", "")
			cmd.Flags().StringArray("root", nil, "")
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatal(err)
			}
			opts, err := ingestOptions(cmd)
			if err != nil {
				t.Fatalf("ingestOptions: %v", err)
			}
			if !maps.EqualFunc(opts.ConfigRoots, tt.want, slices.Equal) {
				t.Errorf("ConfigRoots = %v, want %v", opts.ConfigRoots, tt.want)
			}
		})
	}
}

// A typo in --root must fail loudly rather than silently override the
// roots of an adapter that does not exist.
func TestRootFlagRejectsMalformedSpecs(t *testing.T) {
	for _, spec := range []string{"nopath", "=/only/path", "claude-code=", "nosuchagent=/p"} {
		cmd := &cobra.Command{}
		cmd.Flags().String("claude-dir", "", "")
		cmd.Flags().StringArray("root", nil, "")
		if err := cmd.ParseFlags([]string{"--root", spec}); err != nil {
			t.Fatal(err)
		}
		if _, err := ingestOptions(cmd); err == nil {
			t.Errorf("--root %q accepted, want an error", spec)
		}
	}
}
