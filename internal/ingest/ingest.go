// Package ingest is the agent-agnostic v2 pipeline: resolve roots, discover
// sources through adapters, hash-compare for incremental re-indexing, parse
// changed sources into per-file write transactions, resolve pending links,
// and regenerate derived facets (docs/v2-plan.md §5.1, §5.6).
//
// The pipeline never sees agent-specific shapes — adapters emit canonical
// records; everything here is uniform across agents.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// Options configures one pipeline run.
type Options struct {
	// Rebuild drops all derived data first (user state survives).
	Rebuild bool
	// Prune removes rows whose source files no longer exist on disk.
	// Default is retention (deleted sources keep their data — that history
	// may exist nowhere else).
	Prune bool
	// ConfigRoots are explicit --root overrides per agent; they take
	// precedence over the agent's own env overrides and defaults.
	ConfigRoots map[canon.AgentSlug][]string
	// Getenv defaults to os.Getenv; injectable for tests.
	Getenv agent.Getenv
	// Home is the user's home directory for ~-expansion; defaults to
	// os.UserHomeDir.
	Home string
	// Progress, when non-nil, receives pipeline progress: one event per
	// root after discovery (Root=true, Total set) and one per source after
	// it is hashed. Callbacks run on the ingest goroutine — keep them
	// cheap and throttle any output on the consumer side.
	Progress func(Progress)
}

// Progress is one pipeline progress event.
type Progress struct {
	Agent canon.AgentSlug
	// Root is true for root-discovery events; Path is then the root
	// directory and Total the number of sources discovered under it.
	Root  bool
	Path  string
	Total int
	// Seen/Changed count sources across the whole run so far.
	Seen    int
	Changed int
}

// Report summarizes one pipeline run.
type Report struct {
	RunID        int64
	Status       string // ok | partial
	FilesSeen    int
	FilesChanged int
	Sessions     int
	Messages     int
	ToolCalls    int
	Artifacts    int
	History      int
	Issues       []canon.Issue
	LinksPending int
	Duration     time.Duration
}

// Runner executes the pipeline over a fixed adapter set.
type Runner struct {
	store    *db.Store
	pricer   db.Pricer
	adapters []agent.Adapter
}

// New builds a Runner. pricer prices the usage rollups regenerated after
// each run that changed data.
func New(store *db.Store, pricer db.Pricer, adapters ...agent.Adapter) *Runner {
	return &Runner{store: store, pricer: pricer, adapters: adapters}
}

type resolvedRoot struct {
	adapter agent.Adapter
	root    agent.Root
}

