import { useState } from "react";
import { LoadMore, usePagedList } from "../paged";
import { Link } from "@tanstack/react-router";
import { api, fmtWhen, shortPath, type CommandRow } from "../api";
import { useHighlight } from "../highlight";
import { useRowWindow } from "../windowed";
import {
  AgentDot,
  CopyButton,
  EmptyNote,
  FilterBar,
  inputCls,
  LoadError,
  Loading,
  PageHeader,
  SkeletonRows,
  useDebounced,
} from "../ui";

const FORMATS = ["zsh", "bash", "fish", "plain"] as const;
const PAGE = 100;

// Global command browser: every shell command any agent ran, newest
// first, each row linking back to its session — plus one-click export
// into real shell history files (same formats as `ccpeek export`).
export function CommandsPage() {
  const [qInput, setQInput] = useState("");
  // Settled before it reaches the query: the filter used to fire one
  // full-text request per keystroke.
  const q = useDebounced(qInput, 250);
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
    ["commands", q, agent, since, until],
    (offset) =>
      api.commands({
        q,
        agent,
        since,
        until,
        limit: String(PAGE),
        offset: String(offset),
      }),
    PAGE,
  );
  // Windowed: each row carries a highlighted <pre>, so a browser scrolled
  // through five pages was 500 syntax-highlighted blocks in the DOM at
  // once. Only the on-screen slice is mounted, and highlighting re-runs
  // for each new slice rather than once over everything.
  const { listRef, virtualizer, virtualItems } = useRowWindow<
    CommandRow,
    HTMLUListElement
  >(rows, (c, i) => `${c.sessionId}-${c.at}-${i}`, 76);
  useHighlight(listRef, [virtualItems]);

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
      <PageHeader title="Commands">
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
            value={qInput}
            onChange={(e) => setQInput(e.target.value)}
            placeholder="Filter commands…"
            aria-label="Filter commands"
            className={`w-64 ${inputCls}`}
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
              <div className="border-t border-edge px-3 py-1.5 text-micro text-ink-faint">
                current filters, ≤1000
              </div>
            </div>
          </details>
        </FilterBar>
      </PageHeader>

      {error && <LoadError error={error} />}
      {isLoading && (
        <Loading label="Loading commands…">
          <SkeletonRows rows={8} />
        </Loading>
      )}
      {!isLoading && !error && rows.length === 0 && (
        <EmptyNote>No commands match.</EmptyNote>
      )}

      <ul
        ref={listRef}
        className="relative overflow-hidden rounded-md border border-edge"
        style={{ height: virtualizer.getTotalSize() }}
      >
        {virtualItems.map((vi) => {
          const c = rows[vi.index];
          return (
            <li
              key={vi.key}
              data-index={vi.index}
              ref={virtualizer.measureElement}
              className="group absolute top-0 left-0 w-full border-b border-edge bg-surface-1 px-3 py-2 transition-colors hover:bg-surface-2/40"
              style={{
                transform: `translateY(${vi.start - virtualizer.options.scrollMargin}px)`,
              }}
            >
              <div className="mb-1 flex min-w-0 items-center gap-2 font-mono text-meta text-ink-faint">
                <AgentDot agent={c.agent} />
                {c.cwd && (
                  <span className="min-w-0 truncate">{shortPath(c.cwd)}</span>
                )}
                <Link
                  to="/sessions/$agent/$sessionId"
                  params={{ agent: c.agent, sessionId: c.sessionId }}
                  search={{ tab: "commands" }}
                  className="shrink-0 hover:text-accent"
                >
                  session {c.sessionId.slice(0, 8)}
                </Link>
                <span className="ml-auto shrink-0 tabular-nums">
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
          );
        })}
      </ul>

      <LoadMore
        hasNextPage={hasNextPage}
        isFetchingNextPage={isFetchingNextPage}
        onLoadMore={fetchNextPage}
      />
    </div>
  );
}
