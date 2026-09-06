package ingest

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Timing for the kqueue-platform loop (see Watch and poll).
const (
	// pollInterval is the idle scan cadence. It matches v1's
	// fixed-interval ticker default; a no-change pass is a stat-only
	// fingerprint sweep, so the steady-state cost is metadata
	// syscalls, not reads.
	pollInterval = 30 * time.Second
	// hotPollInterval is the scan cadence while changes are landing:
	// new files are invisible to the per-file watches until a pass
	// finds them, so the backstop tightens during activity.
	hotPollInterval = 2 * time.Second
	// hotFor is how long after a changed pass the scan stays at
	// hotPollInterval before decaying to pollInterval.
	hotFor = 2 * time.Minute
	// hotWindow is the modification recency that earns a file its own
	// kqueue watch. Watching a FILE costs one descriptor; it is
	// watching a DIRECTORY that fans out to one per contained file.
	hotWindow = 24 * time.Hour
	// maxHotFiles caps the loop's descriptor budget.
	maxHotFiles = 512
)

// pollTimings carries poll's knobs so tests can compress (or stretch)
// them independently; Watch builds the production set.
type pollTimings struct {
	idle     time.Duration // scan interval when the tree is quiet
	fast     time.Duration // scan interval while changes are landing
	hotFor   time.Duration // how long a changed pass keeps the fast interval
	debounce time.Duration // delay between a file event and its pass
}

// Watch re-runs the pipeline whenever any resolved agent root changes,
// replacing v1's fixed-interval ticker with real notifications
// (docs/v2-plan.md §5.5). Events are debounced — agents write in bursts —
// and onChange fires only for runs that changed data. Roots that appear
// after startup (an agent installed mid-session) are picked up by a slow
// rescan tick.
//
// On kqueue platforms (macOS and the BSDs) Watch cannot use this path:
// fsnotify's kqueue backend opens a file descriptor for every FILE inside
// each watched directory, not one per directory, so a recursive watch over
// the agent roots holds an fd per transcript — tens of thousands per
// process — and a few concurrent servers (one `ccpeek mcp` per agent
// session) can exhaust the system file table (kern.maxfiles), taking
// unrelated apps down with ENFILE. Those platforms run poll instead: an
// adaptive-interval scan plus individual watches on recently-modified
// files (ADR-0002).
//
// debounce is the change-to-pass latency knob for both paths. Zero means
// each path's defaults — production callers pass zero, tests pass
// something short to keep watch passes prompt.
func (r *Runner) Watch(ctx context.Context, opts Options, debounce time.Duration, onChange func(*Report)) error {
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		t := pollTimings{idle: pollInterval, fast: hotPollInterval, hotFor: hotFor, debounce: 500 * time.Millisecond}
		if debounce > 0 {
			t = pollTimings{idle: debounce, fast: debounce, hotFor: hotFor, debounce: debounce}
		}
		return r.poll(ctx, opts, t, onChange)
	}
	if debounce <= 0 {
		debounce = 500 * time.Millisecond
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	watched := map[string]bool{}
	// Watch registration can fail per directory — on Linux the per-user
	// inotify limit (often 8192) is reachable by a large projects tree
	// plus the sidecar trees. Silently dropping those left the user with
	// "watch mode enabled" and no live updates. Failures are counted and
	// reported once per pass, naming the knob.
	warned := false
	addRoots := func() {
		if opts.Getenv == nil {
			opts.Getenv = os.Getenv
		}
		if opts.Home == "" {
			opts.Home, _ = os.UserHomeDir()
		}
		roots, _ := r.resolveRoots(opts)
		failed := 0
		for _, rr := range roots {
			failed += addRecursive(watcher, rr.root.Path, watched)
		}
		if failed > 0 && !warned {
			warned = true
			log.Printf("ccpeek: watching %d of %d directories — %d could not be registered; "+
				"live updates for those rely on the periodic rescan "+
				"(on Linux, raise fs.inotify.max_user_watches)",
				len(watched), len(watched)+failed, failed)
		}
	}
	addRoots()

	var (
		timer   *time.Timer
		timerCh <-chan time.Time
	)
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(debounce)
			timerCh = timer.C
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(debounce)
	}

	// Slow tick: catch roots created after startup and anything fsnotify
	// missed (network mounts, editors with odd rename dances).
	rescan := time.NewTicker(2 * time.Minute)
	defer rescan.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// New directories need their own watches (fsnotify is not
			// recursive).
			if ev.Op.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					addRecursive(watcher, ev.Name, watched)
				}
			}
			arm()

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			if err != nil {
				log.Printf("ccpeek: watcher error: %v", err)
			}
			arm() // err on the side of re-indexing

		case <-timerCh:
			report, err := r.Run(ctx, opts)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue // transient failure: keep watching
			}
			if opts.WatchPass != nil {
				opts.WatchPass()
			}
			if report.FilesChanged > 0 && onChange != nil {
				onChange(report)
			}

		case <-rescan.C:
			addRoots()
			arm()
		}
	}
}

