import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { api, fmtCost, fmtTokens, totalTokens } from "../api";

const AGENTS = ["", "claude-code", "pi", "codex", "opencode", "cursor"];

// The primary surface of the session-centric model: a filterable stream of
// sessions across every agent (docs/v2-plan.md §7 P0).
const PAGE = 100;

export function SessionsPage() {
  const [agent, setAgent] = useState("");
  const [q, setQ] = useState("");
  const [pages, setPages] = useState(1);

  const { data, isLoading, error } = useQuery({
    queryKey: ["sessions", agent, q, pages],
    queryFn: () => api.sessions({ agent, q, limit: String(PAGE * pages) }),
    placeholderData: (prev) => prev,
  });

  const sessions = data ?? [];
  const mayHaveMore = sessions.length === PAGE * pages;

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Sessions</h1>
        <div className="ml-auto flex gap-2">
          <select
            value={agent}
            onChange={(e) => setAgent(e.target.value)}
            className="rounded-md border border-edge bg-surface-1 px-2 py-1.5 text-sm"
            aria-label="Filter by agent"
          >
            {AGENTS.map((a) => (
              <option key={a} value={a}>
                {a === "" ? "all agents" : a}
              </option>
            ))}
          </select>
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Filter by title…"
            className="w-56 rounded-md border border-edge bg-surface-1 px-3 py-1.5 text-sm placeholder:text-ink-dim"
          />
        </div>
      </div>

      {error && <p className="text-warn">Failed to load: {String(error)}</p>}
      {isLoading && <p className="text-ink-dim">Loading…</p>}
      {!isLoading && sessions.length === 0 && (
        <p className="text-ink-dim">No sessions match.</p>
      )}

      <ul className="divide-y divide-edge overflow-hidden rounded-lg border border-edge">
        {sessions.map((s) => (
          <li key={`${s.agent}/${s.id}`}>
            <Link
              to="/sessions/$agent/$sessionId"
              params={{ agent: s.agent, sessionId: s.id }}
              className="block bg-surface-1 px-4 py-3 transition-colors hover:bg-surface-2"
            >
              <div className="flex items-baseline gap-3">
                <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-accent">
                  {s.agent}
                </span>
                <span className="truncate font-medium">
                  {s.title || <span className="text-ink-dim">(untitled)</span>}
                </span>
                <span className="ml-auto shrink-0 text-sm tabular-nums text-ok">
                  {fmtCost(s.costUSD, s.unpricedTokens)}
                </span>
              </div>
              <div className="mt-1 flex gap-4 text-xs text-ink-dim">
                <span>{s.modifiedAt.slice(0, 16).replace("T", " ")}</span>
                <span className="truncate">{s.cwd}</span>
                {s.gitBranch && <span>⎇ {s.gitBranch}</span>}
                <span className="ml-auto tabular-nums">
                  {s.messages} msgs · {s.toolCalls} tools ·{" "}
                  {fmtTokens(totalTokens(s.tokens))} tok
                </span>
              </div>
            </Link>
          </li>
        ))}
      </ul>

      {mayHaveMore && (
        <button
          onClick={() => setPages((p) => p + 1)}
          className="mt-4 w-full rounded-lg border border-edge bg-surface-1 py-2 text-sm text-ink-dim hover:text-ink"
        >
          Load more
        </button>
      )}
    </div>
  );
}
