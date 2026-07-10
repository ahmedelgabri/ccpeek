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
	"encoding/hex"
	"encoding/json"
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
	// ConfigRoots are explicit --root overrides per agent; they take
	// precedence over the agent's own env overrides and defaults.
	ConfigRoots map[canon.AgentSlug][]string
	// Getenv defaults to os.Getenv; injectable for tests.
	Getenv agent.Getenv
	// Home is the user's home directory for ~-expansion; defaults to
	// os.UserHomeDir.
	Home string
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

	known, err := r.store.SourceHashes(ctx)
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

		for _, src := range sources {
			if err := ctx.Err(); err != nil {
				return nil, r.fail(ctx, report, started, err)
			}
			report.FilesSeen++
			hash, err := hashSource(src)
			if err != nil {
				report.Issues = append(report.Issues, canon.Issue{
					Agent: rr.adapter.Slug(), Severity: canon.SeverityWarn,
					Category: "io", SourcePath: src.Path,
					Detail: fmt.Sprintf("hashing failed: %v", err),
				})
				continue
			}
			if known[src.Path] == hash {
				continue // unchanged
			}
			report.FilesChanged++
			if err := r.ingestSource(ctx, rr.adapter, src, hash, report); err != nil {
				report.Issues = append(report.Issues, canon.Issue{
					Agent: rr.adapter.Slug(), Severity: canon.SeverityError,
					Category: "parse", SourcePath: src.Path,
					Detail: err.Error(),
				})
			}
		}
	}

	if _, pending, err := r.store.ResolvePending(ctx); err != nil {
		return nil, r.fail(ctx, report, started, err)
	} else {
		report.LinksPending = pending
	}
	if report.FilesChanged > 0 {
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
	return report, nil
}

// ingestSource parses one changed source inside its own transaction.
func (r *Runner) ingestSource(ctx context.Context, a agent.Adapter, src agent.SourceRef, hash string, report *Report) error {
	w, err := r.store.BeginWrite(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()

	sink := &dbSink{writer: w, agent: a.Slug(), sourceHash: hash, report: report}
	if err := a.Parse(ctx, src, sink); err != nil {
		return err
	}
	if err := w.RecordSourceFile(src.Path, a.Slug(), hash); err != nil {
		return err
	}
	return w.Commit()
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

func (r *Runner) fail(ctx context.Context, report *Report, started time.Time, cause error) error {
	_ = r.store.InsertIssues(ctx, report.RunID, report.Issues)
	_ = r.store.FinishRun(ctx, report.RunID, "failed", started, r.counts(report), cause.Error())
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
