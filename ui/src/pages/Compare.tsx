import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, fmtCost, fmtTokens, type SessionDetail } from "../api";

// Session compare: any two sessions, any agents — v1 could only compare
// within one Claude project.
export function ComparePage() {
  const [a, setA] = useState("");
  const [b, setB] = useState("");

  const sessions = useQuery({
    queryKey: ["sessions", "", ""],
    queryFn: () => api.sessions({ limit: "200" }),
  });

  const list = sessions.data ?? [];
  const [agentA, idA] = a.split("|");
  const [agentB, idB] = b.split("|");
  const left = useQueryDetail(agentA, idA);
  const right = useQueryDetail(agentB, idB);

  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Compare sessions</h1>
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-2">
        {[
          { value: a, set: setA, label: "Session A" },
          { value: b, set: setB, label: "Session B" },
        ].map(({ value, set, label }) => (
          <select
            key={label}
            value={value}
            onChange={(e) => set(e.target.value)}
            className="rounded-md border border-edge bg-surface-1 px-2 py-2 text-sm"
            aria-label={label}
          >
            <option value="">{label}…</option>
            {list.map((s) => (
              <option key={`${s.agent}|${s.id}`} value={`${s.agent}|${s.id}`}>
                [{s.agent}] {s.title || s.id} ({fmtCost(s.costUSD)})
              </option>
            ))}
          </select>
        ))}
      </div>

      {left && right && (
        <div className="overflow-hidden rounded-lg border border-edge">
          <table className="w-full text-sm">
            <thead className="bg-surface-2 text-left text-xs uppercase tracking-wide text-ink-dim">
              <tr>
                <th className="px-4 py-2">Metric</th>
                <th className="px-4 py-2 text-right">A</th>
                <th className="px-4 py-2 text-right">B</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge bg-surface-1">
              <Row label="Cost" a={fmtCost(left.costUSD, left.unpricedTokens)} b={fmtCost(right.costUSD, right.unpricedTokens)} />
              <Row label="Messages" a={String(left.messages)} b={String(right.messages)} />
              <Row label="Tool calls" a={String(left.toolCalls)} b={String(right.toolCalls)} />
              <Row label="Input tokens" a={fmtTokens(left.tokens.input)} b={fmtTokens(right.tokens.input)} />
              <Row label="Output tokens" a={fmtTokens(left.tokens.output)} b={fmtTokens(right.tokens.output)} />
              <Row label="Cache read" a={fmtTokens(left.tokens.cacheRead)} b={fmtTokens(right.tokens.cacheRead)} />
              <Row label="Cache write" a={fmtTokens(left.tokens.cacheWrite)} b={fmtTokens(right.tokens.cacheWrite)} />
              <Row label="Models" a={(left.models ?? []).join(", ")} b={(right.models ?? []).join(", ")} />
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function Row({ label, a, b }: { label: string; a: string; b: string }) {
  return (
    <tr>
      <td className="px-4 py-2 text-ink-dim">{label}</td>
      <td className="px-4 py-2 text-right tabular-nums">{a}</td>
      <td className="px-4 py-2 text-right tabular-nums">{b}</td>
    </tr>
  );
}

function useQueryDetail(agent?: string, id?: string): SessionDetail | null {
  const q = useQuery({
    queryKey: ["session", agent, id],
    queryFn: () => api.session(agent!, id!),
    enabled: Boolean(agent && id),
  });
  return q.data ?? null;
}
