package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBold   = "\033[1m"
)

func init() {
	if os.Getenv("NO_COLOR") != "" || !isTerminal(os.Stderr) {
		colorReset = ""
		colorGreen = ""
		colorYellow = ""
		colorBold = ""
	}
}

func isTerminal(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan indexed data for leaked secrets and sensitive values",
	Long: `Scan all indexed Claude Code data for leaked secrets, API keys,
tokens, passwords, and other sensitive values.

Uses gitleaks detection rules (150+ patterns) to identify potential leaks
in conversation messages, bash commands, plans, shell snapshots, paste
cache, and memories.

Results are stored in the database and viewable in the web UI at /scan/.`,
	RunE: runScan,
}

func init() {
	scanCmd.Flags().StringP("format", "f", "text", "Output format: text, json")
	scanCmd.Flags().Bool("v2", true, "Deprecated no-op: the v2 index is always scanned")
	_ = scanCmd.Flags().MarkHidden("v2")
	rootCmd.AddCommand(scanCmd)
}

// runScan scans the v2 index — every agent's transcripts and artifacts.
// (The v1 scanner retired at the v2.0 cutover; exit code 2 on non-ignored
// findings is preserved.)
func runScan(cmd *cobra.Command, args []string) error {
	return runScanV2(cmd)
}
