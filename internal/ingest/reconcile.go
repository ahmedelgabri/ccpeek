package ingest

import "context"

// Reconcile repairs derived data without reading or reparsing agent sources.
// Imports and --skip-index can use it after a previously interrupted pass.
func (r *Runner) Reconcile(ctx context.Context) error {
	ctx, unlock, err := r.store.LockMaintenance(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = r.reconcile(ctx, false)
	return err
}

// reconcile is shared by normal indexing and recovery. The caller holds the
// maintenance lock; only a data-changing pass ages unresolved links.
func (r *Runner) reconcile(ctx context.Context, changed bool) (int, error) {
	_, pending, err := r.store.ResolvePending(ctx, changed)
	if err != nil {
		return pending, err
	}
	// Source transactions persist this flag, so repair does not depend on
	// counters from the current pass. This also covers imports and pruning.
	dirty, err := r.store.DerivedDirty(ctx)
	if err != nil {
		return pending, err
	}
	if dirty {
		if _, _, err := r.store.ResolveArtifactLinks(ctx, r.linkRules()); err != nil {
			return pending, err
		}
	}
	// Even unchanged sources need repricing when the pricing snapshot changes
	// or a migration leaves empty rollups. RefreshRollups chooses full versus
	// affected-day regeneration; workspace membership must be refreshed first.
	needRollups := dirty
	if !needRollups {
		needRollups, err = r.store.RollupsNeedRegeneration(ctx, r.pricer)
		if err != nil {
			return pending, err
		}
	}
	if needRollups {
		if err := r.store.RefreshWorkspaces(ctx); err != nil {
			return pending, err
		}
		if err := r.store.RefreshRollups(ctx, r.pricer); err != nil {
			return pending, err
		}
	}
	return pending, r.store.SetMeta(ctx, "derived_dirty", "0")
}
