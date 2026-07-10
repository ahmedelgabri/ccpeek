import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { api, fmtCost, fmtTokens, type TranscriptMessage } from "../api";

// The gathering point of the session-centric model: one session with its
// transcript, usage, relations, and linked artifacts.
export function SessionDetailPage() {
  const { agent, sessionId } = useParams({ from: "/sessions/$agent/$sessionId" });

  const detail = useQuery({
    queryKey: ["session", agent, sessionId],
    queryFn: () => api.session(agent, sessionId),
  });
  const transcript = useQuery({
    queryKey: ["transcript", agent, sessionId],
    queryFn: () => api.transcript(agent, sessionId, { limit: "500" }),
  });
  const [treeView, setTreeView] = useState(false);
  const depths = useMemo(
    () => computeDepths(transcript.data ?? []),
    [transcript.data],
  );

  if (detail.isLoading) return <p className="text-ink-dim">Loading…</p>;
  if (detail.error) return <p className="text-warn">{String(detail.error)}</p>;
  const s = detail.data!;

  return (
    <div>
      <div className="mb-1 flex items-baseline gap-3">
        <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-accent">
          {s.agent}
        </span>
        <h1 className="truncate text-xl font-semibold">{s.title || "(untitled)"}</h1>
      </div>
      <p className="mb-4 text-xs text-ink-dim">
        {s.cwd} {s.gitBranch && <>· ⎇ {s.gitBranch}</>} · {s.id}
      </p>

      <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-6">
        <Stat label="Cost" value={fmtCost(s.costUSD, s.unpricedTokens)} accent />
        <Stat label="Input" value={fmtTokens(s.tokens.input)} />
        <Stat label="Output" value={fmtTokens(s.tokens.output)} />
        <Stat label="Cache read" value={fmtTokens(s.tokens.cacheRead)} />
        <Stat label="Cache write" value={fmtTokens(s.tokens.cacheWrite)} />
        <Stat label="Messages" value={String(s.messages)} />
      </div>

      {s.unpricedTokens ? (
        <p className="mb-4 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
          {fmtTokens(s.unpricedTokens)} tokens use a model the pricing table
          can't resolve — the cost shown is a lower bound.
        </p>
      ) : null}

      {(s.relations?.length || s.artifacts?.length || s.models?.length) && (
        <div className="mb-6 flex flex-wrap gap-2 text-xs">
          {s.models?.map((m) => (
            <span key={m} className="rounded-full border border-edge px-2 py-1 text-ink-dim">
              {m}
            </span>
          ))}
          {s.relations?.map((r) => (
            <Link
              key={`${r.kind}-${r.sessionId}`}
              to="/sessions/$agent/$sessionId"
              params={{ agent: s.agent, sessionId: r.sessionId }}
              className="rounded-full border border-accent/40 px-2 py-1 text-accent hover:bg-surface-2"
            >
              {r.direction === "out" ? r.kind : `${r.kind} (incoming)`} →{" "}
              {r.sessionId.slice(0, 8)}
            </Link>
          ))}
          {s.artifacts?.map((a) => (
            <span
              key={`${a.kind}-${a.name}`}
              title={`${a.relation} (${a.evidence})`}
              className="rounded-full border border-edge px-2 py-1 text-ink-dim"
            >
              {a.kind}: {a.name.length > 30 ? a.name.slice(0, 30) + "…" : a.name}
            </span>
          ))}
        </div>
      )}

      <div className="mb-2 flex items-center gap-3">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-ink-dim">
          Transcript
        </h2>
        <button
          onClick={() => setTreeView((v) => !v)}
          className={`ml-auto rounded-md border border-edge px-2 py-1 text-xs ${
            treeView ? "bg-surface-2 text-ink" : "text-ink-dim hover:text-ink"
          }`}
        >
          tree view
        </button>
      </div>
      <ol className="space-y-2">
        {(transcript.data ?? []).map((m) => (
          <li
            key={m.seq}
            style={
              treeView
                ? { marginLeft: `${Math.min(depths.get(m.seq) ?? 0, 12) * 16}px` }
                : undefined
            }
            className={`rounded-lg border border-edge p-3 ${
              m.role === "user" ? "bg-surface-2" : "bg-surface-1"
            } ${m.isSidechain && !treeView ? "ml-8 border-dashed" : ""} ${
              m.isSidechain && treeView ? "border-dashed" : ""
            }`}
          >
            <div className="mb-1 flex gap-2 text-xs text-ink-dim">
              <span className={m.role === "assistant" ? "text-accent" : ""}>
                {m.isSidechain ? "↳ " : ""}
                {m.role}
                {m.kind !== "message" ? ` · ${m.kind}` : ""}
              </span>
              {m.model && <span>{m.model}</span>}
              <span className="ml-auto">{m.createdAt.slice(11, 19)}</span>
            </div>
            <div className="whitespace-pre-wrap text-sm leading-relaxed">
              {m.text || <span className="text-ink-dim">(no text content)</span>}
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}

// computeDepths walks the agent-native entry tree (Claude parentUuid, Pi
// id/parentId): an entry's depth is parent depth + 1 when the parent is
// NOT the immediately preceding entry (a real branch), else parent depth —
// keeping the main thread flat while branches indent.
function computeDepths(msgs: TranscriptMessage[]): Map<number, number> {
  const bySeq = new Map<number, number>();
  const byExternal = new Map<string, TranscriptMessage>();
  for (const m of msgs) {
    if (m.externalId) byExternal.set(m.externalId, m);
  }
  let prev: TranscriptMessage | undefined;
  for (const m of msgs) {
    let depth = 0;
    const parent = m.parentId ? byExternal.get(m.parentId) : undefined;
    if (parent) {
      const parentDepth = bySeq.get(parent.seq) ?? 0;
      depth =
        prev && parent.seq !== prev.seq ? parentDepth + 1 : parentDepth;
    } else if (m.isSidechain) {
      depth = 1;
    }
    bySeq.set(m.seq, depth);
    prev = m;
  }
  return bySeq;
}

function Stat({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <div className="rounded-lg border border-edge bg-surface-1 px-3 py-2">
      <div className="text-xs text-ink-dim">{label}</div>
      <div className={`text-lg font-semibold tabular-nums ${accent ? "text-ok" : ""}`}>
        {value}
      </div>
    </div>
  );
}
