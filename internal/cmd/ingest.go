package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/spf13/cobra"
)

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Show ingest/index run history and diagnostics",
	Long: `Show recent indexing runs and any diagnostics captured during ingest.

Examples:
  ccpeek ingest
  ccpeek ingest --latest
  ccpeek ingest --run-id 12
  ccpeek ingest --format json`,
	RunE: runIngest,
}

func init() {
	ingestCmd.Flags().StringP("format", "f", "text", "Output format: text, json")
	ingestCmd.Flags().Int("limit", 10, "Maximum number of runs to list")
	ingestCmd.Flags().Bool("latest", false, "Show the latest run with full issue details")
	ingestCmd.Flags().Int64("run-id", 0, "Show a specific run with full issue details")
	rootCmd.AddCommand(ingestCmd)
}

func runIngest(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	format, _ := cmd.Flags().GetString("format")
	limit, _ := cmd.Flags().GetInt("limit")
	latest, _ := cmd.Flags().GetBool("latest")
	runID, _ := cmd.Flags().GetInt64("run-id")

	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported format %q: use text or json", format)
	}
	if latest && runID != 0 {
		return fmt.Errorf("--latest and --run-id are mutually exclusive")
	}

	// Diagnostics inspect past runs; kicking off a new ingest here would
	// bury the run being investigated.
	eng, err := openEngine(ctx, cmd, neverIndex(), os.Stderr)
	if err != nil {
		return err
	}
	defer eng.Close()

	if latest || runID != 0 {
		run, err := eng.store.GetRun(ctx, runID)
		if err != nil {
			return err
		}
		if run == nil {
			fmt.Fprintln(os.Stderr, "No ingest runs found.")
			return nil
		}
		issues, err := eng.store.ListRunIssues(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("loading ingest issues: %w", err)
		}
		if format == "json" {
			payload := struct {
				Run    *db.IngestRun    `json:"run"`
				Issues []db.IngestIssue `json:"issues"`
			}{Run: run, Issues: issues}
			return emit(payload, false)
		}
		printIngestRunDetails(run, issues)
		return nil
	}

	runs, err := eng.store.ListRuns(ctx, limit)
	if err != nil {
		return err
	}
	if format == "json" {
		return emit(runs, false)
	}
	printIngestRunList(runs)
	return nil
}

func printIngestRunList(runs []db.IngestRun) {
	if len(runs) == 0 {
		fmt.Println("No ingest runs recorded yet.")
		return
	}

	fmt.Printf("%-4s %-11s %-8s %-20s %6s %7s %7s\n", "ID", "MODE", "STATUS", "FINISHED", "WARN", "FILES", "INDEXED")
	for _, run := range runs {
		fmt.Printf(
			"%-4d %-11s %-8s %-20s %6d %7s %7d\n",
			run.ID,
			run.Mode,
			run.Status,
			trimTimestamp(run.FinishedAt),
			run.WarningCount,
			fmt.Sprintf("%d/%d", run.FilesChanged, run.FilesSeen),
			run.RecordsIndexed,
		)
	}
	fmt.Println()
	fmt.Println("Use `ccpeek ingest --latest` for the newest run or `ccpeek ingest --run-id <id>` for details.")
}

func printIngestRunDetails(run *db.IngestRun, issues []db.IngestIssue) {
	fmt.Printf("Run %d\n", run.ID)
	fmt.Printf("  Mode:      %s\n", run.Mode)
	fmt.Printf("  Status:    %s\n", run.Status)
	fmt.Printf("  Started:   %s\n", run.StartedAt)
	fmt.Printf("  Finished:  %s\n", run.FinishedAt)
	fmt.Printf("  Duration:  %dms\n", run.DurationMS)
	fmt.Printf("  Roots:     %s\n", string(run.Roots))
	fmt.Printf("  Files:     %d seen, %d changed\n", run.FilesSeen, run.FilesChanged)
	fmt.Printf("  Indexed:   %d\n", run.RecordsIndexed)
	fmt.Printf("  Warnings:  %d total (%d parse failures, %d unresolved links)\n",
		run.WarningCount, run.ParseFailures, run.UnresolvedLinks)
	if run.ErrorMessage != "" {
		fmt.Printf("  Error:     %s\n", run.ErrorMessage)
	}
	if len(issues) == 0 {
		fmt.Println("\nNo diagnostics recorded for this run.")
		return
	}

	fmt.Println("\nDiagnostics:")
	for i, issue := range issues {
		line := ""
		if issue.Line > 0 {
			line = ":" + strconv.Itoa(issue.Line)
		}
		agent := ""
		if issue.AgentSlug != "" {
			agent = issue.AgentSlug + " "
		}
		fmt.Printf("  %d. [%s/%s] %s%s%s\n", i+1, issue.Severity, issue.Category, agent, issue.SourcePath, line)
		fmt.Printf("     %s\n", issue.Detail)
	}
}

// trimTimestamp renders an RFC3339 timestamp for the fixed-width run
// table: seconds precision, and the date-time separator as a space so
// the column reads as a date and a time.
//
// It used to TrimSuffix the "T" off ts[:19] — position 19 lands after
// the seconds, never on the separator at position 10, so the suffix
// never matched and every row printed the raw 2026-07-27T10:00:00.
func trimTimestamp(ts string) string {
	if len(ts) > 19 {
		ts = ts[:19]
	}
	return strings.Replace(ts, "T", " ", 1)
}
