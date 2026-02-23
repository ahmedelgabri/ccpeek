package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/server"
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
	rootCmd.Flags().String("data-dir", filepath.Join(os.TempDir(), ".ccpeek"), "Data output/read directory")
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
	dataDir, _ := cmd.Flags().GetString("data-dir")
	skipIndex, _ := cmd.Flags().GetBool("skip-index")
	indexOnly, _ := cmd.Flags().GetBool("index-only")
	openBrowser, _ := cmd.Flags().GetBool("open")
	watch, _ := cmd.Flags().GetBool("watch")

	if !skipIndex {
		fmt.Println("Indexing", claudeDir, "->", dataDir)
		if err := index.Run(claudeDir, dataDir); err != nil {
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

	return server.ListenAndServe(addr, dataDir, claudeDir, watch)
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
