import { useEffect, useMemo, useRef, useState, type PointerEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  api,
  fmtTokens,
  type ContextPoint,
  type SessionContext,
} from "../../api";
import { fmtWhen } from "../../time";
import { EmptyNote, LoadError, Panel, SkeletonRows } from "../../ui";
import { EventList } from "../insights/events";

const HEIGHT = 220;
const PAD = { top: 16, right: 12, bottom: 22, left: 56 };

// ContextTab charts how full the context was at each model request — the
// prompt each one sent (input plus cache reads and writes) — with the
// compactions that emptied it marked, and both as tables beneath.
export function ContextTab({
  agent,
  sessionId,
  onJump,
}: {
  agent: string;
  sessionId: string;
  onJump: (seq: number) => void;
}) {
  const q = useQuery({
    queryKey: ["sessionContext", agent, sessionId],
    queryFn: () => api.sessionContext(agent, sessionId),
  });
  if (q.isLoading) return <SkeletonRows rows={6} />;
  if (q.error) return <LoadError error={q.error} />;
  const { points, compactions } = q.data!;
  if (points.length === 0)
    return <EmptyNote>No model requests with recorded usage.</EmptyNote>;
  return (
    <div className="space-y-4">
      <Panel label="Prompt tokens per request">
        <Chart points={points} compactions={compactions} onJump={onJump} />
      </Panel>
      {compactions.length > 0 && (
        <Panel label="Compactions">
          <EventList events={compactions} showSession={false} onJump={onJump} />
        </Panel>
      )}
      <details className="rounded-md border border-edge bg-surface-1">
        <summary className="cursor-pointer px-3 py-2 font-mono text-meta text-ink-faint">
          table view · {points.length} requests
        </summary>
        <div className="max-h-96 overflow-auto">
          <table className="w-full font-mono text-xs tabular-nums">
            <thead className="text-left text-micro text-ink-faint">
              <tr>
                <th className="px-3 py-1 font-normal">seq</th>
                <th className="px-3 py-1 font-normal">time</th>
                <th className="px-3 py-1 text-right font-normal">prompt</th>
                <th className="px-3 py-1 text-right font-normal">output</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {points.map((p) => (
                <tr key={p.seq}>
                  <td className="px-3 py-1">
                    <button
                      type="button"
                      onClick={() => onJump(p.seq)}
                      className="hover:text-accent"
                    >
                      #{p.seq}
                    </button>
                  </td>
                  <td className="px-3 py-1 text-ink-faint">
                    {fmtWhen(p.at ?? "")}
                  </td>
                  <td className="px-3 py-1 text-right">
                    {fmtTokens(p.promptTokens)}
                  </td>
                  <td className="px-3 py-1 text-right text-ink-faint">
                    {fmtTokens(p.outputTokens)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}

// niceCeil rounds a maximum up to 1, 2, or 5 times a power of ten, so the
// three gridlines land on readable values.
function niceCeil(n: number): number {
  if (n <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(n));
  for (const m of [1, 2, 5, 10]) if (m * p >= n) return m * p;
  return 10 * p;
}

// geometry lays the chart out: scales, the line, compaction markers, and
// the peak request.
function geometry(
  points: ContextPoint[],
  compactions: SessionContext["compactions"],
  width: number,
) {
  const max = niceCeil(Math.max(...points.map((p) => p.promptTokens)));
  const innerW = Math.max(width - PAD.left - PAD.right, 1);
  const innerH = HEIGHT - PAD.top - PAD.bottom;
  const x = (i: number) =>
    PAD.left +
    (points.length === 1 ? innerW / 2 : (i / (points.length - 1)) * innerW);
  const y = (v: number) => PAD.top + innerH - (v / max) * innerH;
  const path = points
    .map(
      (p, i) =>
        `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(p.promptTokens).toFixed(1)}`,
    )
    .join("");
  // A compaction sits between the last request before it and the first
  // after: marked at the first request whose seq follows it.
  const markers = compactions
    .map((c) => points.findIndex((p) => p.seq > c.messageSeq))
    .filter((i) => i >= 0);
  const peak = points.reduce((a, p) =>
    p.promptTokens > a.promptTokens ? p : a,
  );
  return { max, innerW, innerH, x, y, path, markers, peak };
}

// Chart is a single-series line: requests evenly spaced in order (time
// gaps between them are not the story), prompt size up. A crosshair snaps
// to the nearest request; clicking it opens the transcript there.
function Chart({
  points,
  compactions,
  onJump,
}: {
  points: ContextPoint[];
  compactions: SessionContext["compactions"];
  onJump: (seq: number) => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);
  const [hover, setHover] = useState<number | null>(null);
  useEffect(() => {
    const el = box.current;
    const ro = new ResizeObserver(() => {
      if (el) setWidth(el.clientWidth);
    });
    if (el) {
      ro.observe(el);
      setWidth(el.clientWidth);
    }
    return () => ro.disconnect();
  }, []);

  // Hovering re-renders on every pointer move; the geometry only changes
  // with the data or the width.
  const { max, innerW, innerH, x, y, path, markers, peak } = useMemo(
    () => geometry(points, compactions, width),
    [points, compactions, width],
  );

  const onMove = (e: PointerEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const rel = (e.clientX - rect.left - PAD.left) / innerW;
    setHover(
      Math.min(
        points.length - 1,
        Math.max(0, Math.round(rel * (points.length - 1))),
      ),
    );
  };
  const h = hover === null ? null : points[hover];

  return (
    <div ref={box} className="relative px-1 py-2">
      <svg
        width={width}
        height={HEIGHT}
        role="img"
        aria-label={`Prompt tokens across ${points.length} requests, peaking at ${fmtTokens(peak.promptTokens)}${markers.length ? `, with ${markers.length} compactions` : ""}. The table view below lists every request.`}
        onPointerMove={onMove}
        onPointerLeave={() => setHover(null)}
        onClick={() => h && onJump(h.seq)}
        className="block cursor-crosshair"
      >
        {[0, 0.5, 1].map((f) => (
          <g key={f}>
            <line
              x1={PAD.left}
              x2={PAD.left + innerW}
              y1={y(max * f)}
              y2={y(max * f)}
              stroke="var(--color-edge)"
              strokeWidth={1}
            />
            <text
              x={PAD.left - 8}
              y={y(max * f)}
              textAnchor="end"
              dominantBaseline="middle"
              className="fill-ink-faint font-mono text-[10px]"
            >
              {fmtTokens(max * f)}
            </text>
          </g>
        ))}
        {markers.map((i) => (
          <g key={`c${i}`}>
            <line
              x1={x(i)}
              x2={x(i)}
              y1={PAD.top}
              y2={PAD.top + innerH}
              stroke="var(--color-ink-faint)"
              strokeWidth={1}
            />
            <text
              x={x(i) + 4}
              y={PAD.top + 8}
              className="fill-ink-faint font-mono text-[10px]"
            >
              compacted
            </text>
          </g>
        ))}
        <path
          d={path}
          fill="none"
          stroke="var(--color-accent)"
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
        {h && hover !== null && (
          <g>
            <line
              x1={x(hover)}
              x2={x(hover)}
              y1={PAD.top}
              y2={PAD.top + innerH}
              stroke="var(--color-edge-strong)"
              strokeWidth={1}
            />
            <circle
              cx={x(hover)}
              cy={y(h.promptTokens)}
              r={4}
              fill="var(--color-accent)"
              stroke="var(--color-surface-1)"
              strokeWidth={2}
            />
          </g>
        )}
        <text
          x={PAD.left}
          y={HEIGHT - 6}
          className="fill-ink-faint font-mono text-[10px]"
        >
          first request
        </text>
        <text
          x={PAD.left + innerW}
          y={HEIGHT - 6}
          textAnchor="end"
          className="fill-ink-faint font-mono text-[10px]"
        >
          request {points.length}
        </text>
      </svg>
      {h && hover !== null && (
        <div
          className="pointer-events-none absolute top-2 rounded-md border border-edge bg-surface-1 px-2.5 py-1.5 shadow-sm"
          style={
            x(hover) > width / 2
              ? { right: width - x(hover) + 12 }
              : { left: x(hover) + 12 }
          }
        >
          <div className="font-mono text-sm font-medium text-ink tabular-nums">
            {fmtTokens(h.promptTokens)} prompt
          </div>
          <div className="font-mono text-micro text-ink-faint tabular-nums">
            request {hover + 1} · #{h.seq} · {fmtWhen(h.at ?? "")}
          </div>
        </div>
      )}
    </div>
  );
}
