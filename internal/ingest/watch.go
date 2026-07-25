package ingest

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch re-runs the pipeline whenever any resolved agent root changes,
// replacing v1's fixed-interval ticker with real notifications
// (docs/v2-plan.md §5.5). Events are debounced — agents write in bursts —
// and onChange fires only for runs that changed data. Roots that appear
// after startup (an agent installed mid-session) are picked up by a slow
// rescan tick.
func (r *Runner) Watch(ctx context.Context, opts Options, debounce time.Duration, onChange func(*Report)) error {
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
