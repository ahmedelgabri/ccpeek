import { useQuery } from "@tanstack/react-query";
import {
  api,
  fmtCount,
  fmtTokens,
  type EffortRow,
  type InsightFilters,
} from "../../api";
import {
  AgentChip,
  EmptyNote,
  LoadError,
  Money,
  Panel,
  SkeletonRows,
  StatTile,
} from "../../ui";

// Effort levels are ordinal, so they share one hue stepping from light to
// dark; "not recorded" is the neutral gray outside the scale. Unknown
// levels sort before the known ones.
const LEVELS = ["low", "medium", "high", "xhigh", "max"];
const levelRank = (l: string) => (l === "" ? 99 : LEVELS.indexOf(l));
const levelLabel = (l: string) => l || "not recorded";
function levelFill(l: string): string {
  const i = LEVELS.indexOf(l);
  if (l === "") return "var(--color-edge-strong)";
  const pct = i < 0 ? 50 : 20 + i * 20;
  return `color-mix(in oklab, var(--color-accent) ${pct}%, var(--color-surface-1))`;
}

// EffortTab: what each reasoning effort or thinking level cost, and how
// the levels were used day by day.
export function EffortTab({ f }: { f: InsightFilters }) {
  const byLevel = useQuery({
    queryKey: ["effort", "level", f],
    queryFn: () => api.effort({ ...f, group: "level" }),
  });
  const byDay = useQuery({
    queryKey: ["effort", "day", f],
    queryFn: () => api.effort({ ...f, group: "day" }),
  });
  if (byLevel.error) return <LoadError error={byLevel.error} />;
  if (byLevel.isLoading) return <SkeletonRows rows={6} />;
  const rows = [...(byLevel.data ?? [])].toSorted(
    (a, b) =>
      (a.agent ?? "").localeCompare(b.agent ?? "") ||
      levelRank(a.level) - levelRank(b.level),
  );
  const recorded = rows.filter((r) => r.level !== "");
  const recordedRequests = recorded.reduce((n, r) => n + r.requests, 0);
  const allRequests = rows.reduce((n, r) => n + r.requests, 0);
  const perLevel = new Map<string, number>();
  for (const r of recorded)
    perLevel.set(r.level, (perLevel.get(r.level) ?? 0) + r.requests);
  const top = [...perLevel].toSorted((a, b) => b[1] - a[1])[0];
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile
          label="Requests with a level"
          value={fmtCount(recordedRequests)}
          detail={`of ${fmtCount(allRequests)}`}
        />
        <StatTile label="Most used" value={top ? top[0] : "—"} />
        <StatTile label="Levels seen" value={fmtCount(perLevel.size)} />
        <StatTile
          label="Cost at a recorded level"
          value={
            <Money
              usd={recorded.reduce((n, r) => n + r.costUSD, 0)}
              incomplete={recorded.some((r) => (r.unpricedTokens ?? 0) > 0)}
            />
          }
        />
      </div>
      <Panel label="Requests per day by level">
        {byDay.error ? (
          <div className="px-3 py-3">
            <LoadError error={byDay.error} />
          </div>
        ) : (
          <DailyLevels rows={byDay.data ?? []} />
        )}
      </Panel>
      <Panel label="By level">
        {rows.length === 0 ? (
          <EmptyNote>No model requests in this range.</EmptyNote>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left font-mono text-micro text-ink-faint">
                <tr>
                  <th className="px-3 py-1.5 font-normal">agent</th>
                  <th className="px-3 py-1.5 font-normal">level</th>
                  <th className="px-3 py-1.5 text-right font-normal">
                    requests
                  </th>
                  <th className="px-3 py-1.5 text-right font-normal">output</th>
                  <th className="px-3 py-1.5 text-right font-normal">cost</th>
                  <th className="px-3 py-1.5 text-right font-normal">
                    per request
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge font-mono text-xs tabular-nums">
                {rows.map((r) => (
                  <tr key={`${r.agent}/${r.level}`}>
                    <td className="px-3 py-1.5">
                      <AgentChip agent={r.agent ?? ""} />
                    </td>
                    <td
                      className={`px-3 py-1.5 ${r.level ? "" : "text-ink-faint"}`}
                    >
                      {levelLabel(r.level)}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      {fmtCount(r.requests)}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      {fmtTokens(r.tokens.output)}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      <Money
                        usd={r.costUSD}
                        incomplete={(r.unpricedTokens ?? 0) > 0}
                        title={`$${r.costUSDExact}`}
                      />
                    </td>
                    <td className="px-3 py-1.5 text-right text-ink-faint">
                      <Money
                        usd={r.requests ? r.costUSD / r.requests : 0}
                        incomplete={(r.unpricedTokens ?? 0) > 0}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  );
}

const MAX_DAYS = 60;

// DailyLevels stacks each day's requests by level, oldest left. Columns
// are capped at 24px and the most recent MAX_DAYS days are shown; the By
// level table is the full accessible view.
function DailyLevels({ rows }: { rows: EffortRow[] }) {
  const days = [...new Set(rows.map((r) => r.day ?? ""))]
    .filter(Boolean)
    .toSorted()
    .slice(-MAX_DAYS);
  if (days.length === 0)
    return <EmptyNote>No model requests in this range.</EmptyNote>;
  const levels = [...new Set(rows.map((r) => r.level))].toSorted(
    (a, b) => levelRank(a) - levelRank(b),
  );
  const cell = new Map(rows.map((r) => [`${r.day}/${r.level}`, r.requests]));
  const totals = days.map((d) =>
    levels.reduce((n, l) => n + (cell.get(`${d}/${l}`) ?? 0), 0),
  );
  const max = Math.max(...totals, 1);
  const H = 140;
  return (
    <div className="px-3 py-2.5">
      <div className="mb-2 flex flex-wrap gap-3 font-mono text-micro text-ink-dim">
        {levels.map((l) => (
          <span key={l || "none"} className="inline-flex items-center gap-1.5">
            <span
              aria-hidden
              className="inline-block h-2 w-3 rounded-sm"
              style={{ background: levelFill(l) }}
            />
            {levelLabel(l)}
          </span>
        ))}
      </div>
      <div
        className="flex items-end gap-0.5 overflow-x-auto"
        style={{ height: H }}
        role="img"
        aria-label={`Requests per day by effort level over ${days.length} days; the By level table lists the totals.`}
      >
        {days.map((d, i) => (
          <div
            key={d}
            className="flex w-full max-w-6 min-w-1.5 flex-col-reverse gap-0.5"
            style={{ height: (totals[i] / max) * H }}
          >
            {levels.map((l) => {
              const n = cell.get(`${d}/${l}`) ?? 0;
              if (n === 0) return null;
              return (
                <div
                  key={l || "none"}
                  title={`${d} · ${levelLabel(l)} · ${fmtCount(n)} requests`}
                  className="w-full first:rounded-b-none last:rounded-t-sm"
                  style={{
                    height: `${(n / totals[i]) * 100}%`,
                    background: levelFill(l),
                  }}
                />
              );
            })}
          </div>
        ))}
      </div>
      <div className="mt-1 flex justify-between font-mono text-micro text-ink-faint">
        <span>{days[0]}</span>
        <span>{days[days.length - 1]}</span>
      </div>
    </div>
  );
}
