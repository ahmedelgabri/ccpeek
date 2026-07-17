package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/api"
	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/secrets"
	"github.com/spf13/cobra"
)

var Version = "dev"

var rootCmd = &cobra.Command{
	Use:           "ccpeek",
	Short:         "Explore your Claude Code history",
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       Version,
	RunE:          run,
}

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	rootCmd.PersistentFlags().String("claude-dir", filepath.Join(home, ".claude"), "Path to Claude Code data directory")
	rootCmd.PersistentFlags().String("data-file", filepath.Join(dataDir(), "ccpeek.db"), "SQLite database file path")

	rootCmd.Flags().IntP("port", "p", 3000, "Server port")
	rootCmd.Flags().Bool("skip-index", false, "Skip indexing, serve existing data")
	rootCmd.Flags().Bool("index-only", false, "Index and exit (don't start server)")
	rootCmd.Flags().BoolP("open", "o", false, "Open browser after starting server")
	rootCmd.Flags().BoolP("watch", "w", false, "Re-index periodically while serving")
	rootCmd.Flags().Bool("rebuild", false, "Force full rebuild (drop all data and re-index from scratch)")
	rootCmd.Flags().Bool("prune", false, "Remove data from source files that no longer exist on disk")
	rootCmd.Flags().Bool("skip-scan", false, "Skip secret scanning after indexing")
	rootCmd.Flags().BoolP("quiet", "q", false, "Suppress informational output")
	rootCmd.Flags().Int("watch-interval", 30, "Re-index interval in seconds (used with --watch)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	port, _ := cmd.Flags().GetInt("port")
	claudeDir, _ := cmd.Flags().GetString("claude-dir")
	skipIndex, _ := cmd.Flags().GetBool("skip-index")
	indexOnly, _ := cmd.Flags().GetBool("index-only")
	openBrowser, _ := cmd.Flags().GetBool("open")
	watch, _ := cmd.Flags().GetBool("watch")
	rebuild, _ := cmd.Flags().GetBool("rebuild")
	prune, _ := cmd.Flags().GetBool("prune")
	skipScan, _ := cmd.Flags().GetBool("skip-scan")
	quiet, _ := cmd.Flags().GetBool("quiet")
	watchInterval, _ := cmd.Flags().GetInt("watch-interval")

	// Validate mutually exclusive flags
	if skipIndex && indexOnly {
		return fmt.Errorf("--skip-index and --index-only are mutually exclusive")
	}
	if skipIndex && rebuild {
		return fmt.Errorf("--skip-index and --rebuild are mutually exclusive")
	}

	// Validate port early to avoid failing after indexing
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}
	if watchInterval <= 0 {
		return fmt.Errorf("invalid watch interval %d: must be greater than 0", watchInterval)
	}

	// logf prints to stderr unless --quiet is set
	logf := func(format string, a ...any) {
		if !quiet {
			fmt.Fprintf(os.Stderr, format, a...)
		}
	}

	// An explicitly-passed --claude-dir that doesn't exist is a user
	// mistake and must fail loudly (missing default roots just mean the
	// agent isn't installed and are fine).
	if cmd.Flags().Changed("claude-dir") && !skipIndex {
		if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
			return fmt.Errorf("claude data directory not found: %s", claudeDir)
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

	// runScan reports rather than fails: on the serving path a scan
	// problem must not take the server down. The mutex serializes the
	// bootstrap scan with watch-triggered rescans — the scanner's
	// per-entity state updates must not interleave.
	var scanMu sync.Mutex
	runScan := func(ctx context.Context, ingestReport *ingest.Report) error {
		if skipScan || ingestReport == nil || ingestReport.FilesChanged == 0 {
			return nil
		}
		scanMu.Lock()
		defer scanMu.Unlock()
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
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	logf("Serving on %s\n", url)

	var ready atomic.Bool
	go func() {
		if bootstrap != nil {
			err := bootstrap(ctx)
			ready.Store(true) // ready even on failure: the server answers from what exists
			events.Notify()
			if err != nil {
				if ctx.Err() == nil {
					logf("WARNING: indexing failed: %v\n", err)
				}
				return
			}
			// The scan is derived data over already-ingested rows — it must
			// never delay watch mode picking up new appends.
			go func() {
				if err := runScan(ctx, eng.report); err != nil && ctx.Err() == nil {
					logf("WARNING: %v\n", err)
				}
				events.Notify() // findings changed; refresh the scan views
			}()
		} else {
			ready.Store(true)
		}
		if watch {
			logf("Watch mode enabled (fsnotify; --watch-interval is a v1 no-op)\n")
			// Watch passes carry the prune policy the serve run started
			// with, and re-scan what each pass changed — otherwise new
			// secrets (and pruned sources) stay stale until a restart.
			watchOpts := ingestOptions(cmd)
			watchOpts.Prune = prune
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

	return serve(ctx, addr, buildServeHandler(api.Handler(eng.query, events, ready.Load, readProgress)))
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

// dataDir returns the XDG data directory for ccpeek.
// It respects $XDG_DATA_HOME, falling back to ~/.local/share/ccpeek.
func dataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "ccpeek")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "ccpeek")
	}
	return filepath.Join(home, ".local", "share", "ccpeek")
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
