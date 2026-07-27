package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/ahmedelgabri/ccpeek/internal/secrets"
	"github.com/spf13/cobra"
)

var (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
)

func init() {
	if os.Getenv("NO_COLOR") != "" || !isTerminal(os.Stderr) {
		colorReset = ""
		colorRed = ""
		colorGreen = ""
		colorYellow = ""
		colorDim = ""
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
	Long: `Scan your agent history for leaked secrets, API keys, tokens,
passwords, and other sensitive values.

Uses gitleaks detection rules (150+ patterns) to identify potential leaks
in conversation messages, bash commands, plans, shell snapshots, paste
cache, and memories.

Sessions written since the last index pass are indexed first (--no-index
to scan the index as it stands), so a CI check reports on what is on disk
now rather than on whatever a previous run happened to index. Exit code 2
means non-ignored findings exist.

Results are stored in the database and viewable in the web UI at /scan/.`,
	RunE: runScan,
}

func init() {
	scanCmd.Flags().StringP("format", "f", "text", "Output format: text, json")
	scanCmd.Flags().Bool("full", false, "Re-scan everything, discarding incremental scan state")
	scanCmd.Flags().Bool("no-index", false, "Skip the incremental re-index before scanning")
	rootCmd.AddCommand(scanCmd)
}

// runScan scans the index — every agent's transcripts and artifacts, not
// just Claude's. Exit code 2 on non-ignored findings.
func runScan(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	format, _ := cmd.Flags().GetString("format")
	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported format %q: use text or json", format)
	}

	// Index first, like every other read command (`ccpeek query`), unless
	// --no-index says otherwise. Scanning the index as-is meant this
	// command answered about a snapshot of unknown age: a session written
	// since the last index pass — the one holding the key someone just
	// pasted — was invisible, so the exit-2 contract a CI job depends on
	// green-lit it. The pass is incremental; on an up-to-date index it
	// costs a stat per source.
	eng, err := openEngine(ctx, cmd, skipFlag(cmd, "no-index"), os.Stderr)
	if err != nil {
		return err
	}
	defer eng.Close()

	fmt.Fprintln(os.Stderr, "Scanning for secrets...")
	scanner, err := secrets.New(eng.store)
	if err != nil {
		return err
	}
	run := scanner.Run
	if full, _ := cmd.Flags().GetBool("full"); full {
		run = scanner.RunFull
	}
	findings, report, err := run(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Scanned %d changed session(s), %d changed artifact(s)\n",
		report.SessionsScanned, report.ArtifactsScanned)

	active := 0
	for _, f := range findings {
		if !f.Ignored {
			active++
		}
	}

	if format == "json" {
		if err := emit(findings, false); err != nil {
			return err
		}
	} else {
		if len(findings) == 0 {
			fmt.Println("No secrets detected.")
		}
		for _, f := range findings {
			marker := "!"
			if f.Ignored {
				marker = "-"
			}
			fmt.Printf("%s %-24s %-9s %s (line %d): %s\n",
				marker, f.RuleID, f.EntityType, f.NaturalKey, f.Line, f.MatchRedacted)
		}
		if active > 0 {
			fmt.Printf("\nWARNING: %d non-ignored finding(s)\n", active)
		}
	}

	if active > 0 {
		// Returned, not os.Exit: the deferred engine close has to run, and
		// the findings are already reported above.
		return &exitError{code: exitScanFindings}
	}
	return nil
}
