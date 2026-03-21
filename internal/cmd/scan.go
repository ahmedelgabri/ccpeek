package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/scan"
	"github.com/ahmedelgabri/ccpeek/internal/store"
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
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	dataFile, _ := cmd.Flags().GetString("data-file")
	format, _ := cmd.Flags().GetString("format")

	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported format %q: use text or json", format)
	}

	db, err := store.Open(ctx, dataFile)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	fmt.Fprintln(os.Stderr, "Scanning for secrets...")

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

	if format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(findings); err != nil {
			return fmt.Errorf("encoding json: %w", err)
		}
	} else {
		printScanResults(findings)
	}

	if len(findings) > 0 {
		os.Exit(2)
	}
	return nil
}

func printScanResults(findings []model.ScanFinding) {
	if len(findings) == 0 {
		fmt.Printf("\n  %s%s CLEAN %s  No secrets detected.%s\n\n",
			colorGreen, colorBold, colorReset+colorGreen, colorReset)
		return
	}

	fmt.Printf("\n  %s%s WARNING %s  Found %d potential secret(s)%s\n\n",
		colorYellow, colorBold, colorReset+colorYellow, len(findings), colorReset)

	// Summary table
	ruleCounts := make(map[string]int)
	for _, f := range findings {
		ruleCounts[f.RuleID]++
	}
	type ruleCount struct {
		rule  string
		count int
	}
	var sorted []ruleCount
	for rule, count := range ruleCounts {
		sorted = append(sorted, ruleCount{rule, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	// Find the longest rule name for alignment
	maxRule := 4 // len("RULE")
	for _, rc := range sorted {
		if len(rc.rule) > maxRule {
			maxRule = len(rc.rule)
		}
	}

	fmt.Printf("  %s%-*s  %5s  %s%s\n", colorDim, maxRule, "RULE", "COUNT", "SOURCE", colorReset)
	for _, rc := range sorted {
		types := make(map[string]bool)
		for _, f := range findings {
			if f.RuleID == rc.rule {
				types[f.SourceType] = true
			}
		}
		var typeList []string
		for t := range types {
			typeList = append(typeList, t)
		}
		sort.Strings(typeList)
		fmt.Printf("  %s%-*s%s  %5d  %s\n",
			colorRed, maxRule, rc.rule, colorReset, rc.count, strings.Join(typeList, ", "))
	}

	// Individual findings
	fmt.Printf("\n  %sFindings:%s\n\n", colorBold, colorReset)
	for i, f := range findings {
		fmt.Printf("  %s%d.%s %s%s%s %s%s%s\n",
			colorDim, i+1, colorReset,
			colorRed, f.RuleID, colorReset,
			colorDim, f.Description, colorReset)
		fmt.Printf("     %sSecret:%s  %s\n", colorDim, colorReset, f.MatchRedacted)
		fmt.Printf("     %sSource:%s  %s %s(%s)%s\n",
			colorDim, colorReset, f.SourceType,
			colorDim, f.SourceID, colorReset)
		if i < len(findings)-1 {
			fmt.Println()
		}
	}

	fmt.Printf("\n  %sView and manage findings in the web UI at /scan/%s\n\n", colorDim, colorReset)
}
