import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import {
  api,
  fmtCost,
  fmtTokens,
  inclusiveUntil,
  shortPath,
  totalTokens,
  type SessionSummary,
} from "../api";
import { AgentChip, EmptyNote, FilterBar, SkeletonRows } from "../ui";

const PAGE = 100;

// The primary surface of the session-centric model: a filterable stream
// grouped by day. Filters live in the URL, so every filtered view is a
// deep link (docs/v2-plan.md §5.2).
export function SessionsPage() {
  const search = useSearch({ from: "/sessions" });
  const navigate = useNavigate({ from: "/sessions" });
  const [pages, setPages] = useState(1);
  const agent = search.agent ?? "";
  const q = search.q ?? "";
  const project = search.project ?? "";
  const since = search.since ?? "";
  const until = search.until ?? "";

  const setFilter = (patch: Record<string, string>) =>
    void navigate({
      search: (prev: Record<string, string | undefined>) => {
        const merged: Record<string, string | undefined> = {
          ...prev,
          ...patch,
        };
        for (const k of Object.keys(merged)) {
          if (!merged[k]) delete merged[k];
        }
        return merged;
      },
      replace: true,
    });

  const { data, isLoading, error } = useQuery({
    queryKey: ["sessions", agent, q, project, since, until, pages],
    queryFn: () =>
      api.sessions({
        agent,
        q,
        project,
        since,
        until: inclusiveUntil(until),
        limit: String(PAGE * pages),
      }),
    placeholderData: (prev) => prev,
  });

  const sessions = data ?? [];
  const mayHaveMore = sessions.length === PAGE * pages;
  const groups = groupByDay(sessions);

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Sessions</h1>
        {project && (
          <button
            onClick={() => setFilter({ project: "" })}
            className="rounded border border-edge bg-surface-2/60 px-2 py-0.5 font-mono text-xs text-ink-dim hover:text-ink"
            title="Clear workspace filter"
          >
            {shortPath(project)} ✕
          </button>
        )}
        <FilterBar
          since={since}
          until={until}
          onRange={(sv, uv) => setFilter({ since: sv, until: uv })}
          agent={agent}
          onAgent={(v) => setFilter({ agent: v })}
        >
          <input
            value={q}
            onChange={(e) => setFilter({ q: e.target.value })}
            placeholder="Filter by title…"
            className="w-56 rounded-md border border-edge bg-surface-1 px-3 py-1.5 text-sm placeholder:text-ink-faint"
          />
        </FilterBar>
      </div>

      {error && <p className="text-warn">Failed to load: {String(error)}</p>}
      {isLoading && <SkeletonRows rows={8} />}
      {!isLoading && sessions.length === 0 && (
        <EmptyNote>No sessions match.</EmptyNote>
      )}

      <div className="space-y-4">
        {groups.map((g) => (
          <section key={g.day}>
            <h2 className="microlabel mb-1.5 flex items-center gap-2">
              {g.day}
              <span className="h-px flex-1 bg-edge" />
              <span className="tabular-nums">{g.sessions.length}</span>
            </h2>
            <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
              {g.sessions.map((s) => (
                <SessionRow key={`${s.agent}/${s.id}`} s={s} />
              ))}
            </ul>
          </section>
        ))}
      </div>

      {mayHaveMore && (
        <button
          onClick={() => setPages((p) => p + 1)}
          className="mt-4 w-full rounded-md border border-edge bg-surface-1 py-2 font-mono text-xs text-ink-dim hover:text-ink"
        >
          load more
        </button>
      )}
    </div>
  );
}

function SessionRow({ s }: { s: SessionSummary }) {
  return (
    <li>
      <Link
        to="/sessions/$agent/$sessionId"
        params={{ agent: s.agent, sessionId: s.id }}
        className="block border-l-2 border-transparent bg-surface-1 px-4 py-2.5 transition-colors hover:border-accent hover:bg-surface-2/40"
      >
        <div className="flex items-baseline gap-3">
          <AgentChip agent={s.agent} />
          <span className="truncate text-sm font-medium">
            {s.title || <span className="text-ink-faint">(untitled)</span>}
          </span>
          <span className="ml-auto shrink-0 font-mono text-sm text-ok tabular-nums">
            {fmtCost(s.costUSD, s.unpricedTokens)}
          </span>
        </div>
        <div className="mt-1 flex gap-4 font-mono text-[11px] text-ink-faint">
          <span className="tabular-nums">{s.modifiedAt.slice(11, 16)}</span>
          <span className="truncate">{shortPath(s.cwd)}</span>
          {s.gitBranch && <span>⎇ {s.gitBranch}</span>}
          <span className="ml-auto shrink-0 tabular-nums">
            {s.messages} msgs · {s.toolCalls} tools ·{" "}
            {fmtTokens(totalTokens(s.tokens))} tok
          </span>
        </div>
      </Link>
    </li>
  );
}

function groupByDay(sessions: SessionSummary[]) {
  const out: { day: string; sessions: SessionSummary[] }[] = [];
  for (const s of sessions) {
    const day = s.modifiedAt.slice(0, 10) || "(no date)";
    const last = out[out.length - 1];
    if (last && last.day === day) last.sessions.push(s);
    else out.push({ day, sessions: [s] });
  }
  return out;
}
