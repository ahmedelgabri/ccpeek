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

	// run() indexes, so every agent root is pinned inside the test's temp
	// directories — pinRoots does exactly that (Claude by flag, the rest by
	// their env overrides) and also registers --data-file and --root.
	// Without it the root command's tests ingest (and secret scan) the
	// developer's real ~/.claude, ~/.codex, ~/.cursor and friends.
	cmd := pinRoots(t, filepath.Join(t.TempDir(), "ccpeek.db"), t.TempDir())
	cmd.Flags().Int("port", 3000, "")
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

// A flag that contradicts another is rejected, not ignored. Two pairs
// were already rejected; these were silently dropped, so `ccpeek
// --index-only --watch --open --port 8080` printed nothing about the
// three flags it was about to disregard, and --skip-index --prune looked
// like it had pruned.
func TestRunRejectsContradictoryFlags(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  map[string]string
		want string
	}{
		{
			name: "index-only and watch",
			set:  map[string]string{"watch": "true"},
			want: "--index-only and --watch",
		},
		{
			name: "index-only and open",
			set:  map[string]string{"open": "true"},
			want: "--index-only and --open",
		},
		{
			name: "index-only and port",
			set:  map[string]string{"port": "4321"},
			want: "--index-only and --port",
		},
		{
			name: "skip-index and prune",
			set:  map[string]string{"index-only": "false", "skip-index": "true", "prune": "true"},
			want: "--skip-index and --prune",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRootTestCommand(t)
			for flag, value := range tt.set {
				if err := cmd.Flags().Set(flag, value); err != nil {
					t.Fatal(err)
				}
			}
			err := run(cmd, nil)
			if err == nil {
				t.Fatal("the contradiction was accepted")
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "mutually exclusive") {
				t.Fatalf("error = %v, want it to name %s as mutually exclusive", err, tt.want)
			}
		})
	}

	// A serving flag left at its default is not a contradiction: only a
	// flag the user actually passed is.
	t.Run("defaults are not contradictions", func(t *testing.T) {
		cmd := newRootTestCommand(t)
		if err := run(cmd, nil); err != nil {
			t.Fatalf("plain --index-only failed: %v", err)
		}
	})
}

// `ccpeek --help` is where someone learns what the bare command does.
// The Short line (which is also the man page's NAME entry) named one of
// the five agents ccpeek indexes, and nothing anywhere said that running
// it with no arguments indexes the history and starts a web server.
func TestRootHelpDescribesTheProduct(t *testing.T) {
	short := strings.ToLower(rootCmd.Short)
	if strings.Contains(short, "claude code") {
		t.Errorf("Short still names one agent of five: %q", rootCmd.Short)
	}
	if !strings.Contains(short, "index") {
		t.Errorf("Short does not say ccpeek indexes anything: %q", rootCmd.Short)
	}

	long := strings.ToLower(rootCmd.Long)
	if long == "" {
		t.Fatal("the root command has no Long help")
	}
	for _, want := range []string{"3000", "server", "index"} {
		if !strings.Contains(long, want) {
			t.Errorf("Long help does not mention %q:\n%s", want, rootCmd.Long)
		}
	}
	// All five agents, so nobody has to guess whether theirs is covered.
	for _, agent := range []string{"claude code", "pi", "codex", "opencode", "cursor"} {
		if !strings.Contains(long, agent) {
			t.Errorf("Long help does not mention %q", agent)
		}
	}
}

// --data-file is the sharpest edge in the CLI: aimed at the v2 index it
// derives a second store and starts a v1 import that cannot ever
// succeed. The help has to say which database it names.
func TestDataFileHelpNamesTheLegacyDatabase(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("data-file")
	if f == nil {
		t.Fatal("--data-file is missing")
	}
	usage := strings.ToLower(f.Usage)
	for _, want := range []string{"legacy", "v1", "sibling"} {
		if !strings.Contains(usage, want) {
			t.Errorf("--data-file help does not mention %q: %q", want, f.Usage)
		}
	}
	if !strings.Contains(usage, "not point it at a v2") && !strings.Contains(usage, "do not point") {
		t.Errorf("--data-file help does not warn against pointing it at a v2 index: %q", f.Usage)
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
