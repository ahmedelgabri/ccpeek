import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, fmtCost, fmtTokens, totalTokens } from "../api";

const GROUPS = ["day", "model", "project", "agent"] as const;

// Cost explorer v0: rollup aggregates with CSS bars. The richer
// brush/zoom explorer (ECharts) layers on top of the same endpoint.
export function UsagePage() {
  const [group, setGroup] = useState<(typeof GROUPS)[number]>("day");

  const { data, isLoading, error } = useQuery({
    queryKey: ["usage", group],
    queryFn: () => api.usage({ group }),
  });

  const rows = data ?? [];
  const maxCost = Math.max(...rows.map((r) => r.costUSD), 0.000001);
  const total = rows.reduce((acc, r) => acc + r.costUSD, 0);
  const anyUnpriced = rows.some((r) => r.hasUnpriced);

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-semibold">Usage</h1>
        <span className="text-sm text-ok tabular-nums">
          {fmtCost(total)} {anyUnpriced && <span className="text-warn">(+unpriced)</span>}
        </span>
        <div className="ml-auto flex rounded-md border border-edge text-sm">
          {GROUPS.map((g) => (
            <button
              key={g}
              onClick={() => setGroup(g)}
              className={`px-3 py-1.5 first:rounded-l-md last:rounded-r-md ${
                g === group ? "bg-surface-2 text-ink" : "text-ink-dim hover:text-ink"
              }`}
            >
              {g}
            </button>
          ))}
        </div>
      </div>

      {error && <p className="text-warn">Failed to load: {String(error)}</p>}
      {isLoading && <p className="text-ink-dim">Loading…</p>}
      {!isLoading && rows.length === 0 && (
        <p className="text-ink-dim">No usage recorded yet.</p>
      )}

      <div className="overflow-hidden rounded-lg border border-edge">
        <table className="w-full text-sm">
          <thead className="bg-surface-2 text-left text-xs uppercase tracking-wide text-ink-dim">
            <tr>
              <th className="px-4 py-2">{group}</th>
              <th className="px-4 py-2 text-right">sessions</th>
              <th className="px-4 py-2 text-right">tokens</th>
              <th className="px-4 py-2 text-right">cache read</th>
              <th className="px-4 py-2 text-right">cost</th>
              <th className="w-1/3 px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge bg-surface-1">
            {rows.map((r) => (
              <tr key={r.group || "(none)"}>
                <td className="px-4 py-2 font-mono text-xs">
                  {r.group || <span className="text-ink-dim">(no {group})</span>}
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
                <td className="px-4 py-2 text-right tabular-nums text-ok">
                  {fmtCost(r.costUSD)}
                </td>
                <td className="px-4 py-2">
                  <div
                    className="h-2 rounded bg-accent/70"
                    style={{ width: `${Math.max((r.costUSD / maxCost) * 100, 1)}%` }}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
