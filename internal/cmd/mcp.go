package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve ccpeek as an MCP server over stdio",
	Long: `Expose the ccpeek index to agents via the Model Context Protocol:
sessions, session, transcript, usage, and search tools over stdio.

The archive stays live for the whole connection: once the first pass
finishes, the server watches every agent root and re-indexes on
filesystem changes, so sessions written while a client holds the
connection open — clients keep MCP servers running for days — are
queryable without a restart. The status tool reports whether a pass is
running.

Register with Claude Code:

  claude mcp add ccpeek -- ccpeek mcp

stdout carries the protocol; all logging goes to stderr.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMCP(cmd, os.Stdin, os.Stdout, os.Stderr, 0)
	},
}

// runMCP serves MCP over the given streams and keeps the index live for
// as long as the connection lasts. debounce is the watch loop's
// coalescing window; 0 takes ingest's own default and tests shorten it.
func runMCP(cmd *cobra.Command, stdin io.Reader, stdout, logw io.Writer, debounce time.Duration) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	logf := func(format string, a ...any) { fmt.Fprintf(logw, format, a...) }

	// stdout is the MCP transport — everything else goes to stderr.
	// Serve IMMEDIATELY and index in the background: a first run or a
	// large changed corpus must not stall the client's initialize
	// handshake past its launch timeout. While a pass runs, tools read a
	// visibly WARMING archive — per-source transactions commit
	// incrementally and rollups regenerate at the end of the pass — and
	// the `status` tool tells clients they are reading that state.
	eng, bootstrap, err := openEngineDeferred(ctx, cmd, indexNow(), logw)
	if err != nil {
		return err
	}
	defer eng.Close()

	// Watch passes reuse the run's root overrides, resolved HERE so a
	// malformed --root fails the command rather than a background
	// goroutine that has nowhere to report it.
	watchOpts, err := ingestOptions(cmd)
	if err != nil {
		return err
	}
	var index indexState
	watchOpts.Progress = index.progress

	// The background pipeline must not outlive the store. On EOF — a
	// client that connects, probes, and disconnects — Serve returns while
	// the first pass is still running, and closing the engine under it
	// killed the pass with "sql: database is closed", leaving a first run
	// without its migrated_at stamp so the next start redid the whole
	// bootstrap. Cancel and JOIN before the store closes: defers run LIFO,
	// so this one is registered after eng.Close and therefore runs first.
	// A canceled pass keeps the sources it already committed — per-source
	// transactions are the crash-safety model.
	indexCtx, stopIndexing := context.WithCancel(ctx)
	var indexing sync.WaitGroup
	defer func() {
		stopIndexing()
		indexing.Wait()
	}()
	indexing.Add(1)
	go func() {
		defer indexing.Done()
		liveIndex(indexCtx, eng, bootstrap, watchOpts, debounce, &index, logf)
	}()

	status := func() mcp.Status {
		st := mcp.Status{Indexing: index.running()}
		// ONE read, off the 4-connection read pool. GetMeta goes through
		// the single writer connection, so a pair of them queued behind
		// ingest's write transactions — and MCP processes requests
		// serially, so a stalled status stalled every request behind it,
		// ping included.
		meta, err := eng.store.GetMetaMulti(ctx, "v1_import_state", "v1_import_error")
		if err == nil {
			st.V1ImportState = meta["v1_import_state"]
			st.V1ImportError = meta["v1_import_error"]
		}
		return st
	}

	err = mcp.New(eng.query, Version, status).Serve(ctx, stdin, stdout)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		// A signal ended the session; that is a clean shutdown, not a
		// transport failure to report on stderr and exit non-zero for.
		return nil
	}
	return err
}

// liveIndex is the MCP server's index pipeline: bootstrap, then the
// secret scan, then the fsnotify watch loop — the serving path's order,
// for the serving path's reason (the scanner pages messages across
// several read snapshots, so an ingest running under it could pair a new
// content hash with a stale page set; watch passes scan inside onChange,
// which the loop runs synchronously between passes, so ingest and
// scanning never overlap). One goroutine runs all of it, so unlike the
// HTTP path there is nothing to serialize the two scan sites against.
//
// Watch is unconditional here rather than a flag: an MCP server is
// long-lived by nature, and an index frozen at startup is the wrong
// default for a connection measured in days. A watcher that cannot be
// set up (the per-user inotify limit is reachable) degrades to serving
// the bootstrap snapshot instead of taking the server down.
func liveIndex(ctx context.Context, eng *engine, bootstrap func(context.Context) error, opts ingest.Options, debounce time.Duration, index *indexState, logf func(string, ...any)) {
	if bootstrap != nil {
		// Through runBootstrap, so the pass's outcome is recorded in meta
		// here too: whichever entry point ran last, the state a serving
		// ccpeek reads describes the index it is actually serving.
		if err := index.during(func() error { return runBootstrap(ctx, eng, bootstrap) }); err != nil {
			if ctx.Err() == nil {
				logf("WARNING: indexing failed (serving whatever is indexed so far): %v\n", err)
			}
			return
		}
		if err := index.during(func() error {
			return scanChanged(ctx, eng, eng.report, logf)
		}); err != nil && ctx.Err() == nil {
			logf("WARNING: %v\n", err)
		}
	}
	if err := eng.runner.Watch(ctx, opts, debounce, func(rep *ingest.Report) {
		if err := index.during(func() error {
			return scanChanged(ctx, eng, rep, logf)
		}); err != nil && ctx.Err() == nil {
			logf("WARNING: %v\n", err)
		}
	}); err != nil && ctx.Err() == nil {
		logf("WARNING: not watching for new sessions (serving the archive as last indexed): %v\n", err)
	}
}

// indexState backs the `status` tool's Indexing flag, which has to read
// true while ANY pass rewrites the archive, not only the first one.
//
// Passes this file calls — the bootstrap and the two scan sites —
// bracket exactly. Watch passes cannot: ingest.Runner.Watch owns its
// loop and announces only the END of a pass that CHANGED something, so a
// flag latched on that announcement would pin true forever after the
// first pass that found nothing new. Watch passes report through
// Options.Progress instead — one event per root, one per source — and
// the flag lapses passGrace after the last of them. Indexing is a
// freshness hint rather than a lock, and this is where the hint is
// approximate: a pass whose tail (link reconciliation, rollups) outlasts
// the grace reads as settled a moment early.
type indexState struct {
	bracketed atomic.Int64
	// lastEvent is the UnixNano of the most recent watch-pass progress
	// event; 0 until one arrives.
	lastEvent atomic.Int64
}

// passGrace is how long one watch-pass progress event holds the flag.
const passGrace = 2 * time.Second

func (s *indexState) begin() { s.bracketed.Add(1) }

func (s *indexState) end() { s.bracketed.Add(-1) }

// during brackets a pass this process drives itself.
func (s *indexState) during(pass func() error) error {
	s.begin()
	defer s.end()
	return pass()
}

// progress runs on the ingest goroutine, so it does one atomic store and
// nothing else.
func (s *indexState) progress(ingest.Progress) {
	s.lastEvent.Store(time.Now().UnixNano())
}

func (s *indexState) running() bool {
	if s.bracketed.Load() > 0 {
		return true
	}
	last := s.lastEvent.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < passGrace
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