// Run executes one ingest pass.
func (r *Runner) Run(ctx context.Context, opts Options) (*Report, error) {
	started := time.Now()
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Home == "" {
		opts.Home, _ = os.UserHomeDir()
	}

	report := &Report{}

	roots, rootIssues := r.resolveRoots(opts)
	report.Issues = append(report.Issues, rootIssues...)

	// Reset before opening the run row — ResetDerived drops ingest_runs too.
	if opts.Rebuild {
		if err := r.store.ResetDerived(ctx); err != nil {
			return nil, err
		}
	}

	rootsJSON, _ := json.Marshal(rootSummary(roots))
	runID, err := r.store.StartRun(ctx, runMode(opts.Rebuild), string(rootsJSON))
	if err != nil {
		return nil, err
	}
	report.RunID = runID

	known, err := r.store.SourceSigs(ctx)
	if err != nil {
		return nil, r.fail(ctx, report, started, err)
	}

	for _, rr := range roots {
		if err := ctx.Err(); err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
		sources, err := rr.adapter.Discover(ctx, rr.root)
		if err != nil {
			report.Issues = append(report.Issues, canon.Issue{
				Agent: rr.adapter.Slug(), Severity: canon.SeverityError,
				Category: "discover", SourcePath: rr.root.Path,
				Detail: err.Error(),
			})
			continue
		}
		sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
		if opts.Progress != nil {
			opts.Progress(Progress{
				Agent: rr.adapter.Slug(), Root: true, Path: rr.root.Path,
				Total: len(sources), Seen: report.FilesSeen, Changed: report.FilesChanged,
			})
		}

		for _, src := range sources {
			if err := ctx.Err(); err != nil {
				return nil, r.fail(ctx, report, started, err)
			}
			report.FilesSeen++
			if err := r.ingestIfChanged(ctx, rr.adapter, src, known[src.Path], report); err != nil {
				report.Issues = append(report.Issues, canon.Issue{
					Agent: rr.adapter.Slug(), Severity: canon.SeverityError,
					Category: "parse", SourcePath: src.Path,
					Detail: err.Error(),
				})
			}
			if opts.Progress != nil {
				opts.Progress(Progress{
					Agent: rr.adapter.Slug(), Path: src.Path,
					Seen: report.FilesSeen, Changed: report.FilesChanged,
				})
			}
		}
	}

	prunedSources := 0
	if opts.Prune {
		pruned, err := r.store.PruneMissingSources(ctx, func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		})
		if err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
		if pruned > 0 {
			prunedSources = pruned
			report.FilesChanged += pruned // force facet/rollup regeneration
		}
	}

	if _, pending, err := r.store.ResolvePending(ctx); err != nil {
		return nil, r.fail(ctx, report, started, err)
	} else {
		report.LinksPending = pending
	}
	// Plans land on disk as slug-named markdown with no session id; link
	// them to the ExitPlanMode call that produced them by plan text.
	// Memories link to the sessions whose file_write/file_edit calls
	// targeted their path. Both RECONCILE the complete (artifact, session)
	// pair set every pass — a later session approving an already-linked
	// plan gains its link, and a plan rewritten under the same name loses
	// links whose evidence no longer holds.
	//
	// Gated on the pass having changed something. Reconciliation reads
	// every plan's content and every memory-writing tool call, so running
	// it unconditionally made each watch-mode debounce fire — most of
	// which change nothing relevant — pay a full pass over the corpus.
	if report.FilesChanged > 0 || prunedSources > 0 {
		if _, _, err := r.store.LinkPlanArtifacts(ctx); err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
		if _, _, err := r.store.LinkMemoryArtifacts(ctx); err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
	}
	// Workspaces and usage rollups derive from sessions and messages;
	// sidecar-only passes (artifacts, history) leave both untouched and
	// skip the rebuild. Prune can delete session rows without emitting
	// records, so it always counts as dirty. Rollups also regenerate when
	// they are empty despite indexed usage — schema migrations drop them
	// (the split columns rebuild here) and this self-heals instead of
	// leaving Usage blank until the next change.
	needRollups := report.Sessions > 0 || report.Messages > 0 || prunedSources > 0
	if !needRollups {
		var rollups, usage int
		if err := r.store.DB().QueryRowContext(ctx, `
			SELECT (SELECT COUNT(*) FROM rollup_usage_daily),
			       (SELECT COUNT(*) FROM message_usage)`).
			Scan(&rollups, &usage); err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
		needRollups = rollups == 0 && usage > 0
	}
	if needRollups {
		if err := r.store.RegenerateWorkspaces(ctx); err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
		if err := r.store.RegenerateRollups(ctx, r.pricer); err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
	}

	report.Status = "ok"
	for _, is := range report.Issues {
		if is.Severity == canon.SeverityError {
			report.Status = "partial"
			break
		}
	}
	report.Duration = time.Since(started)

	if err := r.store.InsertIssues(ctx, runID, report.Issues); err != nil {
		return nil, err
	}
	if err := r.store.FinishRun(ctx, runID, report.Status, started, r.counts(report), ""); err != nil {
		return nil, err
	}
	// Watch mode opens a run row per debounce fire, and nothing outside
	// --rebuild removed them: an all-day `ccpeek --watch` alongside an
	// active coding session grew ingest_runs and its issues without bound.
	// Trimming is best-effort — losing old telemetry must never fail a run
	// that otherwise succeeded.
	if _, err := r.store.TrimRuns(ctx, RunHistoryLimit); err != nil {
		report.Issues = append(report.Issues, canon.Issue{
			Severity: canon.SeverityWarn, Category: "store",
			Detail: fmt.Sprintf("trimming ingest run history: %v", err),
		})
	}
	return report, nil
}

// RunHistoryLimit is how many ingest runs are retained. `ccpeek ingest`
// lists ten by default, so this keeps well more than any surface shows.
const RunHistoryLimit = 100

