import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, fmtCost, fmtTokens, type SessionDetail } from "../api";

const PICKER_PAGE = 100;

// Session compare: any two sessions, any agents — v1 could only compare
// within one Claude project. Each picker filters server-side by title,
// so ANY session is reachable — the old single 200-row fetch silently
// hid everything older.
export function ComparePage() {
  const [a, setA] = useState("");
  const [b, setB] = useState("");

  const [agentA, idA] = a.split("|");
  const [agentB, idB] = b.split("|");
  const left = useQueryDetail(agentA, idA);
  const right = useQueryDetail(agentB, idB);

  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Compare sessions</h1>
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <SessionPicker value={a} onChange={setA} label="Session A" />
        <SessionPicker value={b} onChange={setB} label="Session B" />
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
              <Row
                label="Cost"
                a={fmtCost(left.costUSD, left.unpricedTokens)}
                b={fmtCost(right.costUSD, right.unpricedTokens)}
              />
              <Row
                label="Messages"
                a={String(left.messages)}
                b={String(right.messages)}
              />
              <Row
                label="Tool calls"
                a={String(left.toolCalls)}
                b={String(right.toolCalls)}
              />
              <Row
                label="Input tokens"
                a={fmtTokens(left.tokens.input)}
                b={fmtTokens(right.tokens.input)}
              />
              <Row
                label="Output tokens"
                a={fmtTokens(left.tokens.output)}
                b={fmtTokens(right.tokens.output)}
              />
              <Row
                label="Cache read"
                a={fmtTokens(left.tokens.cacheRead)}
                b={fmtTokens(right.tokens.cacheRead)}
              />
              <Row
                label="Cache write"
                a={fmtTokens(left.tokens.cacheWrite)}
                b={fmtTokens(right.tokens.cacheWrite)}
              />
              <Row
                label="Models"
                a={(left.models ?? []).join(", ")}
                b={(right.models ?? []).join(", ")}
              />
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// SessionPicker is a title-filtered server-side select: the filter query
// narrows on the server, and a full page is flagged so users know to
// refine instead of assuming the list is complete.
function SessionPicker({
  value,
  onChange,
  label,
}: {
  value: string;
  onChange: (v: string) => void;
  label: string;
}) {
  const [q, setQ] = useState("");
  const sessions = useQuery({
    queryKey: ["compare-sessions", q],
    queryFn: () => api.sessions({ q, limit: String(PICKER_PAGE) }),
    placeholderData: (prev) => prev,
  });
  const list = sessions.data ?? [];
  const truncated = list.length === PICKER_PAGE;
  return (
    <div className="flex flex-col gap-1.5">
      <input
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder={`Filter ${label} by title…`}
        aria-label={`Filter ${label} by title`}
        className="rounded-md border border-edge bg-surface-1 px-3 py-1.5 text-sm placeholder:text-ink-faint"
      />
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="rounded-md border border-edge bg-surface-1 px-2 py-2 text-sm"
        aria-label={label}
      >
        <option value="">{label}…</option>
        {list.map((s) => (
          <option key={`${s.agent}|${s.id}`} value={`${s.agent}|${s.id}`}>
            [{s.agent}] {s.title || s.id} ({fmtCost(s.costUSD)})
          </option>
        ))}
        {truncated && (
          <option disabled value="__truncated">
            …showing newest {PICKER_PAGE} — refine the filter for older sessions
          </option>
        )}
      </select>
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
