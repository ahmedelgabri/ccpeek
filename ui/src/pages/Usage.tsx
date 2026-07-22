import { lazy, Suspense, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  api,
  fmtCost,
  fmtTokens,
  inclusiveUntil,
  parityApi,
  totalTokens,
} from "../api";
import { FilterBar, SkeletonRows, useTooltip } from "../ui";

// Lazy so echarts ships as its own chunk, loaded only on this page.
const CostTimeline = lazy(() =>
  import("../CostTimeline").then((m) => ({ default: m.CostTimeline })),
);
const GroupBars = lazy(() =>
  import("../CostTimeline").then((m) => ({ default: m.GroupBars })),
);

const GROUPS = ["day", "model", "project", "agent", "blocks"] as const;

// pivotSearch builds the /sessions filter for a usage row: the row's own
// dimension plus every filter already active on this page, so drilling
// down never widens the scope.
function pivotSearch(
  group: string,
  value: string,
  active: { agent: string; model: string; since: string; until: string },
): Record<string, string> {
  const search: Record<string, string> = {};
  if (active.agent) search.agent = active.agent;
  if (active.model) search.model = active.model;
  if (active.since) search.since = active.since;
  if (active.until) search.until = active.until;
  switch (group) {
    case "day":
      search.since = value;
      search.until = value;
      break;
    case "agent":
      search.agent = value;
      break;
    case "model":
      search.model = value;
      break;
    case "project":
      search.project = value;
      break;
  }
  return search;
}

type SortKey = "group" | "sessions" | "tokens" | "cacheRead" | "cost";

const SORT_VALUE: Record<
  SortKey,
  (r: import("../api").UsageRow) => number | string
> = {
  group: (r) => r.group,
  sessions: (r) => r.sessions,
  tokens: (r) => totalTokens(r.tokens),
  cacheRead: (r) => r.tokens.cacheRead,
  cost: (r) => r.costUSD,
};

