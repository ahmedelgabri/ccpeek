import { LoadMore, usePagedList } from "../paged";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { fmtBytes, parityApi } from "../api";
import {
  AgentChip,
  EmptyNote,
  FilterBar,
  LoadError,
  SkeletonRows,
} from "../ui";

const KINDS = [
  "",
  "plan",
  "todo_list",
  "task_group",
  "shell_snapshot",
  "paste",
  "memory",
  "file_history",
  "usage_facet",
  "usage_report",
] as const;

const PAGE = 100;

// Artifact browser: plans, todos, tasks, snapshots, pastes, memories,
// file history, usage data — every sidecar v1 had pages for, in one
// kind-filterable list. Offset pages of a fixed size: a single capped
// request would silently hide everything past the cap.
export function ArtifactsPage() {
  const search = useSearch({ from: "/artifacts" });
  const navigate = useNavigate({ from: "/artifacts" });
  const kind = search.kind ?? "";
  const agent = search.agent ?? "";

  const setFilter = (patch: { agent?: string; kind?: string }) =>
    void navigate({
      search: (prev: { agent?: string; kind?: string }) => ({
        ...prev,
        ...patch,
      }),
      replace: true,
    });

  const {
    rows: artifacts,
    isLoading,
    error,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = usePagedList(
    ["artifacts", kind, agent],
    (offset) => parityApi.artifacts(kind, agent, PAGE, offset),
    PAGE,
  );

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Artifacts</h1>
        <FilterBar agent={agent} onAgent={(v) => setFilter({ agent: v })}>
          <select
            value={kind}
            onChange={(e) => setFilter({ kind: e.target.value })}
            className="rounded-md border border-edge bg-surface-1 px-2 py-1.5 font-mono text-xs"
            aria-label="Filter by kind"
          >
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {k === "" ? "all kinds" : k.replaceAll("_", " ")}
              </option>
            ))}
          </select>
        </FilterBar>
      </div>

      {error && <LoadError error={error} />}
      {isLoading && (
        <div role="status">
          <span className="sr-only">Loading artifacts…</span>
          <SkeletonRows rows={8} />
        </div>
      )}
      {!isLoading && !error && artifacts.length === 0 && (
        <div role="status">
          <EmptyNote>No artifacts.</EmptyNote>
        </div>
      )}

      {artifacts.length > 0 && (
        <ul className="divide-y divide-edge overflow-hidden rounded-lg border border-edge">
          {artifacts.map((a) => (
            <li key={`${a.agent}/${a.kind}/${a.name}`}>
              <Link
                to="/artifacts/$agent/$kind/$name"
                params={{ agent: a.agent, kind: a.kind, name: a.name }}
                className="flex items-center gap-3 bg-surface-1 px-4 py-3 transition-colors hover:bg-surface-2"
              >
                <AgentChip agent={a.agent} />
                <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-accent">
                  {a.kind.replaceAll("_", " ")}
                </span>
                <span className="truncate font-medium">{a.name}</span>
                <span className="ml-auto shrink-0 text-xs text-ink-dim tabular-nums">
                  {a.sessions > 0 && (
                    <>
                      {a.sessions} {a.sessions === 1 ? "session" : "sessions"}{" "}
                      ·{" "}
                    </>
                  )}
                  {fmtBytes(a.size)}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      <LoadMore
        hasNextPage={hasNextPage}
        isFetchingNextPage={isFetchingNextPage}
        onLoadMore={fetchNextPage}
      />
    </div>
  );
}
