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

// v2Engine bundles the open v2 store with its services.
type v2Engine struct {
	store   *db.Store
	pricing *pricing.Table
	query   *query.Service
	runner  *ingest.Runner
}

// v2DBPath places the v2 database next to the v1 file (docs/v2-plan.md
// §8.2: distinct name so neither binary can corrupt the other's store).
func v2DBPath(v1DataFile string) string {
	return filepath.Join(filepath.Dir(v1DataFile), "ccpeek2.db")
}

// openV2Engine opens (creating + auto-migrating if needed) the v2 engine.
//
// First-run contract (docs/v2-plan.md §8.1): when the v2 database does not
// exist yet, a full ingest of all detected agent roots runs, and — if a v1
// database is present — its orphaned rows and user state are imported.
// Zero flags, zero prompts. Subsequent opens run an incremental ingest
// unless skipIndex is true.
func openV2Engine(ctx context.Context, cmd *cobra.Command, skipIndex bool, logw io.Writer) (*v2Engine, error) {
	dataFile, _ := cmd.Flags().GetString("data-file")

	v2Path := v2DBPath(dataFile)
	firstRun := false
	if _, err := os.Stat(v2Path); os.IsNotExist(err) {
		firstRun = true
	}

	store, err := db.Open(ctx, v2Path)
	if err != nil {
		return nil, err
	}

	table, err := pricing.Embedded()
	if err != nil {
		store.Close()
		return nil, err
	}
	// The full launch set (docs/v2-plan.md §6): Claude Code, Pi, Codex,
	// OpenCode, Cursor.
	runner := ingest.New(store, table,
		claude.New(), pi.New(), codex.New(), opencode.New(), cursor.New())
	eng := &v2Engine{
		store:   store,
		pricing: table,
		query:   query.New(store, table),
		runner:  runner,
	}

	if skipIndex && !firstRun {
		return eng, nil
	}

	opts := v2IngestOptions(cmd)
	if firstRun {
		fmt.Fprintf(logw, "First v2 start: building %s\n", v2Path)
	}
	report, err := eng.runner.Run(ctx, opts)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("indexing: %w", err)
	}
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
	return eng, nil
}

func (e *v2Engine) Close() error { return e.store.Close() }

// v2IngestOptions maps CLI flags to pipeline options. --claude-dir keeps
// working as the Claude adapter's root override (§8.2 CLI compatibility);
// it is only passed when explicitly set so CLAUDE_CONFIG_DIR still applies
// otherwise.
func v2IngestOptions(cmd *cobra.Command) ingest.Options {
	opts := ingest.Options{}
	if cmd.Flags().Changed("claude-dir") {
		claudeDir, _ := cmd.Flags().GetString("claude-dir")
		opts.ConfigRoots = map[canon.AgentSlug][]string{
			claude.Slug: {claudeDir},
		}
	}
	return opts
}
