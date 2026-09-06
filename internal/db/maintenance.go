package db

import (
	"context"
	"time"
)

// PrepareRebuild forces available sources through their parsers again. It does
// not erase retained history: missing sources and v1-only rows may be the only
// remaining copies. Each successful parse replaces its own records atomically.
func (s *Store) PrepareRebuild(ctx context.Context) error {
	ctx, unlock, err := s.LockMaintenance(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if s.path != "" && s.path != ":memory:" {
		if err := s.Backup(ctx, s.path+".before-rebuild-"+time.Now().UTC().Format("20060102T150405.000000000")+".db"); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE source_files SET stat_sig='',parse_version=0`,
		`INSERT OR REPLACE INTO meta(key,value) VALUES ('derived_dirty','1')`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DerivedDirty survives an interrupted pass, unlike the in-memory run report.
func (s *Store) DerivedDirty(ctx context.Context) (bool, error) {
	value, _, err := s.GetMeta(ctx, "derived_dirty")
	return value == "1", err
}
