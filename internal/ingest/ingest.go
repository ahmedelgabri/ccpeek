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
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// Options configures one pipeline run.
type Options struct {
	// Rebuild reparses available sources and preserves retained archive records.
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
	// OnPass, when non-nil, brackets each pass: true when Run starts, false
	// when it returns — including the passes Watch drives itself, which a
	// caller outside this package cannot otherwise see. Watch announces
	// only the END of a pass that CHANGED something, so a consumer wanting
	// "is a pass running" had to INFER it from progress events and a
	// timeout; this states it. Like Progress, it runs on the ingest
	// goroutine: keep it cheap.
	OnPass func(running bool)
	// WatchPass runs after each successful watch pass, including unchanged
	// passes, so interrupted downstream scans can retry without new files.
	WatchPass func(*Report)
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
	// The bracket wraps the WHOLE pass, every return included — which is
	// why it lives here rather than at the call sites: Watch runs passes
	// nobody outside this package calls.
	if opts.OnPass != nil {
		opts.OnPass(true)
		defer opts.OnPass(false)
	}
	lockedCtx, unlock, err := r.store.LockMaintenance(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ctx = lockedCtx
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

	// Back up the archive and invalidate parser fingerprints before the pass.
	if opts.Rebuild {
		if err := r.store.PrepareRebuild(ctx); err != nil {
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
			// An unavailable root is not evidence that its archive was deleted.
			// Only prune sources under roots that are still accessible now.
			covered := false
			for _, rr := range roots {
				if rr.root.Agent != known[path].Agent {
					continue
				}
				rel, err := filepath.Rel(rr.root.Path, path)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue
				}
				if info, err := os.Stat(rr.root.Path); err == nil && info.IsDir() {
					covered = true
					break
				}
			}
			if !covered {
				return true
			}
			_, err := os.Stat(path)
			if err != nil && !os.IsNotExist(err) {
				report.Issues = append(report.Issues, canon.Issue{Severity: canon.SeverityError, Category: "prune", SourcePath: path, Detail: err.Error()})
			}
			return !os.IsNotExist(err)
		})
		if err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
		if pruned > 0 {
			prunedSources = pruned
			report.FilesChanged += pruned // force facet/rollup regeneration
		}
	}

	// Parked links are always retried — a pass that changed nothing still
	// reports how many are outstanding — but only a pass that INGESTED
	// something is allowed to age them towards being dropped. Prune counts
	// as changed (it is folded into FilesChanged above): it can delete the
	// very endpoint a link was waiting for. Watch mode fires a pass per
	// debounce, so ageing on every call made the attempt limit a measure of
	// elapsed time rather than of ingest activity.
	changed := report.FilesChanged > 0
	if _, pending, err := r.store.ResolvePending(ctx, changed); err != nil {
		return nil, r.fail(ctx, report, started, err)
	} else {
		report.LinksPending = pending
	}
	// Artifacts whose provenance lives in their CONTENT — a plan matched by
	// its markdown, a memory by the path a tool call wrote — are linked by
	// rules each adapter declares (agent.LinkRuler). The rules RECONCILE
	// the complete (artifact, session) pair set every pass: a later session
	// producing an already-linked artifact gains its link, and one
	// rewritten under the same name loses the links whose evidence no
	// longer holds.
	//
	// Gated on the pass having changed something. A rule reads every
	// artifact of its kind and every call that could have produced one, so
	// running them unconditionally made each watch-mode debounce — most of
	// which change nothing relevant — pay a full pass over the corpus.
	dirty, err := r.store.DerivedDirty(ctx)
	if err != nil {
		return nil, r.fail(ctx, report, started, err)
	}
	if report.FilesChanged > 0 || dirty {
		if _, _, err := r.store.ResolveArtifactLinks(ctx, r.linkRules()); err != nil {
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
	needRollups := dirty || report.Sessions > 0 || report.Messages > 0 || prunedSources > 0
	if !needRollups {
		var err error
		needRollups, err = r.store.RollupsNeedRegeneration(ctx, r.pricer)
		if err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
	}
	if needRollups {
		if err := r.store.RefreshWorkspaces(ctx); err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
		if err := r.store.RefreshRollups(ctx, r.pricer); err != nil {
			return nil, r.fail(ctx, report, started, err)
		}
	}

	if err := r.store.SetMeta(ctx, "derived_dirty", "0"); err != nil {
		return nil, r.fail(ctx, report, started, err)
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
	parseVersion := 1
	if v, ok := a.(agent.ParseVersioner); ok && v.ParseVersion() > 0 {
		parseVersion = v.ParseVersion()
	}
	versionChanged := prior.ContentHash != "" && prior.ParseVersion != parseVersion
	statSig, statErr := statFingerprint(src)
	if !versionChanged && statErr == nil && statSig != "" && prior.StatSig == statSig {
		return nil // unchanged by stat and parser — skip without reading content
	}

	// A stored cursor lets one read serve two purposes: the full content
	// hash for change detection AND prefix verification for the append
	// path — with the running hasher handed to the adapter so the tail
	// parse seeks straight to the offset instead of re-reading the prefix
	// (previously every append read a multi-GB active log twice).
	var cursor agent.TailState
	tp, tailable := a.(agent.TailParser)
	haveCursor := false
	if !versionChanged && tailable && prior.ParseState != "" && src.Kind == agent.SourceFile {
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

	if !versionChanged && prior.ContentHash == hash {
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
	return r.ingestSource(ctx, a, src, hash, statSig, parseVersion, report)
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
func (r *Runner) ingestSource(ctx context.Context, a agent.Adapter, src agent.SourceRef, hash, statSig string, parseVersion int, report *Report) error {
	w, err := r.store.BeginWrite(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()

	if err := w.ClearHistorySource(a.Slug(), src.Path); err != nil {
		return err
	}
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
	if err := sink.reconcile(); err != nil {
		return err
	}
	if err := w.RecordSourceFile(src.Path, a.Slug(), hash, statSig, parseState, parseVersion); err != nil {
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
	parseVersion := 1
	if v, ok := a.(agent.ParseVersioner); ok && v.ParseVersion() > 0 {
		parseVersion = v.ParseVersion()
	}
	if err := w.RecordSourceFile(src.Path, a.Slug(), hash, statSig,
		marshalTailState(newState), parseVersion); err != nil {
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
				if root.Origin != agent.RootFromDefault || !os.IsNotExist(err) {
					severity := canon.SeverityWarn
					if !os.IsNotExist(err) {
						severity = canon.SeverityError
					}
					issues = append(issues, canon.Issue{
						Agent: a.Slug(), Severity: severity,
						Category: "root", SourcePath: root.Path,
						Detail: fmt.Sprintf("root (%s) unavailable: %v", root.Origin, err),
					})
				}
				continue
			}
			roots = append(roots, resolvedRoot{adapter: a, root: root})
		}
	}
	return roots, issues
}

// linkRules collects the provenance rules from every adapter that
// declares them (agent.LinkRuler is optional — most agents keep no
// content-linked artifacts).
func (r *Runner) linkRules() []canon.LinkRule {
	var rules []canon.LinkRule
	for _, a := range r.adapters {
		if lr, ok := a.(agent.LinkRuler); ok {
			rules = append(rules, lr.LinkRules()...)
		}
	}
	return rules
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
//
// A source's CompanionPaths are folded in, so a session split across two
// trees still re-indexes when either side moves.
func statFingerprint(src agent.SourceRef) (string, error) {
	sig, err := statSignature(src.Path, src.Kind == agent.SourceDir)
	if err != nil {
		return "", err
	}
	folded, err := foldCompanions(sig, src.CompanionPaths, statSignature)
	if err != nil || len(src.CompanionPaths) == 0 {
		return folded, err
	}
	return "c:" + folded, nil
}

// foldCompanions combines a source's own signature with its companions'
// into one. Both tiers of the change check fold companions identically —
// they have to, or the cheap stat tier would report "unchanged" for a
// source whose content the hash tier considers changed, and the file would
// stop re-indexing. per computes one path's signature.
//
// A companion that does not exist contributes an "absent" marker rather
// than an error: a session whose message directory has not been created
// yet is normal, and the directory appearing later has to register as a
// change.
func foldCompanions(base string, paths []string, per func(path string, isDir bool) (string, error)) (string, error) {
	if len(paths) == 0 {
		return base, nil
	}
	h := sha256.New()
	io.WriteString(h, base)
	for _, p := range paths {
		fi, err := os.Stat(p)
		switch {
		case os.IsNotExist(err):
			fmt.Fprintf(h, "\x01%s\x00absent", p)
			continue
		case err != nil:
			return "", err
		}
		sig, err := per(p, fi.IsDir())
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "\x01%s\x00%s", p, sig)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func statSignature(path string, isDir bool) (string, error) {
	if !isDir {
		fi, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("f:%d:%d", fi.Size(), fi.ModTime().UnixNano()), nil
	}

	entries, err := os.ReadDir(path)
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
// databases) by content, directories by sorted child names + contents, and
// any CompanionPaths folded in after the primary.
func hashSource(src agent.SourceRef) (string, error) {
	hash, err := hashPath(src.Path, src.Kind == agent.SourceDir)
	if err != nil {
		return "", err
	}
	return foldCompanions(hash, src.CompanionPaths, hashPath)
}

func hashPath(path string, isDir bool) (string, error) {
	if isDir {
		return hashDir(path)
	}
	return hashFile(path)
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
