package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
// The v2 name derives from the data-file's own name — the default
// ccpeek.db keeps its historical ccpeek2.db sibling, and any explicit
// name gets its own <name>.v2.db — so two different --data-file paths
// in one directory never alias the same store.
func storeDBPath(dataFile string) string {
	dir := filepath.Dir(dataFile)
	base := filepath.Base(dataFile)
	if base == "ccpeek.db" {
		return filepath.Join(dir, "ccpeek2.db")
	}
	ext := filepath.Ext(base)
	return filepath.Join(dir, strings.TrimSuffix(base, ext)+".v2"+ext)
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
	// cleared meta makes the full bootstrap re-run. The v1 import keys on
	// its own v1_import_state meta so a failure retries past the first
	// run.
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

		maybeImportV1(ctx, store, dataFile, logw)
		if firstRun {
			_ = store.SetMeta(ctx, "migrated_at", time.Now().UTC().Format(time.RFC3339))
		}
		return nil
	}
	return eng, bootstrap, nil
}

// v1_import_state meta values. Success and no-legacy-db both stop the
// bootstrap from probing again; anything else — unset, or "failed" —
// makes the next indexing start retry.
const (
	v1ImportSuccess    = "success"
	v1ImportFailed     = "failed"
	v1ImportNoLegacyDB = "no-legacy-db"
)

// maybeImportV1 runs the v1 import unless a previous attempt succeeded
// or established there is no legacy database. The state is deliberately
// separate from migrated_at (the bootstrap marker): a failed import
// must not look done just because the engine came up — it is retried on
// every indexing start until it succeeds, and databases stamped before
// this split get one idempotent re-import. Failure never bricks the
// engine; it is recorded for /api/v1/health and the UI, and
// `ccpeek migrate` re-runs the import with a non-zero exit on error.
func maybeImportV1(ctx context.Context, store *db.Store, dataFile string, logw io.Writer) {
	state, _, err := store.GetMeta(ctx, "v1_import_state")
	if err == nil && (state == v1ImportSuccess || state == v1ImportNoLegacyDB) {
		return
	}
	if _, err := os.Stat(dataFile); err != nil {
		if os.IsNotExist(err) {
			// A previous failed attempt may have recorded an error; the
			// legacy file being gone resolves it, so health must not keep
			// showing a stale message next to the terminal state.
			_ = store.SetMeta(ctx, "v1_import_state", v1ImportNoLegacyDB)
			_ = store.SetMeta(ctx, "v1_import_error", "")
			return
		}
		// Permission or I/O trouble reaching the legacy file is a failed
		// attempt to retry, not proof there is nothing to import.
		_ = store.SetMeta(ctx, "v1_import_state", v1ImportFailed)
		_ = store.SetMeta(ctx, "v1_import_error", err.Error())
		fmt.Fprintf(logw, "WARNING: v1 import failed (kept visible in /api/v1/health; retried next start; `ccpeek migrate` re-runs it loudly): %v\n", err)
		return
	}
	if _, err := runV1Import(ctx, store, dataFile, logw); err != nil {
		fmt.Fprintf(logw, "WARNING: v1 import failed (kept visible in /api/v1/health; retried next start; `ccpeek migrate` re-runs it loudly): %v\n", err)
	}
}

// runV1Import executes the import and records its outcome in meta:
// success stamps v1_imported_at and clears v1_import_error; failure
// records the error without a stamp. The v1 database is opened
// read-only either way.
func runV1Import(ctx context.Context, store *db.Store, dataFile string, logw io.Writer) (*migrate.Report, error) {
	fmt.Fprintf(logw, "Importing v1 data from %s\n", dataFile)
	mreport, err := migrate.ImportV1(ctx, store, dataFile)
	if err != nil {
		_ = store.SetMeta(ctx, "v1_import_state", v1ImportFailed)
		_ = store.SetMeta(ctx, "v1_import_error", err.Error())
		return nil, err
	}
	fmt.Fprintf(logw, "Imported from v1: %d orphaned sessions (%d messages, %d tool calls), %d artifacts, %d history entries, %d ignore flags\n",
		mreport.OrphanSessions, mreport.OrphanMessages,
		mreport.OrphanToolCalls, mreport.OrphanArtifacts,
		mreport.HistoryEntries, mreport.IgnoreFlags)
	b, _ := json.Marshal(mreport)
	_ = store.SetMeta(ctx, "v1_import_report", string(b))
	_ = store.SetMeta(ctx, "v1_import_state", v1ImportSuccess)
	_ = store.SetMeta(ctx, "v1_imported_at", time.Now().UTC().Format(time.RFC3339))
	_ = store.SetMeta(ctx, "v1_import_error", "")
	return mreport, nil
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
