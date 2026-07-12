package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ahmedelgabri/ccpeek/internal/secrets"
	"github.com/spf13/cobra"
)

// runScanV2 scans the v2 index — every agent's transcripts and artifacts,
// not just Claude's. Exit code 2 on non-ignored findings, matching v1.
func runScanV2(cmd *cobra.Command) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	format, _ := cmd.Flags().GetString("format")
	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported format %q: use text or json", format)
	}

	// Scan the index as-is (first run still bootstraps it); `ccpeek` is the
	// indexing entry point.
	eng, err := openV2Engine(ctx, cmd, true, os.Stderr)
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
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{
			"schema": payloadSchema,
			"data":   findings,
		}); err != nil {
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
		os.Exit(2)
	}
	return nil
}
