package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
	"github.com/spf13/cobra"
)

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Show ingest/index run history and diagnostics",
	Long: `Show recent indexing/pruning runs and any diagnostics captured during ingest.

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
	ctx := context.Background()

	dataFile, _ := cmd.Flags().GetString("data-file")
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

	db, err := store.Open(ctx, dataFile)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	if latest || runID != 0 {
		run, issues, err := loadIngestRunDetails(ctx, db, latest, runID)
		if err != nil {
			return err
		}
		if run == nil {
			fmt.Fprintln(os.Stderr, "No ingest runs found.")
			return nil
		}
		if format == "json" {
			payload := struct {
				Run    *model.IngestRun    `json:"run"`
				Issues []model.IngestIssue `json:"issues"`
			}{Run: run, Issues: issues}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		printIngestRunDetails(run, issues)
		return nil
	}

	runs, err := db.ListIngestRuns(ctx, limit)
	if err != nil {
		return fmt.Errorf("loading ingest runs: %w", err)
	}
	if format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(runs)
	}
	printIngestRunList(runs)
	return nil
}

func loadIngestRunDetails(ctx context.Context, db *store.Store, latest bool, runID int64) (*model.IngestRun, []model.IngestIssue, error) {
	var (
		run *model.IngestRun
		err error
	)
	if latest {
		run, err = db.GetLatestIngestRun(ctx)
	} else {
		run, err = db.GetIngestRun(ctx, runID)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("loading ingest run: %w", err)
	}
	if run == nil {
		return nil, nil, nil
	}
	issues, err := db.ListIngestIssues(ctx, run.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading ingest issues: %w", err)
	}
	return run, issues, nil
}

func printIngestRunList(runs []model.IngestRun) {
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

func printIngestRunDetails(run *model.IngestRun, issues []model.IngestIssue) {
	fmt.Printf("Run %d\n", run.ID)
	fmt.Printf("  Mode:      %s\n", run.Mode)
	fmt.Printf("  Status:    %s\n", run.Status)
	fmt.Printf("  Started:   %s\n", run.StartedAt)
	fmt.Printf("  Finished:  %s\n", run.FinishedAt)
	fmt.Printf("  Duration:  %dms\n", run.DurationMS)
	fmt.Printf("  Claude dir: %s\n", run.ClaudeDir)
	fmt.Printf("  Files:     %d seen, %d changed\n", run.FilesSeen, run.FilesChanged)
	fmt.Printf("  Indexed:   %d\n", run.RecordsIndexed)
	fmt.Printf("  Warnings:  %d total (%d skipped files, %d skipped rows, %d parse failures, %d unresolved links)\n",
		run.WarningCount, run.SkippedFiles, run.SkippedRows, run.ParseFailures, run.UnresolvedLinks)
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
		if issue.LineNumber > 0 {
			line = ":" + strconv.Itoa(issue.LineNumber)
		}
		fmt.Printf("  %d. [%s] %s %s%s\n", i+1, issue.Category, issue.SourceType, issue.SourcePath, line)
		fmt.Printf("     %s\n", issue.Detail)
	}
}

func trimTimestamp(ts string) string {
	if len(ts) <= 19 {
		return ts
	}
	return strings.TrimSuffix(ts[:19], "T")
}
