import { lazy, Suspense } from "react";

// The diff renderer is @pierre/diffs (shiki-based: word-level inline
// highlighting, syntax-colored rows, hunk collapsing). It loads as its
// own chunk the first time a diff is expanded — diffs never ride the
// initial bundle. Payloads are already capped server-side (16 KB each
// side), so no client-side size guard is needed.
const PierreDiff = lazy(() => import("./PierreDiff"));

export function DiffView({
  old,
  new: neu,
  path,
}: {
  old: string;
  new: string;
  path?: string;
}) {
  return (
    <Suspense
      fallback={<p className="font-mono text-meta text-ink-dim">Loading…</p>}
    >
      <PierreDiff old={old} new={neu} path={path} />
    </Suspense>
  );
}
