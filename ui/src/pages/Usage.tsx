import { lazy, Suspense, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  api,
  fmtCost,
  fmtTokens,
  inclusiveUntil,
  parityApi,
  totalTokens,
} from "../api";
import { FilterBar, SkeletonRows } from "../ui";

// Lazy so echarts ships as its own chunk, loaded only on this page.
const CostTimeline = lazy(() =>
  import("../CostTimeline").then((m) => ({ default: m.CostTimeline })),
);
const GroupBars = lazy(() =>
  import("../CostTimeline").then((m) => ({ default: m.GroupBars })),
);

const GROUPS = ["day", "model", "project", "agent", "blocks"] as const;

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

  const { data, isLoading, error } = useQuery({
    queryKey: ["usage", group, since, until, agent, model],
    queryFn: () =>
      api.usage({ group, since, until: inclusiveUntil(until), agent, model }),
    enabled: group !== "blocks",
    placeholderData: (prev) => prev,
  });
  // Model options come from the unfiltered model rollup.
  const modelRows = useQuery({
    queryKey: ["usage", "model-options"],
    queryFn: () => api.usage({ group: "model" }),
  });
  const blocks = useQuery({
    queryKey: ["blocks"],
    queryFn: () => parityApi.blocks(36),
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
    return [...unsorted].sort((a, b) => {
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
          since={since}
          until={until}
          onRange={(sv, uv) => {
            setSince(sv);
            setUntil(uv);
          }}
          agent={agent}
          onAgent={setAgent}
          model={model}
          models={models}
          onModel={setModel}
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

      {budget.data && budget.data.monthlyUSD > 0 && (
        <BudgetBanner
          spent={budget.data.spentUSD}
          monthly={budget.data.monthlyUSD}
          month={budget.data.month}
        />
      )}

      {group === "blocks" ? (
        <BlocksTable
          blocks={blocks.data ?? []}
          loading={blocks.isLoading}
        />
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
          {error && <p className="text-warn">Failed to load: {String(error)}</p>}
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
                  ) : group === "agent" ? (
                    <Link
                      to="/sessions"
                      search={{ agent: r.group }}
                      className="hover:text-accent"
                    >
                      {r.group}
                    </Link>
                  ) : group === "project" ? (
                    <Link
                      to="/sessions"
                      search={{ project: r.group }}
                      className="hover:text-accent"
                    >
                      {r.group}
                    </Link>
                  ) : (
                    r.group
                  )}
                  {r.hasUnpriced && (
                    <span className="ml-2 text-warn" title="Contains unpriced tokens">
                      ●
                    </span>
                  )}
                </td>
                <td className="px-4 py-2 text-right tabular-nums">{r.sessions}</td>
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
                <td className="px-4 py-2">
                  <div
                    className="flex h-2 gap-[1px] overflow-hidden rounded"
                    style={{ width: `${Math.max((r.costUSD / maxCost) * 100, 1)}%` }}
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
    </div>
  );
}

function SortableTH({
  label,
  k,
  sort,
  onSort,
  right,
}: {
  label: string;
  k: SortKey;
  sort: { key: SortKey; desc: boolean } | null;
  onSort: (k: SortKey) => void;
  right?: boolean;
}) {
  const active = sort?.key === k;
  return (
    <th
      aria-sort={
        active ? (sort.desc ? "descending" : "ascending") : undefined
      }
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

function BudgetBanner({
  spent,
  monthly,
  month,
}: {
  spent: number;
  monthly: number;
  month: string;
}) {
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

function BlocksTable({
  blocks,
  loading,
}: {
  blocks: import("../api").BlockRow[];
  loading: boolean;
}) {
  if (loading) return <p className="text-ink-dim">Loading…</p>;
  if (blocks.length === 0)
    return <p className="text-ink-dim">No usage recorded yet.</p>;
  const maxTokens = Math.max(...blocks.map((b) => totalTokens(b.tokens)), 1);
  return (
    <div className="overflow-hidden rounded-lg border border-edge">
      <table className="w-full text-sm">
        <thead className="bg-surface-2 text-left text-xs uppercase tracking-wide text-ink-dim">
          <tr>
            <th className="px-4 py-2">5h window (UTC)</th>
            <th className="px-4 py-2 text-right">sessions</th>
            <th className="px-4 py-2 text-right">tokens</th>
            <th className="px-4 py-2 text-right">cost</th>
            <th className="w-1/3 px-4 py-2"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-edge bg-surface-1">
          {blocks.map((b) => (
            <tr key={b.start} className={b.active ? "bg-surface-2/50" : ""}>
              <td className="px-4 py-2 font-mono text-xs">
                {b.start.slice(0, 16).replace("T", " ")} –{" "}
                {b.end.slice(11, 16)}
                {b.active && (
                  <span className="ml-2 rounded bg-ok/20 px-1.5 text-ok">
                    active
                  </span>
                )}
                {(b.unpricedTokens ?? 0) > 0 && (
                  <span className="ml-2 text-warn" title="Contains unpriced tokens">
                    ●
                  </span>
                )}
              </td>
              <td className="px-4 py-2 text-right tabular-nums">{b.sessions}</td>
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