// addRecursive registers a watch on root and every directory under it,
// returning how many registrations FAILED. Directories that have since
// been deleted are dropped from the watched set so it tracks reality
// rather than only ever growing.
func addRecursive(watcher *fsnotify.Watcher, root string, watched map[string]bool) int {
	failed := 0
	seen := map[string]bool{}
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		seen[path] = true
		if watched[path] {
			return nil
		}
		if watcher.Add(path) == nil {
			watched[path] = true
		} else {
			failed++
		}
		return nil
	})
	for path := range watched {
		if !seen[path] && strings.HasPrefix(path, root) {
			_ = watcher.Remove(path)
			delete(watched, path)
		}
	}
	return failed
}

// poll is the kqueue-platform loop (see Watch): an adaptive-interval
// scan as the backstop, plus individual kqueue watches on the files
// modified within hotWindow so appends to live sessions land
// near-instantly instead of on the next tick. New files are invisible
// to per-file watches, so the scan runs at t.fast while changes are
// landing and decays to t.idle once the tree goes quiet. Each pass
// re-resolves roots, so late-appearing agents need no separate rescan
// tick. onChange fires only for runs that changed data, same as the
// watch path.
func (r *Runner) poll(ctx context.Context, opts Options, t pollTimings, onChange func(*Report)) error {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Home == "" {
		opts.Home, _ = os.UserHomeDir()
	}

	var (
		events <-chan fsnotify.Event
		werrs  <-chan error
	)
	watched := map[string]bool{}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Degraded but functional: the scan alone still picks
		// everything up within t.idle.
		log.Printf("ccpeek: no file watches (%v); live updates rely on the periodic scan", err)
	} else {
		defer watcher.Close()
		events = watcher.Events
		werrs = watcher.Errors
		r.syncHotWatches(watcher, opts, watched)
	}

	var lastChange time.Time
	timer := time.NewTimer(t.idle)
	defer timer.Stop()
	arm := func(d time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case _, ok := <-events:
			if !ok {
				events, werrs = nil, nil // watcher died: scan-only from here
				continue
			}
			arm(t.debounce)

		case err, ok := <-werrs:
			if !ok {
				events, werrs = nil, nil
				continue
			}
			if err != nil {
				log.Printf("ccpeek: watcher error: %v", err)
			}
			arm(t.debounce) // err on the side of re-indexing

		case <-timer.C:
			report, err := r.Run(ctx, opts)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				timer.Reset(t.idle) // transient failure: keep scanning
				continue
			}
			if opts.WatchPass != nil {
				opts.WatchPass()
			}
			if report.FilesChanged > 0 {
				lastChange = time.Now()
				if onChange != nil {
					onChange(report)
				}
				// New hot files can only appear when a pass changed
				// something, so syncing here keeps the re-walk off the
				// no-change fast path. Aged-out watches linger until
				// the next change, which is harmless: the set stays
				// bounded at maxHotFiles either way.
				if watcher != nil {
					r.syncHotWatches(watcher, opts, watched)
				}
			}
			if time.Since(lastChange) < t.hotFor {
				timer.Reset(t.fast)
			} else {
				timer.Reset(t.idle)
			}
		}
	}
}

// syncHotWatches points watcher at hotFiles: adds the missing ones,
// drops the aged-out ones, and leaves the overlap alone. Failures are
// ignored — a file that cannot be watched is still covered by the scan.
func (r *Runner) syncHotWatches(watcher *fsnotify.Watcher, opts Options, watched map[string]bool) {
	want := map[string]bool{}
	for _, p := range r.hotFiles(opts) {
		want[p] = true
	}
	for p := range watched {
		if !want[p] {
			_ = watcher.Remove(p)
			delete(watched, p)
		}
	}
	for p := range want {
		if !watched[p] && watcher.Add(p) == nil {
			watched[p] = true
		}
	}
}

// hotFiles walks the resolved roots for files modified within hotWindow
// and returns the newest maxHotFiles of them — the sessions whose
// appends are worth a descriptor each.
func (r *Runner) hotFiles(opts Options) []string {
	type hot struct {
		path string
		mod  time.Time
	}
	cutoff := time.Now().Add(-hotWindow)
	var files []hot
	roots, _ := r.resolveRoots(opts)
	for _, rr := range roots {
		filepath.WalkDir(rr.root.Path, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, err := d.Info(); err == nil && info.ModTime().After(cutoff) {
				files = append(files, hot{path, info.ModTime()})
			}
			return nil
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > maxHotFiles {
		files = files[:maxHotFiles]
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths
}
