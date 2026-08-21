import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { LoadMore, usePagedList } from "../paged";
import { Link, useSearch } from "@tanstack/react-router";
import {
  api,
  COST_MODES,
  normalizeCostMode,
  fmtCount,
  fmtTokens,
  plural,
  shortPath,
  totalTokens,
  type CostMode,
  type SessionSummary,
} from "../api";
import { fullWhen, localDay, localTime } from "../time";
import {
  AgentChip,
  EmptyNote,
  FilterBar,
  groupRuns,
  inputCls,
  LoadError,
  Loading,
  Money,
  PageHeader,
  SectionHeading,
  Segmented,
  SkeletonRows,
  useSetFilter,
  useSlashFocus,
  useUrlText,
} from "../ui";

const PAGE = 100;

// The primary surface of the session-centric model: a filterable stream
// grouped by day. Filters live in the URL, so every filtered view is a
// deep link (docs/v2-plan.md §5.2).
export function SessionsPage() {
  const search = useSearch({ from: "/sessions" });
  const setFilter = useSetFilter("/sessions");
  const agent = search.agent ?? "";
  const project = search.project ?? "";
  const model = search.model ?? "";
  const since = search.since ?? "";
  const until = search.until ?? "";
  const costMode: CostMode = normalizeCostMode(search.cost_mode);

  // The URL is the filter. The box is local so typing stays responsive and
  // only the settled value is pushed — one request and one history write
  // per pause, not per keystroke — but what the list queries is the URL's
  // q, and an external navigation (the sidebar's Sessions link, a heatmap
  // day, the back button) resets the box. Keying the query off the
  // debounced local value instead left the two disagreeing: the address
  // bar said "all sessions" while the list stayed narrowed by text no
  // longer on screen.
  const q = search.q ?? "";
  const [titleInput, setTitleInput] = useUrlText(q, (v) => setFilter({ q: v }));
  const titleBox = useRef<HTMLInputElement>(null);
  useSlashFocus(titleBox);
  const [dense, setDense] = useState(false);

  // Offset pages of a fixed size: a growing single limit would silently
  // stop at the server's cap and hide everything past it.
  const {
    rows: sessions,
    isLoading,
    error,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = usePagedList(
    ["sessions", agent, q, project, model, since, until, costMode],
    (offset) =>
      api.sessions({
        agent,
        query: q,
        project,
        model,
        since,
        until,
        limit: String(PAGE),
        offset: String(offset),
        cost_mode: costMode,
      }),
    PAGE,
  );
  // Model options come from the model rollup, same source as Usage.
  const modelRows = useQuery({
    queryKey: ["usage", "model-options", costMode],
    queryFn: () => api.usage({ group: "model", cost_mode: costMode }),
  });
  const models = (modelRows.data ?? [])
    .map((r) => r.group)
    .filter((m) => m !== "");

  // Grouped by LOCAL day: the server orders by instant, and a local day
  // key stays contiguous under that order, so headings read as the days
  // the user actually worked rather than UTC's idea of them.
  const groups = groupRuns(
    sessions,
    (s) => localDay(s.modifiedAt) || "(no date)",
  );
  const activeFilters = [
    agent,
    project,
    model,
    since,
    until,
    q,
    costMode === "auto" ? "" : costMode,
  ].filter(Boolean).length;

  return (
    <div>
      <PageHeader
        title="Sessions"
        lede={
          sessions.length > 0 && (
            <span className="font-mono text-meta text-ink-faint tabular-nums">
              {fmtCount(sessions.length)}
              {hasNextPage ? "+" : ""} shown
            </span>
          )
        }
      >
        <FilterBar
          since={since}
          until={until}
          onRange={(sv, uv) => setFilter({ since: sv, until: uv })}
          agent={agent}
          onAgent={(v) => setFilter({ agent: v })}
          model={model}
          models={models}
          onModel={(v) => setFilter({ model: v })}
        >
          <Segmented
            label="Cost provenance"
            value={costMode}
            options={COST_MODES.map((mode) => ({ value: mode, label: mode }))}
            onChange={(mode) =>
              setFilter({ cost_mode: mode === "auto" ? "" : mode })
            }
          />
          <input
            ref={titleBox}
            value={titleInput}
            onChange={(e) => setTitleInput(e.target.value)}
            placeholder="Filter by title…"
            aria-label="Filter by title"
            title="Press / to focus"
            className={`w-56 ${inputCls}`}
          />
          <button
            type="button"
            onClick={() => setDense((v) => !v)}
            aria-pressed={dense}
            title="Toggle compact rows"
            className={`rounded-md border border-edge px-2 py-1.5 font-mono text-xs transition-colors hover:border-edge-strong ${
              dense ? "bg-surface-2 text-ink" : "text-ink-dim"
            }`}
          >
            compact
          </button>
        </FilterBar>
      </PageHeader>

      {/* Active filters are visible and individually removable. The
          workspace pill was the only one that showed at all, so a date
          range or model narrowed the list with nothing on screen saying
          so. */}
      {activeFilters > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-1.5">
          {project && (
            <FilterPill
              label={shortPath(project)}
              onClear={() => setFilter({ project: "" })}
            />
          )}
          {agent && (
            <FilterPill
              label={agent}
              onClear={() => setFilter({ agent: "" })}
            />
          )}
          {model && (
            <FilterPill
              label={model}
              onClear={() => setFilter({ model: "" })}
            />
          )}
          {(since || until) && (
            <FilterPill
              label={`${since || "…"} → ${until || "…"}`}
              onClear={() => setFilter({ since: "", until: "" })}
            />
          )}
          {costMode !== "auto" && (
            <FilterPill
              label={`cost: ${costMode}`}
              onClear={() => setFilter({ cost_mode: "" })}
            />
          )}
          {q && (
            <FilterPill
              label={`“${q}”`}
              onClear={() => {
                setTitleInput("");
                setFilter({ q: "" });
              }}
            />
          )}
          {activeFilters > 1 && (
            <button
              type="button"
              onClick={() => {
                setTitleInput("");
                setFilter({
                  project: "",
                  agent: "",
                  model: "",
                  since: "",
                  until: "",
                  q: "",
                  cost_mode: "",
                });
              }}
              className="font-mono text-meta text-ink-faint hover:text-ink"
            >
              clear all
            </button>
          )}
        </div>
      )}

      {error && <LoadError error={error} />}
      {isLoading && (
        <Loading label="Loading sessions…">
          <SkeletonRows rows={8} />
        </Loading>
      )}
      {!isLoading && !error && sessions.length === 0 && (
        <EmptyNote
          hint={
            activeFilters > 0
              ? "Try widening the date range or clearing a filter."
              : undefined
          }
        >
          No sessions match.
        </EmptyNote>
      )}

      <div className="space-y-3">
        {groups.map((g) => (
          <section key={g.key}>
            <SectionHeading count={plural(g.items.length, "session")}>
              {g.key}
            </SectionHeading>
            <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
              {g.items.map((s) => (
                <SessionRow key={`${s.agent}/${s.id}`} s={s} dense={dense} />
              ))}
            </ul>
          </section>
        ))}
      </div>

      <LoadMore
        hasNextPage={hasNextPage}
        isFetchingNextPage={isFetchingNextPage}
        onLoadMore={fetchNextPage}
      />
    </div>
  );
}

