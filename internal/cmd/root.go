package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/scan"
	"github.com/ahmedelgabri/ccpeek/internal/server"
	"github.com/ahmedelgabri/ccpeek/internal/store"
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

	rootCmd.PersistentFlags().String("claude-dir", filepath.Join(home, ".claude"), "Source directory")
	rootCmd.PersistentFlags().String("data-file", filepath.Join(dataDir(), "ccpeek.db"), "SQLite database file path")

	rootCmd.Flags().IntP("port", "p", 3000, "Server port")
	rootCmd.Flags().Bool("skip-index", false, "Skip indexing, serve existing data")
	rootCmd.Flags().Bool("index-only", false, "Index and exit (don't start server)")
	rootCmd.Flags().Bool("open", false, "Open browser after starting server")
	rootCmd.Flags().Bool("watch", false, "Re-index periodically while serving")
	rootCmd.Flags().Bool("rebuild", false, "Force full rebuild (drop all data and re-index from scratch)")
	rootCmd.Flags().Bool("prune", false, "Remove data from source files that no longer exist on disk")
	rootCmd.Flags().Bool("skip-scan", false, "Skip secret scanning after indexing")
	rootCmd.Flags().BoolP("quiet", "q", false, "Suppress informational output")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	claudeDir, _ := cmd.Flags().GetString("claude-dir")
	dataFile, _ := cmd.Flags().GetString("data-file")
	skipIndex, _ := cmd.Flags().GetBool("skip-index")
	indexOnly, _ := cmd.Flags().GetBool("index-only")
	openBrowser, _ := cmd.Flags().GetBool("open")
	watch, _ := cmd.Flags().GetBool("watch")
	rebuild, _ := cmd.Flags().GetBool("rebuild")
	prune, _ := cmd.Flags().GetBool("prune")
	skipScan, _ := cmd.Flags().GetBool("skip-scan")
	quiet, _ := cmd.Flags().GetBool("quiet")

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

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o700); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}

	dbPath := dataFile
	db, err := store.Open(dbPath)
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

	// Check if the claude data directory exists
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) && !skipIndex {
		return fmt.Errorf("claude data directory not found: %s (is Claude Code installed?)", claudeDir)
	}

	dataChanged := false
	if !skipIndex {
		logf("Indexing %s -> %s\n", claudeDir, dbPath)
		if rebuild {
			if err := index.Run(claudeDir, db, true, os.Stderr); err != nil {
				return fmt.Errorf("indexing failed: %w", err)
			}
			dataChanged = true
		} else {
			changed, err := index.RunIncremental(claudeDir, db)
			if err != nil {
				return fmt.Errorf("indexing failed: %w", err)
			}
			dataChanged = changed
		}
		logf("Indexing complete.\n")
	}

	if prune {
		logf("Pruning deleted source files...\n")
		if err := index.Prune(claudeDir, db, os.Stderr); err != nil {
			return fmt.Errorf("pruning failed: %w", err)
		}
		logf("Pruning complete.\n")
	}

	if !skipScan && (dataChanged || rebuild) {
		logf("Scanning for secrets...\n")
		scanner, err := scan.New(db)
		if err != nil {
			return fmt.Errorf("initializing scanner: %w", err)
		}
		findings, err := scanner.Run()
		if err != nil {
			return fmt.Errorf("scan failed: %w", err)
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
		logf("Watch mode enabled, re-indexing every 30s\n")
	}

	if openBrowser {
		openURL(url)
	}

	return server.ListenAndServe(addr, db, claudeDir, watch)
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
		return
	}
	_ = exec.Command(name, url).Start()
}
