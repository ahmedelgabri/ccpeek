import { LoadMore, usePagedList } from "../paged";
import { useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { api, fmtWhen, shortPath } from "../api";
import { useHighlight } from "../highlight";
import {
  AgentDot,
  CopyButton,
  EmptyNote,
  FilterBar,
  SkeletonRows,
  useDebounced,
} from "../ui";

const FORMATS = ["zsh", "bash", "fish", "plain"] as const;
const PAGE = 100;

// Global command browser: every shell command any agent ran, newest
// first, each row linking back to its session — plus one-click export
// into real shell history files (same formats as `ccpeek export`).
export function CommandsPage() {
  const [q, setQ] = useState("");
  // The commands query scans and orders across the whole corpus, so it is
  // not something to run per keystroke.
  const debouncedQ = useDebounced(q);
  const [agent, setAgent] = useState("");
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");

  // Offset pages of a fixed size: a growing single limit would silently
  // stop at the server's cap and hide everything past it.
  const {
    rows,
    isLoading,
    error,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = usePagedList(
    ["commands", debouncedQ, agent, since, until],
    (offset) =>
      api.commands({
        q: debouncedQ,
        agent,
        since,
        until: until,
        limit: String(PAGE),
        offset: String(offset),
      }),
    PAGE,
  );
  const listRef = useRef<HTMLUListElement>(null);
  useHighlight(listRef, [rows]);

  const exportURL = (format: string) => {
    const p = new URLSearchParams({ format, limit: "1000" });
    if (q) p.set("q", q);
    if (agent) p.set("agent", agent);
    if (since) p.set("since", since);
    if (until) p.set("until", until);
    return `/api/v1/commands?${p.toString()}`;
  };

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Commands</h1>
        <FilterBar
          since={since}
          until={until}
          onRange={(sv, uv) => {
            setSince(sv);
            setUntil(uv);
          }}
          agent={agent}
          onAgent={setAgent}
        >
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Filter commands…"
            className="w-64 rounded-md border border-edge bg-surface-1 px-3 py-1.5 text-sm placeholder:text-ink-faint"
          />
          <details className="relative">
            <summary className="cursor-pointer list-none rounded-md border border-edge px-3 py-1.5 font-mono text-xs text-ink-dim hover:text-ink">
              export ▾
            </summary>
            <div className="absolute right-0 z-10 mt-1 w-40 overflow-hidden rounded-md border border-edge bg-surface-1 font-mono text-xs shadow-lg">
              {FORMATS.map((f) => (
                <a
                  key={f}
                  href={exportURL(f)}
                  download
                  className="block px-3 py-1.5 text-ink-dim hover:bg-surface-2 hover:text-ink"
                >
                  {f} history
                </a>
              ))}
              <div className="border-t border-edge px-3 py-1.5 text-[10px] text-ink-faint">
                current filters, ≤1000
              </div>
            </div>
          </details>
        </FilterBar>
      </div>

      {error && <p className="text-warn">Failed to load: {String(error)}</p>}
      {isLoading && <SkeletonRows rows={8} />}
      {!isLoading && !error && rows.length === 0 && (
        <EmptyNote>No commands match.</EmptyNote>
      )}

      <ul
        ref={listRef}
        className="divide-y divide-edge overflow-hidden rounded-md border border-edge"
      >
        {rows.map((c, i) => (
          <li
            key={`${c.sessionId}-${c.at}-${i}`}
            className="group bg-surface-1 px-3 py-2 transition-colors hover:bg-surface-2/40"
          >
            <div className="mb-1 flex items-center gap-2 font-mono text-[11px] text-ink-faint">
              <AgentDot agent={c.agent} />
              {c.cwd && <span className="truncate">{shortPath(c.cwd)}</span>}
              <Link
                to="/sessions/$agent/$sessionId"
                params={{ agent: c.agent, sessionId: c.sessionId }}
                search={{ tab: "commands" }}
                className="hover:text-accent"
              >
                session {c.sessionId.slice(0, 8)}
              </Link>
              <span className="ml-auto tabular-nums">
                {fmtWhen(c.at ?? "")}
              </span>
              <CopyButton text={c.command} />
            </div>
            <pre className="overflow-x-auto text-xs leading-relaxed">
              <code className="language-bash block break-words whitespace-pre-wrap">
                {c.command}
              </code>
            </pre>
          </li>
        ))}
      </ul>

      <LoadMore
        hasNextPage={hasNextPage}
        isFetchingNextPage={isFetchingNextPage}
        onLoadMore={fetchNextPage}
      />
    </div>
  );
}
