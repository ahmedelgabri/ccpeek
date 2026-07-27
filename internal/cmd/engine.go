package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/codex"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/cursor"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/opencode"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/pi"
	"github.com/ahmedelgabri/ccpeek/internal/agent"
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

// indexSkip is a caller's "do not re-index first" decision together with
// the flag that expressed it. The flag name travels with the bool
// because the same engine serves commands that spell the decision
// differently — `ccpeek --skip-index`, `ccpeek query --no-index`,
// `ccpeek scan --no-index` — and two commands (export, ingest) never
// index at all and have no flag to name. A message about the skip that
// names a flag the user did not pass sends them looking for a bug in
// their own command line.
type indexSkip struct {
	skip bool
	// flag is the user-facing spelling, "--no-index" style; empty when
	// the command decided on its own.
	flag string
}

// indexNow is the decision every command that must read fresh data
// makes: run the incremental pass first.
func indexNow() indexSkip { return indexSkip{} }

// neverIndex is for commands that read the index as-is by their own
// contract, with no flag to blame (`ccpeek export`, `ccpeek ingest`).
func neverIndex() indexSkip { return indexSkip{skip: true} }

// skipFlag reads a boolean opt-out flag and remembers its name.
func skipFlag(cmd *cobra.Command, name string) indexSkip {
	skip, _ := cmd.Flags().GetBool(name)
	return indexSkip{skip: skip, flag: "--" + name}
}

