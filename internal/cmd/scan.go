package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/ahmedelgabri/ccpeek/internal/scan"
	"github.com/ahmedelgabri/ccpeek/internal/store"
	"github.com/spf13/cobra"
)

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
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	dataFile, _ := cmd.Flags().GetString("data-file")

	db, err := store.Open(dataFile)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	fmt.Println("Scanning for secrets...")

	scanner, err := scan.New(db)
	if err != nil {
		return fmt.Errorf("initializing scanner: %w", err)
	}

	findings, err := scanner.Run()
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	if len(findings) == 0 {
		fmt.Println("No secrets found.")
		return nil
	}

	fmt.Printf("Found %d potential secret(s).\n\n", len(findings))

	// Summary by rule
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

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RULE\tCOUNT\tSOURCE TYPES")
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
		fmt.Fprintf(w, "%s\t%d\t%s\n", rc.rule, rc.count, joinStrings(typeList))
	}
	w.Flush()

	fmt.Printf("\nView details in the web UI at /scan/\n")
	return nil
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}
