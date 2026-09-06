package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type maintenanceContextKey struct{}

// LockMaintenance serializes complete maintenance passes across processes.
// The returned context allows nested operations on the same archive to reuse
// the lock. It must not escape to a concurrently running maintenance task.
func (s *Store) LockMaintenance(ctx context.Context) (context.Context, func(), error) {
	if s.path == "" || s.path == ":memory:" {
		return ctx, func() {}, nil
	}
	return lockPath(ctx, s.path)
}

func lockPath(ctx context.Context, path string) (context.Context, func(), error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return ctx, nil, err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	} else if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(parent, filepath.Base(path))
	}
	if held, _ := ctx.Value(maintenanceContextKey{}).(string); held == path {
		return ctx, func() {}, nil
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return ctx, nil, fmt.Errorf("opening maintenance lock: %w", err)
	}
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		acquired, err := tryFileLock(f)
		if err != nil {
			f.Close()
			return ctx, nil, err
		}
		if acquired {
			return context.WithValue(ctx, maintenanceContextKey{}, path), func() { unlockFile(f); f.Close() }, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return ctx, nil, ctx.Err()
		case <-timer.C:
		}
	}
}
