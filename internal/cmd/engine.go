package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/codex"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/cursor"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/opencode"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/pi"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/migrate"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
	"github.com/ahmedelgabri/ccpeek/internal/query"
	"github.com/spf13/cobra"
)

// engine bundles the open store with its services.
type engine struct {
	store   *db.Store
	pricing *pricing.Table
	query   *query.Service
	runner  *ingest.Runner
	// report is the bootstrap ingest run's result (nil when skipped).
	report *ingest.Report
}

// storeDBPath places the store next to the legacy v1 file (docs/v2-plan.md
// §8.2: a distinct name so neither binary can corrupt the other's store).
func storeDBPath(dataFile string) string {
	return filepath.Join(filepath.Dir(dataFile), "ccpeek2.db")
}

// openEngineDeferred opens the store and services WITHOUT ingesting, and
// returns the bootstrap step as a closure. bootstrap is nil when nothing
// needs to run (skipIndex on an existing database); otherwise the caller
// decides whether to run it synchronously (CLI commands) or in the
// background (the serving path, so the UI is reachable immediately).
//
// First-run contract (docs/v2-plan.md §8.1): when the database does not
// exist yet, bootstrap runs a full ingest of all detected agent roots and
// — if a legacy v1 database is present — imports its orphaned rows and
// user state. Zero flags, zero prompts. tweak, when non-nil, adjusts the
// pipeline options (rebuild/prune/progress) before the run.
func openEngineDeferred(ctx context.Context, cmd *cobra.Command, skipIndex bool, logw io.Writer, tweak ...func(*ingest.Options)) (*engine, func(context.Context) error, error) {
	dataFile, _ := cmd.Flags().GetString("data-file")

	storePath := storeDBPath(dataFile)
	store, err := db.Open(ctx, storePath)
	if err != nil {
		return nil, nil, err
	}

	// First run is keyed on the migrated_at meta, not file existence: a
	// pre-release schema bump rebuilds the database in Open, and the
	// cleared meta makes the full bootstrap (including the v1 import)
	// re-run.
	_, initialized, err := store.GetMeta(ctx, "migrated_at")
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	firstRun := !initialized

	table, err := pricing.Embedded()
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	// The full launch set (docs/v2-plan.md §6): Claude Code, Pi, Codex,
	// OpenCode, Cursor.
	runner := ingest.New(store, table,
		claude.New(), pi.New(), codex.New(), opencode.New(), cursor.New())
	eng := &engine{
		store:   store,
		pricing: table,
		query:   query.New(store, table),
		runner:  runner,
	}

	if skipIndex && !firstRun {
		return eng, nil, nil
	}

	opts := ingestOptions(cmd)
	for _, t := range tweak {
		if t != nil {
			t(&opts)
		}
	}

	bootstrap := func(ctx context.Context) error {
		if firstRun {
			fmt.Fprintf(logw, "First start: building %s\n", storePath)
		}
		report, err := eng.runner.Run(ctx, opts)
		if err != nil {
			return fmt.Errorf("indexing: %w", err)
		}
		eng.report = report
		if report.FilesChanged > 0 {
			fmt.Fprintf(logw, "Indexed %d changed source(s): %d sessions, %d messages, %d artifacts (%s)\n",
				report.FilesChanged, report.Sessions, report.Messages,
				report.Artifacts, report.Duration.Round(time.Millisecond))
		}

		if firstRun {
			if _, err := os.Stat(dataFile); err == nil {
				fmt.Fprintf(logw, "Importing v1 data from %s\n", dataFile)
				mreport, err := migrate.ImportV1(ctx, store, dataFile)
				if err != nil {
					// Import failure must not brick the new engine; the v1 DB is
					// untouched and the import can be re-run with `ccpeek migrate`.
					fmt.Fprintf(logw, "WARNING: v1 import failed (re-run with `ccpeek migrate`): %v\n", err)
				} else {
					fmt.Fprintf(logw, "Imported from v1: %d orphaned sessions (%d messages), %d artifacts, %d ignore flags\n",
						mreport.OrphanSessions, mreport.OrphanMessages,
						mreport.OrphanArtifacts, mreport.IgnoreFlags)
					b, _ := json.Marshal(mreport)
					_ = store.SetMeta(ctx, "v1_import_report", string(b))
				}
			}
			_ = store.SetMeta(ctx, "migrated_at", time.Now().UTC().Format(time.RFC3339))
		}
		return nil
	}
	return eng, bootstrap, nil
}

// openEngine opens the engine and runs any needed bootstrap ingest
// synchronously — the right shape for CLI commands that answer from the
// index. The serving path uses openEngineDeferred instead.
func openEngine(ctx context.Context, cmd *cobra.Command, skipIndex bool, logw io.Writer, tweak ...func(*ingest.Options)) (*engine, error) {
	eng, bootstrap, err := openEngineDeferred(ctx, cmd, skipIndex, logw, tweak...)
	if err != nil {
		return nil, err
	}
	if bootstrap != nil {
		if err := bootstrap(ctx); err != nil {
			eng.Close()
			return nil, err
		}
	}
	return eng, nil
}

func (e *engine) Close() error { return e.store.Close() }

// ingestOptions maps CLI flags to pipeline options. --claude-dir keeps
// working as the Claude adapter's root override (§8.2 CLI compatibility);
// it is only passed when explicitly set so CLAUDE_CONFIG_DIR still applies
// otherwise.
func ingestOptions(cmd *cobra.Command) ingest.Options {
	opts := ingest.Options{}
	if cmd.Flags().Changed("claude-dir") {
		claudeDir, _ := cmd.Flags().GetString("claude-dir")
		opts.ConfigRoots = map[canon.AgentSlug][]string{
			claude.Slug: {claudeDir},
		}
	}
	return opts
}