function FilterPill({
  label,
  onClear,
}: {
  label: string;
  onClear: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClear}
      title="Remove this filter"
      className="inline-flex max-w-64 items-center gap-1.5 rounded border border-edge bg-surface-2/60 px-2 py-0.5 font-mono text-meta text-ink-dim transition-colors hover:border-edge-strong hover:text-ink"
    >
      <span className="truncate">{label}</span>
      <span aria-hidden>✕</span>
      <span className="sr-only">Remove filter</span>
    </button>
  );
}

function SessionRow({ s, dense }: { s: SessionSummary; dense: boolean }) {
  const tokens = totalTokens(s.tokens);
  return (
    <li>
      <Link
        to="/sessions/$agent/$sessionId"
        params={{ agent: s.agent, sessionId: s.id }}
        search={{
          cost_mode: s.costMode === "auto" ? undefined : s.costMode,
        }}
        className={`block border-l-2 border-transparent bg-surface-1 px-4 transition-colors hover:border-accent hover:bg-surface-2/40 ${
          dense ? "py-1.5" : "py-2.5"
        }`}
      >
        <div className="flex min-w-0 items-baseline gap-3">
          <span
            title={fullWhen(s.modifiedAt)}
            className="shrink-0 font-mono text-meta text-ink-faint tabular-nums"
          >
            {localTime(s.modifiedAt)}
          </span>
          <AgentChip agent={s.agent} />
          <span className="min-w-0 flex-1 truncate text-sm font-medium">
            {s.title || <span className="text-ink-faint">(untitled)</span>}
          </span>
          {dense && (
            <span className="hidden shrink-0 font-mono text-meta text-ink-faint tabular-nums sm:inline">
              {fmtCount(s.messages)} msgs · {fmtTokens(tokens)} tok
            </span>
          )}
          <Money
            usd={s.costUSD}
            unpriced={
              (s.unpricedTokens ?? 0) + (s.unreportedTokens ?? 0) || undefined
            }
            className="shrink-0 text-sm"
            title={`${s.costMode} $${s.costUSDExact}`}
          />
        </div>
        {!dense && (
          <div className="mt-1 flex min-w-0 items-baseline gap-3 font-mono text-meta text-ink-faint">
            <span className="min-w-0 truncate">{shortPath(s.cwd)}</span>
            {s.gitBranch && (
              <span className="shrink-0 truncate">⎇ {s.gitBranch}</span>
            )}
            <span className="ml-auto shrink-0 tabular-nums">
              {fmtCount(s.messages)} msgs · {fmtCount(s.toolCalls)} tools ·{" "}
              {fmtTokens(tokens)} tok
            </span>
          </div>
        )}
      </Link>
    </li>
  );
}
