import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { parityApi } from "../api";

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

// Artifact browser: plans, todos, tasks, snapshots, pastes, memories,
// file history, usage data — every sidecar v1 had pages for, in one
// kind-filterable list.
export function ArtifactsPage() {
  const [kind, setKind] = useState("");

  const { data, isLoading, error } = useQuery({
    queryKey: ["artifacts", kind],
    queryFn: () => parityApi.artifacts(kind),
  });
  const artifacts = data ?? [];

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Artifacts</h1>
        <select
          value={kind}
          onChange={(e) => setKind(e.target.value)}
          className="ml-auto rounded-md border border-edge bg-surface-1 px-2 py-1.5 text-sm"
          aria-label="Filter by kind"
        >
          {KINDS.map((k) => (
            <option key={k} value={k}>
              {k === "" ? "all kinds" : k.replaceAll("_", " ")}
            </option>
          ))}
        </select>
      </div>

      {error && <p className="text-warn">Failed to load: {String(error)}</p>}
      {isLoading && <p className="text-ink-dim">Loading…</p>}
      {!isLoading && artifacts.length === 0 && (
        <p className="text-ink-dim">No artifacts.</p>
      )}

      <ul className="divide-y divide-edge overflow-hidden rounded-lg border border-edge">
        {artifacts.map((a) => (
          <li key={`${a.agent}/${a.kind}/${a.name}`}>
            <Link
              to="/artifacts/$agent/$kind/$name"
              params={{ agent: a.agent, kind: a.kind, name: a.name }}
              className="flex items-baseline gap-3 bg-surface-1 px-4 py-3 transition-colors hover:bg-surface-2"
            >
              <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-accent">
                {a.kind.replaceAll("_", " ")}
              </span>
              <span className="truncate font-medium">{a.name}</span>
              <span className="ml-auto shrink-0 text-xs text-ink-dim tabular-nums">
                {a.sessions > 0 && <>{a.sessions} session(s) · </>}
                {a.size} B
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}