// ingestIfChanged applies the two-tier change check for one source: a
// cheap size+mtime fingerprint first (no bytes read on match — this is
// what keeps warm startups over multi-GB histories fast), then the
// content hash as the source of truth. Hash-check failures are recorded
// as warnings here, parse failures as errors by the caller.
func (r *Runner) ingestIfChanged(ctx context.Context, a agent.Adapter, src agent.SourceRef, prior db.SourceSig, report *Report) error {
	statSig, statErr := statFingerprint(src)
	if statErr == nil && statSig != "" && prior.StatSig == statSig {
		return nil // unchanged by stat — skip without reading content
	}

	// A stored cursor lets one read serve two purposes: the full content
	// hash for change detection AND prefix verification for the append
	// path — with the running hasher handed to the adapter so the tail
	// parse seeks straight to the offset instead of re-reading the prefix
	// (previously every append read a multi-GB active log twice).
	var cursor agent.TailState
	tp, tailable := a.(agent.TailParser)
	haveCursor := false
	if tailable && prior.ParseState != "" && src.Kind == agent.SourceFile {
		if err := json.Unmarshal([]byte(prior.ParseState), &cursor); err == nil && cursor.Offset > 0 {
			haveCursor = true
		}
	}

	var hash string
	var err error
	prefixOK := false
	if haveCursor {
		var resume []byte
		hash, resume, err = hashFileWithPrefix(src.Path, cursor.Offset, cursor.PrefixHash)
		if err == nil && resume != nil {
			cursor.ResumeHash = resume
			prefixOK = true
		}
	} else {
		hash, err = hashSource(src)
	}
	if err != nil {
		report.Issues = append(report.Issues, canon.Issue{
			Agent: a.Slug(), Severity: canon.SeverityWarn,
			Category: "io", SourcePath: src.Path,
			Detail: fmt.Sprintf("hashing failed: %v", err),
		})
		return nil
	}
	if statErr != nil {
		statSig = "" // unknown stat must never match next run
	}

	if prior.ContentHash == hash {
		// Identical bytes behind a new stat (e.g. rewritten in place):
		// refresh the fingerprint so the fast path works next run.
		return r.store.TouchSourceStat(ctx, src.Path, statSig)
	}

	report.FilesChanged++

	// Cursor-capable sources try an append parse first: only the bytes
	// added since the stored cursor are decoded and only new records are
	// written. A cursor the source can't resume from (rewritten prefix,
	// missing session row) falls back to a full parse, which records a
	// fresh cursor.
	if haveCursor && prefixOK {
		err := r.ingestTail(ctx, a, tp, src, cursor, hash, statSig, report)
		if err == nil {
			return nil
		}
		if !errors.Is(err, agent.ErrTailInvalid) && !errors.Is(err, db.ErrUnknownSession) {
			return err
		}
	}
	return r.ingestSource(ctx, a, src, hash, statSig, report)
}

// hashFileWithPrefix hashes a whole file in one pass, additionally
// checking whether its first offset bytes still match wantPrefix. On a
// match it returns the marshaled state of a hasher that has consumed
// exactly those bytes, for the tail parser to resume; on a mismatch (or
// a file shorter than offset) resume is nil and only the full hash is
// meaningful.
func hashFileWithPrefix(path string, offset int64, wantPrefix string) (full string, resume []byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	prefixHasher := sha256.New()
	n, err := io.CopyN(prefixHasher, f, offset)
	if err != nil && err != io.EOF {
		return "", nil, err
	}
	prefixMatches := n == offset &&
		hex.EncodeToString(prefixHasher.Sum(nil)) == wantPrefix
	if prefixMatches {
		if m, ok := prefixHasher.(encoding.BinaryMarshaler); ok {
			resume, _ = m.MarshalBinary()
		}
	}

	// The prefix hasher continues into the full hash: Sum above did not
	// disturb its running state.
	if _, err := io.Copy(prefixHasher, f); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(prefixHasher.Sum(nil)), resume, nil
}