// Cost explorer: rollup aggregates filterable by date range, agent, and
// model, with the ECharts timeline over the same filters.
export function UsagePage() {
  const [group, setGroup] = useState<(typeof GROUPS)[number]>("day");
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");
  const [agent, setAgent] = useState("");
  const [model, setModel] = useState("");

  // No limit: usage is an aggregate surface and the server returns all
  // groups by default, so the page total, charts, and CSV are complete
  // by construction (the old fixed 1000 silently truncated past it).
  const { data, isLoading, error } = useQuery({
    queryKey: ["usage", group, since, until, agent, model],
    queryFn: () =>
      api.usage({
        group,
        since,
        until: inclusiveUntil(until),
        agent,
        model,
      }),
    enabled: group !== "blocks",
    placeholderData: (prev) => prev,
  });
  // Model options come from the unfiltered model rollup.
  const modelRows = useQuery({
    queryKey: ["usage", "model-options"],
    queryFn: () => api.usage({ group: "model" }),
  });
  // Blocks support the agent filter (dates/models don't apply to the
  // rolling 5h windows and are hidden on that tab).
  const blocks = useQuery({
    queryKey: ["blocks", agent],
    queryFn: () => parityApi.blocks(36, agent),
    enabled: group === "blocks",
  });
  const budget = useQuery({
    queryKey: ["budget"],
    queryFn: () => parityApi.budget(),
  });

  // Sorting: null keeps the server order (day desc / cost desc); a click
  // cycles asc↔desc per column.
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean } | null>(
    null,
  );
  const toggleSort = (key: SortKey) =>
    setSort((prev) =>
      prev?.key === key ? { key, desc: !prev.desc } : { key, desc: true },
    );

  const unsorted = data ?? [];
  const rows = useMemo(() => {
    if (!sort) return unsorted;
    const value = SORT_VALUE[sort.key];
    return unsorted.toSorted((a, b) => {
      const av = value(a);
      const bv = value(b);
      const cmp =
        typeof av === "number" && typeof bv === "number"
          ? av - bv
          : String(av).localeCompare(String(bv));
      return sort.desc ? -cmp : cmp;
    });
  }, [unsorted, sort]);
  const maxCost = Math.max(...rows.map((r) => r.costUSD), 0.000001);
  const total = rows.reduce((acc, r) => acc + r.costUSD, 0);
  const anyUnpriced = rows.some((r) => r.hasUnpriced);
  const tooltip = useTooltip();
  const models = (modelRows.data ?? [])
    .map((r) => r.group)
    .filter((m) => m !== "");

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Usage</h1>
        <span className="text-sm text-ok tabular-nums">
          {fmtCost(total)}{" "}
          {anyUnpriced && <span className="text-warn">(+unpriced)</span>}
        </span>
        <FilterBar
          since={group === "blocks" ? undefined : since}
          until={group === "blocks" ? undefined : until}
          onRange={
            group === "blocks"
              ? undefined
              : (sv, uv) => {
                  setSince(sv);
                  setUntil(uv);
                }
          }
          agent={agent}
          onAgent={setAgent}
          model={group === "blocks" ? undefined : model}
          models={group === "blocks" ? undefined : models}
          onModel={group === "blocks" ? undefined : setModel}
        >
          <div className="flex rounded-md border border-edge font-mono text-xs">
            {GROUPS.map((g) => (
              <button
                key={g}
                onClick={() => setGroup(g)}
                className={`px-2.5 py-1.5 first:rounded-l-md last:rounded-r-md ${
                  g === group
                    ? "bg-surface-2 text-ink"
                    : "text-ink-dim hover:text-ink"
                }`}
              >
                {g}
              </button>
            ))}
          </div>
        </FilterBar>
      </div>

      {budget.data && (
        <BudgetBanner
          spent={budget.data.spentUSD}
          monthly={budget.data.monthlyUSD}
          month={budget.data.month}
        />
      )}

      {group === "blocks" ? (
        <BlocksTable blocks={blocks.data ?? []} loading={blocks.isLoading} />
      ) : (
        <>
          <Suspense fallback={null}>
            {group === "day" ? (
              <CostTimeline
                since={since}
                until={inclusiveUntil(until)}
                agent={agent}
                model={model}
              />
            ) : (
              <GroupBars rows={rows} group={group} />
            )}
          </Suspense>
          {error && (
            <p className="text-warn">Failed to load: {String(error)}</p>
          )}
          {isLoading && <SkeletonRows rows={6} className="mb-4" />}
          {!isLoading && rows.length === 0 && (
            <p className="text-ink-dim">No usage recorded yet.</p>
          )}

          <div className="overflow-hidden rounded-lg border border-edge">
            <table className="w-full text-sm">
              <thead className="bg-surface-2 text-left text-xs uppercase tracking-wide text-ink-dim">
                <tr>
                  <SortableTH
                    label={group}
                    k="group"
                    sort={sort}
                    onSort={toggleSort}
                  />
                  <SortableTH
                    label="sessions"
                    k="sessions"
                    sort={sort}
                    onSort={toggleSort}
                    right
                  />
                  <SortableTH
                    label="tokens"
                    k="tokens"
                    sort={sort}
                    onSort={toggleSort}
                    right
                  />
                  <SortableTH
                    label="cache read"
                    k="cacheRead"
                    sort={sort}
                    onSort={toggleSort}
                    right
                  />
                  <SortableTH
                    label="cost"
                    k="cost"
                    sort={sort}
                    onSort={toggleSort}
                    right
                  />
                  <th className="w-1/3 px-4 py-2 font-normal normal-case">
                    <span className="inline-flex items-center gap-1.5">
                      <span className="inline-block h-2 w-2 rounded-[2px] bg-accent/70" />
                      reported
                      <span className="ml-2 inline-block h-2 w-2 rounded-[2px] bg-accent/30" />
                      estimated
                    </span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge bg-surface-1">
                {rows.map((r) => (
                  <tr key={r.group || "(none)"}>
                    <td className="px-4 py-2 font-mono text-xs">
                      {!r.group ? (
                        <span className="text-ink-dim">(no {group})</span>
                      ) : (
                        <Link
                          to="/sessions"
                          search={pivotSearch(group, r.group, {
                            agent,
                            model,
                            since,
                            until,
                          })}
                          title="Sessions matching this row (and the active filters)"
                          className="hover:text-accent"
                        >
                          {r.group}
                        </Link>
                      )}
                      {r.hasUnpriced && (
                        <span
                          className="ml-2 text-warn"
                          title="Contains unpriced tokens"
                        >
                          ●
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums">
                      {r.sessions}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums">
                      {fmtTokens(totalTokens(r.tokens))}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums text-ink-dim">
                      {fmtTokens(r.tokens.cacheRead)}
                    </td>
                    <td
                      className="px-4 py-2 text-right tabular-nums text-ok"
                      title={`reported ${fmtCost(r.costReportedUSD)} · estimated ${fmtCost(r.costEstimatedUSD)}`}
                    >
                      {fmtCost(r.costUSD)}
                    </td>
                    <td
                      className="px-4 py-2"
                      onMouseEnter={(e) =>
                        tooltip.show(
                          e,
                          <>
                            <span className="text-ink">{r.group || group}</span>
                            <br />
                            reported{" "}
                            <span className="text-ok">
                              {fmtCost(r.costReportedUSD)}
                            </span>
                            {" · "}estimated{" "}
                            <span className="text-ok">
                              {fmtCost(r.costEstimatedUSD)}
                            </span>
                          </>,
                        )
                      }
                      onMouseLeave={tooltip.hide}
                    >
                      <div
                        className="flex h-2 gap-[1px] overflow-hidden rounded"
                        style={{
                          width: `${Math.max((r.costUSD / maxCost) * 100, 1)}%`,
                        }}
                      >
                        {r.costReportedUSD > 0 && (
                          <div
                            className="bg-accent/70"
                            style={{
                              width: `${(r.costReportedUSD / (r.costUSD || 1)) * 100}%`,
                            }}
                          />
                        )}
                        {r.costEstimatedUSD > 0 && (
                          <div
                            className="bg-accent/30"
                            style={{
                              width: `${(r.costEstimatedUSD / (r.costUSD || 1)) * 100}%`,
                            }}
                          />
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      {tooltip.node}
    </div>
  );
}

function SortableTH<K extends string>({
  label,
  k,
  sort,
  onSort,
  right,
}: {
  label: string;
  k: K;
  sort: { key: K; desc: boolean } | null;
  onSort: (k: K) => void;
  right?: boolean;
}) {
  const active = sort?.key === k;
  return (
    <th
      aria-sort={active ? (sort.desc ? "descending" : "ascending") : undefined}
      className={`px-4 py-2 ${right ? "text-right" : ""}`}
    >
      <button
        onClick={() => onSort(k)}
        className={`inline-flex items-center gap-1 uppercase hover:text-ink ${
          active ? "text-ink" : ""
        }`}
      >
        {label}
        <span className="text-[9px]">
          {active ? (sort.desc ? "▼" : "▲") : "△"}
        </span>
      </button>
    </th>
  );
}

// BudgetEditor makes the budget mutation reachable from the product,
// not just the API: set, change, or clear (0) the monthly figure.
function BudgetEditor({ monthly }: { monthly: number }) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(String(monthly || ""));
  const qc = useQueryClient();
  const save = useMutation({
    mutationFn: (usd: number) => parityApi.setBudget(usd),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["budget"] });
      setEditing(false);
    },
  });
  if (!editing) {
    return (
      <button
        onClick={() => {
          setValue(String(monthly || ""));
          setEditing(true);
        }}
        className="ml-auto shrink-0 font-mono text-[11px] text-ink-faint hover:text-accent"
      >
        {monthly > 0 ? "edit budget" : "set monthly budget"}
      </button>
    );
  }
  return (
    <form
      className="ml-auto flex shrink-0 items-center gap-1.5"
      onSubmit={(e) => {
        e.preventDefault();
        const usd = Number(value);
        if (!Number.isNaN(usd) && usd >= 0) save.mutate(usd);
      }}
    >
      <span className="font-mono text-[11px] text-ink-faint">$/month</span>
      <input
        autoFocus
        value={value}
        onChange={(e) => setValue(e.target.value)}
        inputMode="decimal"
        className="w-20 rounded border border-edge bg-surface-1 px-1.5 py-0.5 text-right font-mono text-xs tabular-nums"
        aria-label="Monthly budget in USD"
      />
      <button
        type="submit"
        disabled={save.isPending}
        className="rounded border border-edge px-1.5 py-0.5 font-mono text-[11px] text-ink-dim hover:text-ink disabled:opacity-50"
      >
        {save.isPending ? "…" : "save"}
      </button>
      <button
        type="button"
        onClick={() => setEditing(false)}
        className="font-mono text-[11px] text-ink-faint hover:text-ink"
      >
        cancel
      </button>
    </form>
  );
}

function BudgetBanner({
  spent,
  monthly,
  month,
}: {
  spent: number;
  monthly: number;
  month: string;
}) {
  // No budget configured: a quiet affordance to set one, nothing else.
  if (monthly <= 0) {
    return (
      <div className="mb-4 flex items-baseline rounded-lg border border-edge bg-surface-1 px-4 py-2 text-sm text-ink-dim">
        <span>No monthly budget set.</span>
        <BudgetEditor monthly={0} />
      </div>
    );
  }
  const pct = Math.min((spent / monthly) * 100, 100);
  const over = spent > monthly;
  const near = !over && pct >= 80;
  return (
    <div
      className={`mb-4 rounded-lg border px-4 py-3 text-sm ${
        over
          ? "border-warn bg-warn/10 text-warn"
          : near
            ? "border-warn/40 bg-warn/5"
            : "border-edge bg-surface-1"
      }`}
    >
      <div className="mb-1 flex items-baseline gap-2">
        <span>
          {month} budget: {fmtCost(spent)} of {fmtCost(monthly)}
        </span>
        <span className="ml-auto tabular-nums">{pct.toFixed(0)}%</span>
        <BudgetEditor monthly={monthly} />
      </div>
      <div className="h-2 overflow-hidden rounded bg-surface-2">
        <div
          className={`h-full ${over || near ? "bg-warn" : "bg-ok"}`}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
}

type BlockSortKey = "window" | "sessions" | "tokens" | "cost";

const BLOCK_SORT_VALUE: Record<
  BlockSortKey,
  (b: import("../api").BlockRow) => number | string
> = {
  window: (b) => b.start,
  sessions: (b) => b.sessions,
  tokens: (b) => totalTokens(b.tokens),
  cost: (b) => b.costUSD,
};

function BlocksTable({
  blocks,
  loading,
}: {
  blocks: import("../api").BlockRow[];
  loading: boolean;
}) {
  const [sort, setSort] = useState<{
    key: BlockSortKey;
    desc: boolean;
  } | null>(null);
  const toggleSort = (key: BlockSortKey) =>
    setSort((prev) =>
      prev?.key === key ? { key, desc: !prev.desc } : { key, desc: true },
    );
  const sorted = useMemo(() => {
    if (!sort) return blocks;
    const value = BLOCK_SORT_VALUE[sort.key];
    return blocks.toSorted((a, b) => {
      const av = value(a);
      const bv = value(b);
      const cmp =
        typeof av === "number" && typeof bv === "number"
          ? av - bv
          : String(av).localeCompare(String(bv));
      return sort.desc ? -cmp : cmp;
    });
  }, [blocks, sort]);

  if (loading) return <p className="text-ink-dim">Loading…</p>;
  if (blocks.length === 0)
    return <p className="text-ink-dim">No usage recorded yet.</p>;
  const maxTokens = Math.max(...blocks.map((b) => totalTokens(b.tokens)), 1);
  return (
    <div className="overflow-hidden rounded-lg border border-edge">
      <table className="w-full text-sm">
        <thead className="bg-surface-2 text-left text-xs uppercase tracking-wide text-ink-dim">
          <tr>
            <SortableTH
              label="5h window (UTC)"
              k="window"
              sort={sort}
              onSort={toggleSort}
            />
            <SortableTH
              label="sessions"
              k="sessions"
              sort={sort}
              onSort={toggleSort}
              right
            />
            <SortableTH
              label="tokens"
              k="tokens"
              sort={sort}
              onSort={toggleSort}
              right
            />
            <SortableTH
              label="cost"
              k="cost"
              sort={sort}
              onSort={toggleSort}
              right
            />
            <th className="w-1/3 px-4 py-2"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-edge bg-surface-1">
          {sorted.map((b) => (
            <tr key={b.start} className={b.active ? "bg-surface-2/50" : ""}>
              <td className="px-4 py-2 font-mono text-xs">
                {b.start.slice(0, 16).replace("T", " ")} – {b.end.slice(11, 16)}
                {b.active && (
                  <span className="ml-2 rounded bg-ok/20 px-1.5 text-ok">
                    active
                  </span>
                )}
                {(b.unpricedTokens ?? 0) > 0 && (
                  <span
                    className="ml-2 text-warn"
                    title="Contains unpriced tokens"
                  >
                    ●
                  </span>
                )}
              </td>
              <td className="px-4 py-2 text-right tabular-nums">
                {b.sessions}
              </td>
              <td className="px-4 py-2 text-right tabular-nums">
                {fmtTokens(totalTokens(b.tokens))}
              </td>
              <td className="px-4 py-2 text-right tabular-nums text-ok">
                {fmtCost(b.costUSD)}
              </td>
              <td className="px-4 py-2">
                <div
                  className="h-2 rounded bg-accent/70"
                  style={{
                    width: `${Math.max((totalTokens(b.tokens) / maxTokens) * 100, 1)}%`,
                  }}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
