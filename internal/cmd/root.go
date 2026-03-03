package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/ahmedelgabri/ccpeek/internal/index"
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

	rootCmd.Flags().IntP("port", "p", 3000, "Server port")
	rootCmd.Flags().String("claude-dir", filepath.Join(home, ".claude"), "Source directory")
	rootCmd.Flags().String("data-file", filepath.Join(dataDir(), "ccpeek.db"), "SQLite database file path")
	rootCmd.Flags().Bool("skip-index", false, "Skip indexing, serve existing data")
	rootCmd.Flags().Bool("index-only", false, "Index and exit (don't start server)")
	rootCmd.Flags().Bool("open", false, "Open browser after starting server")
	rootCmd.Flags().Bool("watch", false, "Re-index periodically while serving")
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

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o755); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}

	dbPath := dataFile
	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	if !skipIndex {
		fmt.Println("Indexing", claudeDir, "->", dbPath)
		if err := index.Run(claudeDir, db); err != nil {
			return fmt.Errorf("indexing failed: %w", err)
		}
		fmt.Println("Indexing complete.")
	}

	if indexOnly {
		return nil
	}

	addr := fmt.Sprintf(":%d", port)
	url := fmt.Sprintf("http://localhost:%d", port)
	fmt.Println("Serving on", url)

	if watch {
		fmt.Println("Watch mode enabled, re-indexing every 30s")
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