// ingestSource fully parses one changed source inside its own transaction.
func (r *Runner) ingestSource(ctx context.Context, a agent.Adapter, src agent.SourceRef, hash, statSig string, report *Report) error {
	w, err := r.store.BeginWrite(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()

	sink := newSink(w, a.Slug(), src.Path, hash, false)
	parseState := ""
	if tp, ok := a.(agent.TailParser); ok {
		state, err := tp.ParseTail(ctx, src, agent.TailState{}, sink)
		if err != nil {
			sink.publishIssues(report) // terminal: keep the line-level detail
			return err
		}
		parseState = marshalTailState(state)
	} else if err := a.Parse(ctx, src, sink); err != nil {
		sink.publishIssues(report)
		return err
	}
	if err := w.RecordSourceFile(src.Path, a.Slug(), hash, statSig, parseState); err != nil {
		return err
	}
	if err := w.Commit(); err != nil {
		return err
	}
	sink.commitTo(report)
	return nil
}

// ingestTail parses only the bytes appended since state, in its own
// transaction. Errors roll everything back, so a failed tail attempt
// leaves the source exactly as the last successful parse recorded it.
func (r *Runner) ingestTail(ctx context.Context, a agent.Adapter, tp agent.TailParser, src agent.SourceRef, state agent.TailState, hash, statSig string, report *Report) error {
	w, err := r.store.BeginWrite(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()

	sink := newSink(w, a.Slug(), src.Path, hash, true)
	newState, err := tp.ParseTail(ctx, src, state, sink)
	if err != nil {
		// Deliberately publishes nothing: the caller either re-parses the
		// source in full (which re-emits these diagnostics) or surfaces
		// the error itself.
		return err
	}
	if err := w.RecordSourceFile(src.Path, a.Slug(), hash, statSig, marshalTailState(newState)); err != nil {
		return err
	}
	if err := w.Commit(); err != nil {
		return err
	}
	sink.commitTo(report)
	return nil
}

// marshalTailState serializes a cursor for source_files.parse_state; a
// zero state (the source has no cursor semantics) stores as "".
func marshalTailState(state agent.TailState) string {
	if state.Offset == 0 {
		return ""
	}
	buf, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	return string(buf)
}

func (r *Runner) resolveRoots(opts Options) ([]resolvedRoot, []canon.Issue) {
	var roots []resolvedRoot
	var issues []canon.Issue
	for _, a := range r.adapters {
		resolved := agent.ResolveRoots(a.Slug(), a.RootSpec(),
			opts.ConfigRoots[a.Slug()], opts.Getenv, opts.Home)
		for _, root := range resolved {
			if _, err := os.Stat(root.Path); err != nil {
				// A missing default root just means the agent isn't
				// installed; a missing explicit root is a user mistake that
				// must surface (docs/v2-plan.md §5.1).
				if root.Origin != agent.RootFromDefault {
					issues = append(issues, canon.Issue{
						Agent: a.Slug(), Severity: canon.SeverityWarn,
						Category: "root", SourcePath: root.Path,
						Detail: fmt.Sprintf("configured root (%s) not found", root.Origin),
					})
				}
				continue
			}
			roots = append(roots, resolvedRoot{adapter: a, root: root})
		}
	}
	return roots, issues
}

func (r *Runner) counts(report *Report) db.RunCounts {
	var warns, errs int
	for _, is := range report.Issues {
		if is.Severity == canon.SeverityError {
			errs++
		} else {
			warns++
		}
	}
	return db.RunCounts{
		FilesSeen:       report.FilesSeen,
		FilesChanged:    report.FilesChanged,
		RecordsIndexed:  report.Sessions + report.Messages + report.ToolCalls + report.Artifacts + report.History,
		ParseFailures:   errs,
		UnresolvedLinks: report.LinksPending,
		WarningCount:    warns,
	}
}

// fail closes the run row and records what was collected before the
// failure.
//
// The bookkeeping deliberately runs on a DETACHED context. The most
// common way a run fails is cancellation — Ctrl-C or SIGTERM during the
// first pass over a large history — and writing the "failed" status with
// the very context that was just cancelled makes both statements fail
// instantly, leaving the row StartRun opened stuck at 'running' forever.
// A short deadline keeps a genuinely wedged database from hanging
// shutdown.
func (r *Runner) fail(ctx context.Context, report *Report, started time.Time, cause error) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = r.store.InsertIssues(closeCtx, report.RunID, report.Issues)
	_ = r.store.FinishRun(closeCtx, report.RunID, "failed", started, r.counts(report), cause.Error())
	return cause
}

func runMode(rebuild bool) string {
	if rebuild {
		return "rebuild"
	}
	return "incremental"
}

func rootSummary(roots []resolvedRoot) []map[string]string {
	out := make([]map[string]string, 0, len(roots))
	for _, rr := range roots {
		out = append(out, map[string]string{
			"agent":  string(rr.root.Agent),
			"path":   rr.root.Path,
			"origin": string(rr.root.Origin),
		})
	}
	return out
}

// statFingerprint builds the cheap first-tier change signal: size+mtime
// for files, and a digest of sorted child (name, size, mtime) for
// directories. No content is read. An equal fingerprint means "assume
// unchanged"; anything else falls through to the content hash.
func statFingerprint(src agent.SourceRef) (string, error) {
	if src.Kind != agent.SourceDir {
		fi, err := os.Stat(src.Path)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("f:%d:%d", fi.Size(), fi.ModTime().UnixNano()), nil
	}

	entries, err := os.ReadDir(src.Path)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	infos := make(map[string]os.FileInfo, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			return "", err
		}
		names = append(names, e.Name())
		infos[e.Name()] = fi
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fi := infos[name]
		fmt.Fprintf(h, "%s\x00%d\x00%d\x01", name, fi.Size(), fi.ModTime().UnixNano())
	}
	return "d:" + hex.EncodeToString(h.Sum(nil)), nil
}

// hashSource fingerprints a source: files (including per-session SQLite
// databases) by content, directories by sorted child names + contents.
func hashSource(src agent.SourceRef) (string, error) {
	switch src.Kind {
	case agent.SourceDir:
		return hashDir(src.Path)
	default:
		return hashFile(src.Path)
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		io.WriteString(h, name)
		h.Write([]byte{0})
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return "", err // unreadable children must change the verdict, not vanish (v1 bug)
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
