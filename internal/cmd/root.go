package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/scan"
	"github.com/ahmedelgabri/ccpeek/internal/server"
	"github.com/ahmedelgabri/ccpeek/internal/store"
	"github.com/spf13/cobra"
)

var Version = "dev"

var rootCmd = &cobra.Command{
	Use:           "ccpeek",
	Short:         "Explore your AI coding history",
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
	rootCmd.PersistentFlags().String("cursor-dir", filepath.Join(home, ".cursor"), "Path to Cursor data directory")
	rootCmd.PersistentFlags().Bool("cursor-sqlite", true, "Index Cursor SQLite metadata/file history (can be expensive on large stores)")
	rootCmd.PersistentFlags().Int("cursor-sqlite-max-db-size-mb", 0, "Skip Cursor SQLite DB files larger than this size in MB (0 disables size limit)")
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
	cursorDir, _ := cmd.Flags().GetString("cursor-dir")
	cursorSQLiteEnabled, _ := cmd.Flags().GetBool("cursor-sqlite")
	cursorSQLiteMaxDBMB, _ := cmd.Flags().GetInt("cursor-sqlite-max-db-size-mb")
	dataFile, _ := cmd.Flags().GetString("data-file")
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

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o700); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}

	dbPath := dataFile
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		hint := ""
		if strings.Contains(err.Error(), "locked") {
			hint = " (is another ccpeek instance running?)"
		}
		return fmt.Errorf("opening database: %w%s", err, hint)
	}
	defer db.Close()

	// logf prints to stderr unless --quiet is set
	logf := func(format string, a ...any) {
		if !quiet {
			fmt.Fprintf(os.Stderr, format, a...)
		}
	}

	indexOpts := index.DefaultRunOptions
	indexOpts.IncludeCursorSQLite = cursorSQLiteEnabled
	if cursorSQLiteMaxDBMB > 0 {
		indexOpts.MaxCursorSQLiteBytes = int64(cursorSQLiteMaxDBMB) * 1024 * 1024
	}

	// Check if the claude data directory exists
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) && !skipIndex {
		return fmt.Errorf("claude data directory not found: %s (is Claude Code installed?)", claudeDir)
	}

	dataChanged := false
	if !skipIndex {
		beforeRunID, _ := latestIngestRunID(ctx, db)
		logf("Indexing Claude (%s) + Cursor (%s) -> %s\n", claudeDir, cursorDir, dbPath)
		if !cursorSQLiteEnabled {
			logf("  Cursor SQLite indexing disabled (--cursor-sqlite=false)\n")
		} else if cursorSQLiteMaxDBMB > 0 {
			logf("  Cursor SQLite DB size limit: %d MB (--cursor-sqlite-max-db-size-mb)\n", cursorSQLiteMaxDBMB)
		}
		if rebuild {
			if err := index.RunWithOptions(ctx, claudeDir, cursorDir, db, true, os.Stderr, indexOpts); err != nil {
				return fmt.Errorf("indexing failed: %w", err)
			}
			dataChanged = true
		} else {
			changed, err := index.RunIncrementalWithOptions(ctx, claudeDir, cursorDir, db, indexOpts)
			if err != nil {
				return fmt.Errorf("indexing failed: %w", err)
			}
			dataChanged = changed
		}
		if err := db.EnsureFilePermissions(); err != nil {
			return fmt.Errorf("tightening database permissions: %w", err)
		}
		if run, err := ingestRunAfter(ctx, db, beforeRunID); err == nil {
			logIngestWarnings(logf, run)
		}
		logf("Indexing complete.\n")
	}

	if prune {
		beforeRunID, _ := latestIngestRunID(ctx, db)
		logf("Pruning deleted source files...\n")
		if err := index.Prune(ctx, claudeDir, cursorDir, db, os.Stderr); err != nil {
			return fmt.Errorf("pruning failed: %w", err)
		}
		if err := db.EnsureFilePermissions(); err != nil {
			return fmt.Errorf("tightening database permissions: %w", err)
		}
		if run, err := ingestRunAfter(ctx, db, beforeRunID); err == nil {
			logIngestWarnings(logf, run)
		}
		logf("Pruning complete.\n")
	}

	if !skipScan && (dataChanged || rebuild) {
		logf("Scanning for secrets...\n")
		scanner, err := scan.New(db)
		if err != nil {
			return fmt.Errorf("initializing scanner: %w", err)
		}
		findings, err := scanner.Run(ctx)
		if err != nil {
			return fmt.Errorf("scan failed: %w", err)
		}
		if err := db.EnsureFilePermissions(); err != nil {
			return fmt.Errorf("tightening database permissions: %w", err)
		}
		if len(findings) == 0 {
			logf("  %sNo secrets detected.%s\n", colorGreen, colorReset)
		} else {
			logf("  %s%sWARNING%s %s%d potential secret(s) found. Run `ccpeek scan` for details.%s\n",
				colorBold, colorYellow, colorReset, colorYellow, len(findings), colorReset)
		}
	}

	if indexOnly {
		return nil
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	logf("Serving on %s\n", url)

	if watch {
		logf("Watch mode enabled, re-indexing every %ds\n", watchInterval)
	}

	if openBrowser {
		openURL(url)
	}

	return server.ListenAndServeWithOptions(
		ctx,
		addr,
		db,
		claudeDir,
		cursorDir,
		watch,
		time.Duration(watchInterval)*time.Second,
		!skipScan,
		indexOpts,
	)
}

func latestIngestRunID(ctx context.Context, db *store.Store) (int64, error) {
	run, err := db.GetLatestIngestRun(ctx)
	if err != nil || run == nil {
		return 0, err
	}
	return run.ID, nil
}

func ingestRunAfter(ctx context.Context, db *store.Store, previousID int64) (*model.IngestRun, error) {
	run, err := db.GetLatestIngestRun(ctx)
	if err != nil || run == nil || run.ID == previousID {
		return nil, err
	}
	return run, nil
}

func logIngestWarnings(logf func(string, ...any), run *model.IngestRun) {
	if run == nil || run.WarningCount == 0 {
		return
	}
	logf("  %s%sWARNING%s %sIngest completed with %d diagnostic(s): %d skipped file(s), %d skipped row(s), %d parse failure(s), %d unresolved link(s). Run `ccpeek ingest --latest` for details.%s\n",
		colorBold, colorYellow, colorReset, colorYellow,
		run.WarningCount, run.SkippedFiles, run.SkippedRows, run.ParseFailures, run.UnresolvedLinks, colorReset)
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
