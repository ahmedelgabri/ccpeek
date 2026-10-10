import { lazy, Suspense } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, fmtCount, fmtTokens, type InsightFilters } from "../../api";
import {
  AgentChip,
  EmptyNote,
  LoadError,
  Money,
  Panel,
  SkeletonRows,
  StatTile,
} from "../../ui";
import { levelLabel, levelRank } from "./levels";
import { TABLE_BODY, TableHead } from "./table";

// ECharts loads as its own chunk, as on the Usage page.
const EffortTimeline = lazy(() =>
  import("../../EffortTimeline").then((m) => ({ default: m.EffortTimeline })),
);

// EffortTab: what each reasoning effort or thinking level cost, and how
// the levels were used day by day.
export function EffortTab({ f }: { f: InsightFilters }) {
  const byLevel = useQuery({
    queryKey: ["effort", "level", f],
    queryFn: () => api.effort({ ...f, group: "level" }),
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
      <Suspense fallback={null}>
        <EffortTimeline {...f} />
      </Suspense>
      <Panel label="By level">
        {rows.length === 0 ? (
          <EmptyNote>No model requests in this range.</EmptyNote>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <TableHead
                labels={["agent", "level"]}
                numbers={["requests", "output", "cost", "per request"]}
              />
              <tbody className={TABLE_BODY}>
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
