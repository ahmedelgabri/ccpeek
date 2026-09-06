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
	if _, _, err := r.store.ResolvePending(ctx, false); err != nil {
		return err
	}
	if _, _, err := r.store.ResolveArtifactLinks(ctx, r.linkRules()); err != nil {
		return err
	}
	if err := r.store.RefreshWorkspaces(ctx); err != nil {
		return err
	}
	if err := r.store.RefreshRollups(ctx, r.pricer); err != nil {
		return err
	}
	return r.store.SetMeta(ctx, "derived_dirty", "0")
}
