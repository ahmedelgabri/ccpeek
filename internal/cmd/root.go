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

	rootCmd.PersistentFlags().String("index-file", "", "Use this v2 archive directly; --data-file may optionally name a legacy database to import")

	rootCmd.Flags().IntP("port", "p", 3000, "Server port")
	rootCmd.Flags().Bool("skip-index", false, "Skip indexing, serve existing data")
	rootCmd.Flags().Bool("index-only", false, "Index and exit (don't start server)")
	rootCmd.Flags().BoolP("open", "o", false, "Open browser after starting server")
	rootCmd.Flags().BoolP("watch", "w", false, "Re-index while serving, on filesystem changes")
	rootCmd.Flags().Bool("rebuild", false, "Reparse available sources and regenerate derived data; retain missing-source history")
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
	return runWithBrowser(cmd, openURL)
}

func runWithBrowser(cmd *cobra.Command, launchBrowser func(string)) error {
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

	// Validate flags before binding, but leave database opening and migrations
	// off the path to the browser. Subcommands also validate in the engine.
	watchOpts, err := ingestOptions(cmd)
	if err != nil {
		return err
	}
	if err := checkExplicitRoots(watchOpts.ConfigRoots); err != nil {
		return err
	}
	watchOpts.Prune = prune
	legacy, err := resolveDataFile(cmd)
	if err != nil {
		return err
	}
	if _, err := resolveIndexFile(cmd, legacy); err != nil {
		return err
	}
	newEngine := func(ctx context.Context) (*engine, func(context.Context) error, error) {
		return openEngineDeferred(ctx, cmd, skipIndex, os.Stderr, func(o *ingest.Options) {
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
	}

	// Scans run serially with watch passes and acquire the archive's
	// maintenance lock themselves, including across processes. The scanner
	// pages through several read snapshots: an interleaved ingest could pair
	// a new content hash with stale pages. The lock keeps that view stable;
	// finishing the startup scan before watch also preserves this ordering.
	runScan := func(ctx context.Context, eng *engine) error {
		if skipScan {
			return nil
		}
		return scanPending(ctx, eng, logf)
	}

	if indexOnly {
		eng, bootstrap, err := newEngine(ctx)
		if err != nil {
			return err
		}
		defer eng.Close()
		if bootstrap != nil {
			if err := bootstrap(ctx); err != nil {
				return err
			}
		}
		return runScan(ctx, eng)
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

	defer ln.Close()
	var ready atomic.Bool
	pending := &switchHandler{next: api.Handler(nil, api.Deps{Events: events})}
	indexCtx, stopIndexing := context.WithCancel(ctx)
	var indexing sync.WaitGroup
	var eng *engine // Read on shutdown only after joining the initializer.
	defer func() {
		stopIndexing()
		indexing.Wait()
		if eng != nil {
			eng.Close()
		}
	}()
	indexing.Add(1)
	go func() {
		defer indexing.Done()
		ctx := indexCtx
		var bootstrap func(context.Context) error
		var err error
		eng, bootstrap, err = newEngine(ctx)
		if err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "ERROR: archive initialization failed: %v\n", err)
				pending.set(api.Handler(nil, api.Deps{
					Events: events,
					Outcomes: func() (api.V1ImportStatus, api.BootstrapStatus) {
						return api.V1ImportStatus{}, api.BootstrapStatus{State: "failed", Error: "Archive initialization failed. See the terminal for details and restart ccpeek to retry."}
					},
				}))
				events.Notify()
			}
			return
		}
		// Publish only after migrations have committed. Existing SSE streams
		// keep the same broadcaster and refresh when the database becomes usable.
		pending.set(api.Handler(eng.query, api.Deps{
			Events: events, Ready: ready.Load, Progress: readProgress,
			Outcomes: func() (api.V1ImportStatus, api.BootstrapStatus) {
				meta, err := eng.store.GetMetaMulti(ctx,
					metaV1ImportState, metaV1ImportError, metaV1ImportedAt,
					metaBootstrapState, metaBootstrapError)
				if err != nil {
					return api.V1ImportStatus{}, api.BootstrapStatus{}
				}
				return api.V1ImportStatus{
					State: meta[metaV1ImportState], Error: meta[metaV1ImportError], ImportedAt: meta[metaV1ImportedAt],
				}, api.BootstrapStatus{State: meta[metaBootstrapState], Error: meta[metaBootstrapError]}
			},
		}))
		events.Notify()
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
		}
		ready.Store(true)
		events.Notify()
		// Catch up on scans even when bootstrap indexing was skipped.
		// Finish this scan before starting the watch loop.
		if err := runScan(ctx, eng); err != nil && ctx.Err() == nil {
			logf("WARNING: %v\n", err)
		}
		events.Notify()
		if watch {
			logf("Watch mode enabled (re-indexing on changes)\n")
			// Watch passes carry the prune policy the serve run started
			// with, and re-scan what each pass changed — otherwise new
			// secrets (and pruned sources) stay stale until a restart.
			watchOpts.WatchPass = func() {
				events.Notify() // fresh data first; the scan follows
				if err := runScan(ctx, eng); err != nil && ctx.Err() == nil {
					logf("WARNING: %v\n", err)
				}
				events.Notify()
			}
			if err := eng.runner.Watch(ctx, watchOpts, 0, nil); err != nil && ctx.Err() == nil {
				logf("WARNING: watch stopped: %v\n", err)
			}
		}
	}()

	if openBrowser {
		launchBrowser(url)
	}

	return serve(ctx, ln, buildServeHandler(pending))
}

// scanPending catches up from persisted scan coverage, independently of
// whether the latest ingest changed files. HTTP and MCP share this path.
func scanPending(ctx context.Context, eng *engine, logf func(string, ...any)) error {
	status, err := eng.query.ArchiveStatus(ctx)
	if err != nil {
		return err
	}
	if !status.Scan.Pending {
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
