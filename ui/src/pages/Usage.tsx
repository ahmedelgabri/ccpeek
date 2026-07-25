import { lazy, Suspense, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  api,
  fmtCost,
  fmtCostExact,
  fmtCount,
  fmtTokens,
  parityApi,
  shortPath,
  totalTokens,
} from "../api";
import {
  EmptyNote,
  FilterBar,
  LoadError,
  Money,
  PageHeader,
  Panel,
  Segmented,
  SkeletonRows,
  useTooltip,
} from "../ui";

// Lazy so echarts ships as its own chunk, loaded only on this page.
const CostTimeline = lazy(() =>
  import("../CostTimeline").then((m) => ({ default: m.CostTimeline })),
);

const GROUPS = ["day", "model", "project", "agent", "blocks"] as const;
type Group = (typeof GROUPS)[number];

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

/** groupLabel renders a rollup key for reading. Project keys are absolute
 *  paths, and the table printed them raw while the chart beside it showed
 *  the same row shortened — two spellings of one workspace, one above the
 *  other. */
function groupLabel(group: string, value: string): string {
  return group === "project" ? shortPath(value) : value;
}

// useSorted is the sortable-table state machine: click a column to sort
// by it, click again to reverse. `values` maps each sort key to the field
// it reads, so one comparator (numeric where both sides are numbers,
// locale-aware otherwise) serves every table. SortableTH is already
// generic over the key type, so the two tables here share both halves.
function useSorted<T, K extends string>(
  rows: T[],
  values: Record<K, (row: T) => number | string>,
) {
  const [sort, setSort] = useState<{ key: K; desc: boolean } | null>(null);
  const toggleSort = (key: K) =>
    setSort((prev) =>
      prev?.key === key ? { key, desc: !prev.desc } : { key, desc: true },
    );
  const sorted = useMemo(() => {
    if (!sort) return rows;
    const value = values[sort.key];
    return rows.toSorted((a, b) => {
      const av = value(a);
      const bv = value(b);
      const cmp =
        typeof av === "number" && typeof bv === "number"
          ? av - bv
          : String(av).localeCompare(String(bv));
      return sort.desc ? -cmp : cmp;
    });
  }, [rows, sort, values]);
  return { sorted, sort, toggleSort };
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

// Cost explorer: one persistent timeline over the active filters, and one
// table beneath it whose grouping the reader chooses. The grouping used to
// swap the CHART too — three different graphs appearing and disappearing
// under one control — which meant the page never held still and the
// single-category cases rendered a 270px panel to show one number that
// was already in the row below.
export function UsagePage() {
  const [group, setGroup] = useState<Group>("day");
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");
  const [agent, setAgent] = useState("");
  const [model, setModel] = useState("");
  const isBlocks = group === "blocks";

  // No limit: usage is an aggregate surface and the server returns all
  // groups by default, so the page total, charts, and CSV are complete
  // by construction (the old fixed 1000 silently truncated past it).
  const { data, isLoading, error } = useQuery({
    queryKey: ["usage", group, since, until, agent, model],
    queryFn: () => api.usage({ group, since, until, agent, model }),
    enabled: !isBlocks,
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
    enabled: isBlocks,
  });
  const budget = useQuery({
    queryKey: ["budget"],
    queryFn: () => parityApi.budget(),
  });

  // Sorting: null keeps the server order (day desc / cost desc); a click
  // cycles asc↔desc per column.
  const unsorted = useMemo(
    () => (isBlocks ? [] : (data ?? [])),
    [data, isBlocks],
  );
  const { sorted: rows, sort, toggleSort } = useSorted(unsorted, SORT_VALUE);
  const maxCost = Math.max(...rows.map((r) => r.costUSD), 0.000001);

  // The headline total is the sum of WHAT IS ON SCREEN. It used to come
  // from the rollup query alone, which is disabled on the blocks view but
  // keeps its placeholder data — so the header showed the previous
  // grouping's total above a table that added up to something else.
  const blockRows = blocks.data ?? [];
  const total = isBlocks
    ? blockRows.reduce((acc, b) => acc + b.costUSD, 0)
    : rows.reduce((acc, r) => acc + r.costUSD, 0);
  const anyUnpriced = isBlocks
    ? blockRows.some((b) => (b.unpricedTokens ?? 0) > 0)
    : rows.some((r) => r.hasUnpriced);

  const tooltip = useTooltip();
  const models = (modelRows.data ?? [])
    .map((r) => r.group)
    .filter((m) => m !== "");

  return (
    <div>
      <PageHeader
        title="Usage"
        lede={
          <span className="flex items-baseline gap-1.5">
            <Money
              usd={total}
              unpriced={anyUnpriced ? 1 : undefined}
              className="text-sm"
            />
            <span className="font-mono text-meta text-ink-faint">
              {isBlocks ? "across shown windows" : "over the current filters"}
            </span>
          </span>
        }
      >
        <FilterBar
          since={isBlocks ? undefined : since}
          until={isBlocks ? undefined : until}
          onRange={
            isBlocks
              ? undefined
              : (sv, uv) => {
                  setSince(sv);
                  setUntil(uv);
                }
          }
          agent={agent}
          onAgent={setAgent}
          model={isBlocks ? undefined : model}
          models={isBlocks ? undefined : models}
          onModel={isBlocks ? undefined : setModel}
        />
      </PageHeader>

      {budget.data && (
        <BudgetBanner
          spent={budget.data.spentUSD}
          monthly={budget.data.monthlyUSD}
          month={budget.data.month}
        />
      )}

      <Suspense fallback={null}>
        <CostTimeline
          since={isBlocks ? "" : since}
          until={isBlocks ? "" : until}
          agent={agent}
          model={isBlocks ? "" : model}
        />
      </Suspense>

      <Panel
        label={isBlocks ? "Rolling 5-hour windows" : `Grouped by ${group}`}
        action={
          <div className="flex items-center gap-3">
            {!isBlocks && <CostSplitLegend />}
            <Segmented
              label="Group usage by"
              value={group}
              options={GROUPS.map((g) => ({ value: g, label: g }))}
              onChange={setGroup}
            />
          </div>
        }
      >
        {isBlocks ? (
          <BlocksTable blocks={blockRows} loading={blocks.isLoading} />
        ) : (
          <>
            {error && (
              <div className="px-3 py-3">
                <LoadError error={error} />
              </div>
            )}
            {isLoading && <SkeletonRows rows={6} />}
            {!isLoading && !error && rows.length === 0 && (
              <EmptyNote>No usage in this range.</EmptyNote>
            )}
            {rows.length > 0 && (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead className="border-b border-edge bg-surface-2 text-left">
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
                      {/* The reported/estimated split is a legend, and a
                          legend is not a column heading — it lives in the
                          panel header now. */}
                      <th className="w-1/3 px-4 py-2 microlabel">
                        cost split
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
                              title={`Sessions matching this row (and the active filters)\n${r.group}`}
                              className="hover:text-accent"
                            >
                              {groupLabel(group, r.group)}
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
                        <td className="px-4 py-2 text-right font-mono text-xs tabular-nums">
                          {fmtCount(r.sessions)}
                        </td>
                        <td className="px-4 py-2 text-right font-mono text-xs tabular-nums">
                          {fmtTokens(totalTokens(r.tokens))}
                        </td>
                        <td className="px-4 py-2 text-right font-mono text-xs text-ink-dim tabular-nums">
                          {fmtTokens(r.tokens.cacheRead)}
                        </td>
                        <td className="px-4 py-2 text-right">
                          <Money
                            usd={r.costUSD}
                            unpriced={r.hasUnpriced ? 1 : undefined}
                            className="text-xs"
                            title={`reported ${fmtCostExact(r.costReportedUSD)} · estimated ${fmtCostExact(r.costEstimatedUSD)}`}
                          />
                        </td>
                        <td
                          className="px-4 py-2"
                          tabIndex={0}
                          {...tooltip.bind(
                            <>
                              <span className="text-ink">
                                {groupLabel(group, r.group) || group}
                              </span>
                              <br />
                              reported {fmtCostExact(r.costReportedUSD)}
                              <br />
                              estimated {fmtCostExact(r.costEstimatedUSD)}
                            </>,
                          )}
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
            )}
          </>
        )}
      </Panel>
      {tooltip.node}
    </div>
  );
}

function CostSplitLegend() {
  return (
    <span className="hidden items-center gap-1.5 font-mono text-micro text-ink-faint sm:inline-flex">
      <span className="inline-block h-2 w-2 rounded-[2px] bg-accent/70" />
      reported
      <span className="ml-1 inline-block h-2 w-2 rounded-[2px] bg-accent/30" />
      estimated
    </span>
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
        type="button"
        onClick={() => onSort(k)}
        className={`microlabel inline-flex items-center gap-1 hover:text-ink ${
          active ? "text-ink" : ""
        }`}
      >
        {label}
        {/* Only the active column carries a marker: a triangle on every
            heading is five pieces of chrome saying nothing. */}
        {active && <span aria-hidden>{sort.desc ? "▼" : "▲"}</span>}
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
        type="button"
        onClick={() => {
          setValue(String(monthly || ""));
          setEditing(true);
        }}
        className="ml-auto shrink-0 font-mono text-meta text-ink-faint hover:text-accent"
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
      <span className="font-mono text-meta text-ink-faint">$/month</span>
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
        className="rounded border border-edge px-1.5 py-0.5 font-mono text-meta text-ink-dim hover:text-ink disabled:opacity-50"
      >
        {save.isPending ? "…" : "save"}
      </button>
      <button
        type="button"
        onClick={() => setEditing(false)}
        className="font-mono text-meta text-ink-faint hover:text-ink"
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
      <div className="mb-4 flex items-baseline rounded-md border border-edge bg-surface-1 px-4 py-2 text-sm text-ink-dim">
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
      className={`mb-4 rounded-md border px-4 py-3 text-sm ${
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
        <span className="ml-auto font-mono tabular-nums">
          {pct.toFixed(0)}%
        </span>
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
  const { sorted, sort, toggleSort } = useSorted(blocks, BLOCK_SORT_VALUE);

  if (loading) return <SkeletonRows rows={5} />;
  if (blocks.length === 0)
    return <EmptyNote>No usage recorded yet.</EmptyNote>;
  const maxTokens = Math.max(...blocks.map((b) => totalTokens(b.tokens)), 1);
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead className="border-b border-edge bg-surface-2 text-left">
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
            {/* The bar scales by TOKENS. It used to sit unlabelled beside
                the cost column, where the only available reading was that
                it showed cost — which it never did. */}
            <th className="w-1/3 px-4 py-2 microlabel">token share</th>
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
              <td className="px-4 py-2 text-right font-mono text-xs tabular-nums">
                {fmtCount(b.sessions)}
              </td>
              <td className="px-4 py-2 text-right font-mono text-xs tabular-nums">
                {fmtTokens(totalTokens(b.tokens))}
              </td>
              <td className="px-4 py-2 text-right">
                <Money
                  usd={b.costUSD}
                  unpriced={b.unpricedTokens}
                  className="text-xs"
                />
              </td>
              <td className="px-4 py-2">
                <div
                  className="h-2 rounded bg-accent/70"
                  title={`${fmtTokens(totalTokens(b.tokens))} tokens`}
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