// openEngineDeferred opens the store and services WITHOUT ingesting, and
// returns the bootstrap step as a closure. bootstrap is nil when nothing
// needs to run (a skip on an existing database); otherwise the caller
// decides whether to run it synchronously (CLI commands) or in the
// background (the serving path, so the UI is reachable immediately).
//
// First-run contract (docs/v2-plan.md §8.1): when the database does not
// exist yet, bootstrap runs a full ingest of all detected agent roots
// and THEN — if a legacy v1 database is present — imports the rows that
// ingest cannot re-derive, plus user state. Zero flags, zero prompts.
// tweak, when non-nil, adjusts the pipeline options
// (rebuild/prune/progress) before the run.
func openEngineDeferred(ctx context.Context, cmd *cobra.Command, skip indexSkip, logw io.Writer, tweak ...func(*ingest.Options)) (*engine, func(context.Context) error, error) {
	dataFile, err := resolveDataFile(cmd)
	if err != nil {
		return nil, nil, err
	}

	// Flag validation happens BEFORE anything is opened or created, and on
	// EVERY path — the skipping ones included. A typo in --root or a
	// --claude-dir that does not exist is the same user mistake whether or
	// not this run indexes; validating only where the pass runs meant
	// `ccpeek query sessions --root gemini=/tmp/x --no-index` exited 0 with
	// the override silently dropped, while the same command without
	// --no-index failed with "unknown agent".
	opts, err := ingestOptions(cmd)
	if err != nil {
		return nil, nil, err
	}
	if err := checkExplicitRoots(cmd); err != nil {
		return nil, nil, err
	}

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
	runner := ingest.New(store, table, launchAdapters()...)
	eng := &engine{
		store:   store,
		pricing: table,
		query:   query.New(store, table),
		runner:  runner,
	}

	// bootstrap owns the v1 import — it has to run AFTER the ingest pass,
	// and off the path that binds the port. bootstrap is nil on THIS path,
	// so it is also the only place the documented "retried on every start
	// until it succeeds" contract can hold for anyone who runs
	// --skip-index. The import is keyed on its own meta and is a no-op
	// once it has succeeded (or established there is no legacy database),
	// so the usual cost is one meta read.
	if skip.skip && !firstRun {
		maybeImportV1(ctx, store, dataFile, logw)
		return eng, nil, nil
	}
	if skip.skip && firstRun {
		// Name the flag the user actually passed, or nothing at all when
		// the command skipped on its own — "--skip-index ignored" printed
		// by `ccpeek query --no-index` describes a flag that command does
		// not even have.
		if skip.flag != "" {
			fmt.Fprintf(logw, "%s ignored: %s does not exist yet, so there is nothing to read — indexing now\n", skip.flag, storePath)
		} else {
			fmt.Fprintf(logw, "%s does not exist yet — indexing now\n", storePath)
		}
	}

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

		// AFTER the pass, never before it. The importer only adds what v2
		// does not already hold, and every one of those checks answers
		// "no" against an empty first-run store: importing first turns the
		// whole v1 database into orphans, doubling the work and — for
		// retained history rows — handing them to the live history.jsonl
		// parse to delete. Running here also keeps the import off the path
		// that binds the port (root.go and mcp.go run bootstrap in the
		// background) and picks up a legacy database that appeared since
		// the engine opened.
		if rep := maybeImportV1(ctx, store, dataFile, logw); rep != nil && rep.OrphanSessions > 0 {
			// The pass regenerated the derived facets before these sessions
			// existed; without a rebuild they stay outside every workspace
			// until some later pass happens to change something.
			if err := store.RegenerateWorkspaces(ctx); err != nil {
				fmt.Fprintf(logw, "WARNING: regenerating workspaces after the v1 import: %v\n", err)
			}
		}
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
// or established there is no legacy database, and reports what came over
// (nil when nothing ran). The state is deliberately separate from
// migrated_at (the bootstrap marker): a failed import must not look done
// just because the engine came up — it is retried on every start until
// it succeeds, and databases stamped before this split get one
// idempotent re-import. Failure never bricks the engine; it is recorded
// for /api/v1/health and the UI, and `ccpeek migrate` re-runs the import
// with a non-zero exit on error.
func maybeImportV1(ctx context.Context, store *db.Store, dataFile string, logw io.Writer) *migrate.Report {
	state, _, err := store.GetMeta(ctx, "v1_import_state")
	if err == nil && (state == v1ImportSuccess || state == v1ImportNoLegacyDB) {
		return nil
	}
	present, err := checkV1Source(ctx, store, dataFile)
	if err != nil {
		// A v2 store at --data-file is a misconfiguration, not a transient
		// failure: retrying it every start can only fail again. checkV1Source
		// has already recorded the terminal state, so this says it once.
		if errors.Is(err, errDataFileIsV2) {
			fmt.Fprintf(logw, "WARNING: %v\n", err)
			return nil
		}
		fmt.Fprintf(logw, "WARNING: v1 import failed (kept visible in /api/v1/health; retried next start; `ccpeek migrate` re-runs it loudly): %v\n", err)
		return nil
	}
	if !present {
		return nil
	}
	rep, err := runV1Import(ctx, store, dataFile, logw)
	if err != nil {
		fmt.Fprintf(logw, "WARNING: v1 import failed (kept visible in /api/v1/health; retried next start; `ccpeek migrate` re-runs it loudly): %v\n", err)
		return nil
	}
	return rep
}

// checkV1Source records the terminal meta state for a legacy database
// that cannot be read, and reports whether one is there to import.
//
// Both entry points — the bootstrap's warn-and-retry path and
// `ccpeek migrate`'s exit-non-zero one — need exactly this state machine
// and differ only in what they do afterwards. Written out twice, a new
// terminal state added to one would leave the other reporting something
// /api/v1/ready does not recognize.
func checkV1Source(ctx context.Context, store *db.Store, dataFile string) (present bool, err error) {
	if _, err := os.Stat(dataFile); err != nil {
		if os.IsNotExist(err) {
			// A previous failed attempt may have recorded an error; the
			// legacy file being gone resolves it, so health must not keep
			// showing a stale message next to the terminal state.
			_ = store.SetMeta(ctx, "v1_import_state", v1ImportNoLegacyDB)
			_ = store.SetMeta(ctx, "v1_import_error", "")
			return false, nil
		}
		// Permission or I/O trouble reaching the legacy file is a failed
		// attempt to retry, not proof there is nothing to import.
		_ = store.SetMeta(ctx, "v1_import_state", v1ImportFailed)
		_ = store.SetMeta(ctx, "v1_import_error", err.Error())
		return false, fmt.Errorf("checking v1 database: %w", err)
	}
	// --data-file names the LEGACY database; pointing it at a v2 store is
	// the trap the flag's own help used to set. It derives a second v2
	// store beside the real one (<name>.v2.db) and then hands the v2
	// database to the v1 importer, which finds v1's sessions/messages
	// table NAMES and fails on their columns — every start, forever,
	// because the failure state is the retrying one. Terminal state here:
	// no amount of retrying turns a v2 store into a v1 one.
	if isV2Store(ctx, dataFile) {
		msg := fmt.Sprintf("%s is a ccpeek v2 index, not a legacy v1 database — --data-file names the v1 database and the v2 index lives at the derived sibling %s; no v1 import will be attempted",
			dataFile, storeDBPath(dataFile))
		_ = store.SetMeta(ctx, "v1_import_state", v1ImportNoLegacyDB)
		_ = store.SetMeta(ctx, "v1_import_error", msg)
		return false, fmt.Errorf("%w: %s", errDataFileIsV2, msg)
	}
	return true, nil
}

// errDataFileIsV2 marks the misconfiguration above so the bootstrap can
// report it once and move on while `ccpeek migrate` still exits non-zero.
var errDataFileIsV2 = errors.New("--data-file points at a v2 index")

// isV2Store reports whether path holds a ccpeek v2 index. The
// discriminator is the SCHEMA, not the version: both generations carry a
// meta table with a schema_version and their numbering overlaps (v1
// reached 14, v2 is at 13), so the number says nothing. `agents` and
// `session_workspaces` are v2's multi-agent spine, which the
// Claude-only v1 never had in any vintage. Unreadable, or not a database
// at all, answers false — those are the ordinary import path's errors to
// report, and it retries them because they can change.
func isV2Store(ctx context.Context, path string) bool {
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return false
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name IN ('agents', 'session_workspaces')`).Scan(&n); err != nil {
		return false
	}
	return n == 2
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
func openEngine(ctx context.Context, cmd *cobra.Command, skip indexSkip, logw io.Writer, tweak ...func(*ingest.Options)) (*engine, error) {
	eng, bootstrap, err := openEngineDeferred(ctx, cmd, skip, logw, tweak...)
	if err != nil {
		return nil, err
	}
	if bootstrap != nil {
		if err := runBootstrap(ctx, eng, bootstrap); err != nil {
			eng.Close()
			return nil, err
		}
	}
	return eng, nil
}

// bootstrap_state meta values, mirroring the v1 import's: the outcome of
// the last bootstrap pass, so a failure is durable state rather than a
// stderr line that scrolled away.
const (
	metaBootstrapState = "bootstrap_state"
	metaBootstrapError = "bootstrap_error"

	bootstrapSuccess = "success"
	bootstrapFailed  = "failed"
)

// runBootstrap runs the bootstrap pass and records its outcome in meta.
//
// The serving path used to flip /api/v1/ready to 200 whether the pass
// succeeded or not, while a failed v1 import deliberately HELD readiness
// at 503 — the same "partial history must not read as ready" argument,
// applied to only one of the two ways history goes missing. Recording the
// outcome here is what lets the serving path hold readiness for a failed
// pass with a reason attached; the caller decides what to do with the
// error, because a CLI command reports it by exiting non-zero instead.
//
// Warnings inside a SUCCESSFUL pass (one agent's root unreadable, a
// source that would not parse) are not failures: the pass returns nil,
// this records success, and readiness proceeds. Only a pass that could
// not complete records failure.
//
// A canceled pass records nothing: shutdown is not an outcome, and the
// writes would fail against the canceled context anyway.
func runBootstrap(ctx context.Context, eng *engine, bootstrap func(context.Context) error) error {
	err := bootstrap(ctx)
	if ctx.Err() != nil {
		return err
	}
	if err != nil {
		_ = eng.store.SetMeta(ctx, metaBootstrapState, bootstrapFailed)
		_ = eng.store.SetMeta(ctx, metaBootstrapError, err.Error())
		return err
	}
	_ = eng.store.SetMeta(ctx, metaBootstrapState, bootstrapSuccess)
	_ = eng.store.SetMeta(ctx, metaBootstrapError, "")
	return nil
}

func (e *engine) Close() error { return e.store.Close() }

// ingestOptions maps CLI flags to pipeline options.
//
// ingest.Options.ConfigRoots is per-agent and agent.ResolveRoots applies it
// uniformly to all five adapters — but the only flag feeding it was
// --claude-dir, so users of the other four could relocate their data only
// through each agent's own environment variable. --root <agent>=<path>
// (docs/v2-plan.md §5.4) is the general mechanism the layer below already
// implemented; it repeats, and --claude-dir remains its Claude-specific
// alias for v1 CLI compatibility (§8.2).
//
// Overrides are only passed when explicitly set, so the environment
// variables still apply otherwise.
func ingestOptions(cmd *cobra.Command) (ingest.Options, error) {
	opts := ingest.Options{}
	roots := map[canon.AgentSlug][]string{}

	if cmd.Flags().Changed("claude-dir") {
		claudeDir, _ := cmd.Flags().GetString("claude-dir")
		roots[claude.Slug] = []string{claudeDir}
	}
	specs, _ := cmd.Flags().GetStringArray("root")
	for _, spec := range specs {
		slug, path, ok := strings.Cut(spec, "=")
		if !ok || slug == "" || path == "" {
			return opts, fmt.Errorf("--root %q: want <agent>=<path>, e.g. --root claude-code=~/backup/claude", spec)
		}
		if !knownAgent(canon.AgentSlug(slug)) {
			return opts, fmt.Errorf("--root %q: unknown agent %q (want one of %s)",
				spec, slug, strings.Join(agentSlugs(), ", "))
		}
		roots[canon.AgentSlug(slug)] = append(roots[canon.AgentSlug(slug)], path)
	}

	if len(roots) > 0 {
		opts.ConfigRoots = roots
	}
	return opts, nil
}

// checkExplicitRoots fails on an explicitly-given agent root that does
// not exist. An explicit path is a claim about where the data IS, so a
// typo must be loud; a DEFAULT root that is missing only means the agent
// is not installed, which is normal on every machine and stays silent.
//
// It lives beside ingestOptions rather than inside it because `ccpeek
// doctor` calls that one to REPORT missing roots — erroring there would
// break the command whose whole job is saying which roots are missing.
// Every path that opens the store runs this instead, so the root command
// and the subcommands agree: `ccpeek --claude-dir /gone` and `ccpeek
// query sessions --claude-dir /gone` both fail, and both say why.
func checkExplicitRoots(cmd *cobra.Command) error {
	if cmd.Flags().Changed("claude-dir") {
		dir, _ := cmd.Flags().GetString("claude-dir")
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return fmt.Errorf("claude data directory not found: %s", dir)
		}
	}
	specs, _ := cmd.Flags().GetStringArray("root")
	for _, spec := range specs {
		slug, path, ok := strings.Cut(spec, "=")
		if !ok || path == "" {
			continue // ingestOptions reports the malformed spec itself
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return fmt.Errorf("--root %s: data directory not found: %s", slug, path)
		}
	}
	return nil
}

// resolveDataFile returns the --data-file value, refusing to guess when
// there is nothing to derive the default from. dataDir used to fall back
// to the OS temp directory when the home directory could not be
// resolved, which put an ARCHIVE — the thing ccpeek exists to keep —
// somewhere the next reboot may wipe, silently and by default. Refusing
// to start costs the user one environment variable; the fallback cost
// them their index without telling them.
func resolveDataFile(cmd *cobra.Command) (string, error) {
	if v, _ := cmd.Flags().GetString("data-file"); v != "" {
		return v, nil
	}
	if dataDirErr != nil {
		return "", dataDirErr
	}
	return "", fmt.Errorf("--data-file is empty: pass the database path, or unset the flag and let ccpeek use $XDG_DATA_HOME/ccpeek")
}

// launchAdapters is THE launch set (docs/v2-plan.md §6): Claude Code, Pi,
// Codex, OpenCode, Cursor. The pipeline, `ccpeek doctor`, and --root's
// validator all derive from it — listing them separately meant a sixth
// adapter needed three coordinated edits, and missing the validator's
// would reject `--root sixth=/path` for an agent ingest handles fine.
func launchAdapters() []agent.Adapter {
	return []agent.Adapter{
		claude.New(), pi.New(), codex.New(), opencode.New(), cursor.New(),
	}
}

func knownAgent(slug canon.AgentSlug) bool {
	for _, a := range launchAdapters() {
		if a.Slug() == slug {
			return true
		}
	}
	return false
}

func agentSlugs() []string {
	out := make([]string, 0, 5)
	for _, a := range launchAdapters() {
		out = append(out, string(a.Slug()))
	}
	slices.Sort(out)
	return out
}
