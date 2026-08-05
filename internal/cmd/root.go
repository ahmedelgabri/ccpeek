package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/api"
	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/secrets"
	"github.com/ahmedelgabri/ccpeek/internal/webui"
	"github.com/spf13/cobra"
)

var Version = "dev"

// The Short line is also the man page's NAME entry, so it has to name
// the product rather than one of the five agents it indexes; the Long
// one has to say what the BARE command does, which is the thing `ccpeek
// --help` never mentioned — a user reading it could not learn that
// `ccpeek` with no arguments indexes their history and starts a server.
var rootCmd = &cobra.Command{
	Use:   "ccpeek",
	Short: "Index and explore your coding-agent history",
	Long: `ccpeek indexes local sessions from Claude Code, Pi, Codex CLI, OpenCode,
and Cursor into one searchable database with real token usage and
estimated cost. Everything stays on this machine.

Run with no arguments, ccpeek indexes every agent's history and serves
the web UI on http://127.0.0.1:3000 (--port to change it, --open to
launch a browser, --index-only to index and exit). The port is bound
immediately and the first index pass runs behind it.

Other surfaces over the same index: ` + "`ccpeek query`" + ` (JSON for agents and
scripts), ` + "`ccpeek mcp`" + ` (MCP server over stdio), ` + "`ccpeek scan`" + ` (secret
scanning), ` + "`ccpeek export`" + ` (shell history), ` + "`ccpeek doctor`" + ` (what was
detected where).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       Version,
	RunE:          run,
}

// dataDirErr records why the default database location could not be
// derived, so every command that needs the store fails with the reason
// instead of quietly writing the archive somewhere else. Flag defaults
// are computed at init time, where there is nobody to return an error
// to; resolveDataFile raises it at the point of use.
var dataDirErr error

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	defaultDataFile := ""
	dir, err := dataDir()
	if err != nil {
		dataDirErr = err
	} else {
		defaultDataFile = filepath.Join(dir, "ccpeek.db")
	}

	rootCmd.PersistentFlags().String("claude-dir", filepath.Join(home, ".claude"), "Path to Claude Code data directory (alias for --root claude-code=<path>)")
	rootCmd.PersistentFlags().StringArray("root", nil, "Override an agent's data directory: <agent>=<path>. Repeatable, e.g. --root codex=~/backup/codex")
	rootCmd.PersistentFlags().String("data-file", defaultDataFile,
		"Path of the LEGACY v1 database (imported once, never modified). The v2 index this ccpeek reads and writes lives at a sibling derived from this name — ccpeek2.db for the default ccpeek.db, <name>.v2.db otherwise. Do NOT point it at a v2 index")

	rootCmd.Flags().IntP("port", "p", 3000, "Server port")
	rootCmd.Flags().Bool("skip-index", false, "Skip indexing, serve existing data")
	rootCmd.Flags().Bool("index-only", false, "Index and exit (don't start server)")
	rootCmd.Flags().BoolP("open", "o", false, "Open browser after starting server")
	rootCmd.Flags().BoolP("watch", "w", false, "Re-index while serving, on filesystem changes")
	rootCmd.Flags().Bool("rebuild", false, "Force full rebuild (drop all data and re-index from scratch)")
	rootCmd.Flags().Bool("prune", false, "Remove data from source files that no longer exist on disk")
	rootCmd.Flags().Bool("skip-scan", false, "Skip secret scanning after indexing")
	rootCmd.Flags().BoolP("quiet", "q", false, "Suppress informational output")
	// v2 watches with fsnotify (or, on kqueue platforms, per-file watches
	// plus an adaptive scan — see ingest.Runner.Watch), so there is no
	// interval to set.
	// The flag stays accepted so existing scripts keep working, but it is
	// marked deprecated (cobra prints the notice when it is passed) rather
	// than advertised with help text that describes behaviour it no longer
	// has — and it is no longer validated, since rejecting a value that
	// changes nothing is the worst of both.
	rootCmd.Flags().Int("watch-interval", 30, "Re-index interval in seconds")
	_ = rootCmd.Flags().MarkDeprecated("watch-interval",
		"v2 re-indexes on filesystem events; the interval is ignored")
}

// ExecuteContext runs the CLI under ctx, which every command reaches
// through cmd.Context(). main wires signal cancellation into it: ONE
// source of signal context for the whole binary, so a command's own
// shutdown path (the HTTP server's graceful stop, the MCP server's
// background indexing) cannot double-register handlers and disagree
// about who cancels what.
// It is also the ONLY place the process exits non-zero. A command that
// needs a specific exit code returns an exitError instead of calling
// os.Exit, which would skip its own deferred cleanup (the query and scan
// paths defer the store close); by the time this unwraps one, cobra has
// returned and the defers have run.
func ExecuteContext(ctx context.Context) {
	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return
	}
	code := 1
	var exit *exitError
	if errors.As(err, &exit) {
		// A nil cause means the command already reported the failure in
		// its own contract (the JSON envelope on stdout).
		code, err = exit.code, exit.err
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}

func run(cmd *cobra.Command, args []string) error {
	// Cancellation arrives from ExecuteContext, not from a handler
	// installed here. The nil guard is for tests that call run directly.
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	port, _ := cmd.Flags().GetInt("port")
	// Read ONCE, as the decision the engine takes (flag name attached), and
	// reused by the conflict checks below — a second GetBool for the same
	// flag is a second thing to keep in step.
	skipIndex := skipFlag(cmd, "skip-index")
	indexOnly, _ := cmd.Flags().GetBool("index-only")
	openBrowser, _ := cmd.Flags().GetBool("open")
	watch, _ := cmd.Flags().GetBool("watch")
	rebuild, _ := cmd.Flags().GetBool("rebuild")
	prune, _ := cmd.Flags().GetBool("prune")
	skipScan, _ := cmd.Flags().GetBool("skip-scan")
	quiet, _ := cmd.Flags().GetBool("quiet")

	// Validate port early to avoid failing after indexing
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}

	// Contradictory flags are rejected, never quietly dropped — a flag
	// that changes nothing looks like it worked. Two pairs were already
	// rejected here; the ones below were the same kind of contradiction
	// and were being ignored: --index-only never starts a server, so
	// every serving flag beside it asks for something that cannot happen,
	// and --prune is work the skipped pass would have done.
	if skipIndex.skip && indexOnly {
		return fmt.Errorf("--skip-index and --index-only are mutually exclusive")
	}
	if skipIndex.skip && rebuild {
		return fmt.Errorf("--skip-index and --rebuild are mutually exclusive")
	}
	if skipIndex.skip && prune {
		return fmt.Errorf("--skip-index and --prune are mutually exclusive: pruning happens during an index pass")
	}
	if indexOnly {
		for _, name := range []string{"watch", "open", "port"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--index-only and --%s are mutually exclusive: --index-only indexes and exits without serving", name)
			}
		}
	}

	// logf prints to stderr unless --quiet is set
	logf := func(format string, a ...any) {
		if !quiet {
			fmt.Fprintf(os.Stderr, format, a...)
		}
	}

	// Progress from the bootstrap pass feeds three consumers: the stderr
	// logger, /api/v1/health (the UI banner shows live counts), and
	// throttled SSE notifies so pages fill in while the first pass is
	// still running instead of sitting on zeros until it finishes.
	events := api.NewBroadcaster()
	var (
		progMu     sync.Mutex
		prog       api.IndexProgress
		lastNotify time.Time
	)
	readProgress := func() api.IndexProgress {
		progMu.Lock()
		defer progMu.Unlock()
		return prog
	}

	// The engine owns indexing and serving. The legacy v1 database, when
	// present, was imported on first run and stays untouched for rollback.
	// It also validates the root flags — an explicitly-passed --claude-dir
	// or --root that does not exist fails here, on every path. That check
	// used to live in this function and only in this function, so the
	// subcommands accepted a missing directory the root command rejected.
	eng, bootstrap, err := openEngineDeferred(ctx, cmd, skipIndex, os.Stderr, func(o *ingest.Options) {
		o.Rebuild = rebuild
		o.Prune = prune
		var logProgress func(ingest.Progress)
		if !quiet {
			logProgress = newProgressLogger(os.Stderr)
		}
		o.Progress = func(p ingest.Progress) {
			progMu.Lock()
			prog = api.IndexProgress{Agent: string(p.Agent), Seen: p.Seen, Changed: p.Changed}
			notify := time.Since(lastNotify) > 2*time.Second
			if notify {
				lastNotify = time.Now()
			}
			progMu.Unlock()
			if notify {
				events.Notify()
			}
			if logProgress != nil {
				logProgress(p)
			}
		}
	})
	if err != nil {
		return err
	}
	defer eng.Close()

	// Both recorded outcomes in ONE meta round trip, because health and
	// readiness both want both: the v1 import outcome so a failed import
	// stays visible in the UI instead of dissolving into a startup log line
	// (the bootstrap retries it on every start until it succeeds), and the
	// bootstrap outcome so /api/v1/ready can say WHY it is holding at 503
	// (index-failed with the recorded error, not a perpetual "indexing").
	// The SPA polls health every 1.5s for the whole first pass; two reads
	// of the same table per poll was one too many.
	readOutcomes := func() (api.V1ImportStatus, api.BootstrapStatus) {
		meta, err := eng.store.GetMetaMulti(ctx,
			metaV1ImportState, metaV1ImportError, metaV1ImportedAt,
			metaBootstrapState, metaBootstrapError)
		if err != nil {
			return api.V1ImportStatus{}, api.BootstrapStatus{}
		}
		return api.V1ImportStatus{
				State:      meta[metaV1ImportState],
				Error:      meta[metaV1ImportError],
				ImportedAt: meta[metaV1ImportedAt],
			}, api.BootstrapStatus{
				State: meta[metaBootstrapState],
				Error: meta[metaBootstrapError],
			}
	}

	// The mutex serializes the bootstrap scan with watch-triggered
	// rescans — the scanner's per-entity state updates must not
	// interleave.
	var scanMu sync.Mutex
	runScan := func(ctx context.Context, ingestReport *ingest.Report) error {
		if skipScan {
			return nil
		}
		scanMu.Lock()
		defer scanMu.Unlock()
		return scanChanged(ctx, eng, ingestReport, logf)
	}

	if indexOnly {
		if bootstrap != nil {
			if err := bootstrap(ctx); err != nil {
				return err
			}
		}
		return runScan(ctx, eng.report)
	}

	// Serve first: the port is reachable immediately and the first index
	// pass (minutes on a large history) runs behind it, streaming into the
	// UI via SSE when done. /api/v1/ready flips to 200 at that point.
	//
	// The listener is bound HERE, before the browser is launched or the
	// index goroutine starts, so "address already in use" fails cleanly
	// and --open can never race the bind.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	ln, err := listen(ctx, addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	logf("Serving on %s\n", url)
	if !webui.Embedded() {
		logf("%sWARNING%s this binary has no embedded web UI (built with plain `go build`?) — the API works; use a released binary or `just build` for the UI\n",
			colorYellow, colorReset)
	}

	// The watch pass reuses the run's root overrides, resolved here so a
	// malformed --root fails the command rather than a background
	// goroutine (openEngineDeferred has already validated them, so this
	// cannot realistically fail — but the error has nowhere to go inside
	// the goroutine).
	watchOpts, err := ingestOptions(cmd)
	if err != nil {
		return err
	}
	watchOpts.Prune = prune

	var ready atomic.Bool
	go func() {
		if bootstrap != nil {
			err := bootstrap(ctx)
			if err != nil {
				// Readiness is NOT flipped. The server keeps serving what is
				// already indexed (queries answer, the UI loads), but
				// /api/v1/ready stays 503 — the same policy a failed v1 import
				// gets, for the same reason: a caller that blocks on readiness
				// is asking "is the history complete", and after a failed pass
				// it is not. The pass recorded the reason in meta so the
				// state outlives this log line.
				events.Notify()
				if ctx.Err() == nil {
					logf("WARNING: indexing failed (serving what is already indexed; /api/v1/ready stays 503): %v\n", err)
				}
				return
			}
			ready.Store(true)
			events.Notify()
			// The bootstrap scan runs to completion BEFORE watch starts:
			// the scanner pages messages across several read snapshots, so
			// a concurrent watch ingest could rewrite sessions mid-scan and
			// leave scan state pairing a new content hash with a stale page
			// set. The server is already serving; only watch pickup waits.
			// Watch passes themselves scan inside their onChange callback,
			// which the watch loop runs synchronously between passes — so
			// ingest and scanning never overlap anywhere.
			if err := runScan(ctx, eng.report); err != nil && ctx.Err() == nil {
				logf("WARNING: %v\n", err)
			}
			events.Notify() // findings changed; refresh the scan views
		} else {
			ready.Store(true)
		}
		if watch {
			logf("Watch mode enabled (re-indexing on changes)\n")
			// Watch passes carry the prune policy the serve run started
			// with, and re-scan what each pass changed — otherwise new
			// secrets (and pruned sources) stay stale until a restart.
			if err := eng.runner.Watch(ctx, watchOpts, 0, func(rep *ingest.Report) {
				events.Notify() // fresh data first; the scan follows
				if err := runScan(ctx, rep); err != nil && ctx.Err() == nil {
					logf("WARNING: %v\n", err)
				}
				events.Notify()
			}); err != nil && ctx.Err() == nil {
				logf("WARNING: watch stopped: %v\n", err)
			}
		}
	}()

	if openBrowser {
		openURL(url)
	}

	return serve(ctx, ln, buildServeHandler(api.Handler(eng.query, api.Deps{
		Events:   events,
		Ready:    ready.Load,
		Progress: readProgress,
		Outcomes: readOutcomes,
	})))
}

// scanChanged scans what an ingest pass changed and reports through
// logf. Both serving paths use it — HTTP and MCP index the same store
// with the same passes, and a second copy of this would drift.
//
// It returns errors rather than exiting: on a serving path a scan
// problem must not take the server down.
func scanChanged(ctx context.Context, eng *engine, ingestReport *ingest.Report, logf func(string, ...any)) error {
	if ingestReport == nil || ingestReport.FilesChanged == 0 {
		return nil
	}
	logf("Scanning for secrets...\n")
	scanner, err := secrets.New(eng.store)
	if err != nil {
		return fmt.Errorf("initializing scanner: %w", err)
	}
	findings, report, err := scanner.Run(ctx)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}
	logf("  Scanned %d changed session(s), %d changed artifact(s)\n",
		report.SessionsScanned, report.ArtifactsScanned)
	active := 0
	for _, f := range findings {
		if !f.Ignored {
			active++
		}
	}
	if active == 0 {
		logf("  %sNo secrets detected.%s\n", colorGreen, colorReset)
	} else {
		logf("  %s%sWARNING%s %s%d potential secret(s) found. Run `ccpeek scan` for details.%s\n",
			colorBold, colorYellow, colorReset, colorYellow, active, colorReset)
	}
	return nil
}

// newProgressLogger prints ingest progress to w: one line per discovered
// root and a throttled counter line while sources are hashed/parsed, so a
// first run over a multi-GB history never looks hung.
func newProgressLogger(w io.Writer) func(ingest.Progress) {
	var last time.Time
	return func(p ingest.Progress) {
		if p.Root {
			fmt.Fprintf(w, "Indexing %s: %s (%d sources)\n", p.Agent, p.Path, p.Total)
			return
		}
		if time.Since(last) < 2*time.Second {
			return
		}
		last = time.Now()
		fmt.Fprintf(w, "  … %d sources checked, %d changed (%s)\n", p.Seen, p.Changed, p.Path)
	}
}

// dataDir returns the XDG data directory for ccpeek: $XDG_DATA_HOME/ccpeek,
// or ~/.local/share/ccpeek.
//
// With no home directory to derive it from, it FAILS rather than picking
// a location. The fallback used to be the OS temp directory — where an
// index that took minutes to build, and holds history no longer on disk
// anywhere else, lives until the next reboot clears it. Nothing about
// the run would have said so. Callers surface this error the first time
// a command needs the store, and --data-file (or XDG_DATA_HOME) resolves
// it; commands that never open the store are unaffected.
func dataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "ccpeek"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the ccpeek database: no home directory (%w) — set XDG_DATA_HOME to the directory that should hold the index, or pass --data-file", err)
	}
	return filepath.Join(home, ".local", "share", "ccpeek"), nil
}

func openURL(url string) {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		fmt.Fprintf(os.Stderr, "Auto-open not supported on %s, visit %s manually\n", runtime.GOOS, url)
		return
	}
	if err := exec.Command(name, url).Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open browser: %v\n", err)
	}
}
