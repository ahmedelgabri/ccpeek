import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { AGENT_COLOR } from "./api";

// AgentDot is the identity mark: a small square in the agent's fixed
// color. Identity never rides on color alone — pair it with the slug.
export function AgentDot({ agent }: { agent: string }) {
  return (
    <span
      aria-hidden
      className="inline-block h-2 w-2 shrink-0 rounded-[2px]"
      style={{ background: AGENT_COLOR[agent] ?? "var(--color-ink-faint)" }}
    />
  );
}

export function AgentChip({ agent }: { agent: string }) {
  return (
    <span className="inline-flex shrink-0 items-center gap-1.5 rounded border border-edge bg-surface-2/60 px-1.5 py-0.5 font-mono text-[11px] text-ink-dim">
      <AgentDot agent={agent} />
      {agent}
    </span>
  );
}

// Panel is the instrument card: hairline border, micro-label header.
export function Panel({
  label,
  action,
  children,
  className = "",
}: {
  label: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section
      className={`rounded-md border border-edge bg-surface-1 ${className}`}
    >
      <header className="flex items-center border-b border-edge px-3 py-2">
        <h2 className="microlabel">{label}</h2>
        {action && <div className="ml-auto">{action}</div>}
      </header>
      {children}
    </section>
  );
}

export function StatTile({
  label,
  value,
  detail,
  to,
  tone,
  spark,
}: {
  label: string;
  value: string;
  detail?: string;
  to?: string;
  tone?: "ok" | "warn";
  spark?: ReactNode;
}) {
  const body = (
    <>
      <div className="microlabel">{label}</div>
      <div className="flex items-end gap-2">
        <div
          className={`mt-1 font-mono text-xl leading-none font-medium tabular-nums ${
            tone === "ok"
              ? "text-ok"
              : tone === "warn"
                ? "text-warn"
                : "text-ink"
          }`}
        >
          {value}
        </div>
        {spark && <div className="ml-auto">{spark}</div>}
      </div>
      {detail && (
        <div className="mt-1 font-mono text-[11px] text-ink-faint">
          {detail}
        </div>
      )}
    </>
  );
  const cls =
    "block rounded-md border border-edge bg-surface-1 px-3 py-2.5 transition-colors";
  return to ? (
    <Link to={to} className={`${cls} hover:border-edge-strong`}>
      {body}
    </Link>
  ) : (
    <div className={cls}>{body}</div>
  );
}

export function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      onClick={() => {
        void navigator.clipboard.writeText(text);
        setDone(true);
        setTimeout(() => setDone(false), 1200);
      }}
      className="shrink-0 rounded border border-edge px-1.5 py-0.5 font-mono text-[10px] text-ink-faint transition-colors hover:border-edge-strong hover:text-ink"
      title="Copy to clipboard"
    >
      {done ? "copied" : "copy"}
    </button>
  );
}

export function EmptyNote({ children }: { children: ReactNode }) {
  return (
    <p className="px-3 py-6 text-center text-sm text-ink-faint">{children}</p>
  );
}

// Skeleton rows: loading states shaped like the content they replace.
export function SkeletonRows({
  rows = 6,
  className = "",
}: {
  rows?: number;
  className?: string;
}) {
  return (
    <div
      className={`divide-y divide-edge overflow-hidden rounded-md border border-edge ${className}`}
      aria-hidden
    >
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="animate-pulse bg-surface-1 px-4 py-3">
          <div className="mb-2 h-3 w-2/3 rounded bg-surface-2" />
          <div className="h-2 w-1/3 rounded bg-surface-2/70" />
        </div>
      ))}
    </div>
  );
}

export function SkeletonTiles({ n = 6 }: { n?: number }) {
  return (
    <div
      className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6"
      aria-hidden
    >
      {Array.from({ length: n }, (_, i) => (
        <div
          key={i}
          className="animate-pulse rounded-md border border-edge bg-surface-1 px-3 py-2.5"
        >
          <div className="mb-2 h-2 w-1/2 rounded bg-surface-2" />
          <div className="h-5 w-2/3 rounded bg-surface-2/70" />
        </div>
      ))}
    </div>
  );
}

// Hover tooltip: a cursor-anchored popup for data marks (richer than the
// native <title> delay). Wrap the chart area and call show/hide.
export function useTooltip() {
  const [tip, setTip] = useState<{
    x: number;
    y: number;
    content: ReactNode;
  } | null>(null);
  const show = (e: { clientX: number; clientY: number }, content: ReactNode) =>
    setTip({ x: e.clientX, y: e.clientY, content });
  const hide = () => setTip(null);
  const node = tip ? (
    <div
      className="pointer-events-none fixed z-50 max-w-xs rounded-md border border-edge-strong bg-surface-2 px-2.5 py-1.5 font-mono text-[11px] leading-relaxed shadow-xl"
      style={{
        left: Math.min(tip.x + 12, window.innerWidth - 260),
        top: tip.y + 14,
      }}
    >
      {tip.content}
    </div>
  ) : null;
  return { show, hide, node };
}

// Sparkline: an inline single-hue trend for stat tiles (magnitude, not
// identity — accent only).
export function Sparkline({
  values,
  width = 96,
  height = 24,
}: {
  values: number[];
  width?: number;
  height?: number;
}) {
  if (values.length < 2) return null;
  const max = Math.max(...values, 1);
  const step = width / (values.length - 1);
  const pts = values
    .map(
      (v, i) =>
        `${(i * step).toFixed(1)},${(height - 2 - (v / max) * (height - 4)).toFixed(1)}`,
    )
    .join(" ");
  return (
    <svg width={width} height={height} aria-hidden className="opacity-80">
      <polyline
        points={pts}
        fill="none"
        stroke="var(--color-accent)"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
    </svg>
  );
}

// TokenMixBar: one stacked proportion bar for a session's token split —
// sequential steps of the accent hue (magnitude of one whole, not
// separate identities), labeled by the legend beneath it.
const MIX = [
  { key: "input", label: "input", color: "var(--color-accent)" },
  {
    key: "output",
    label: "output",
    color: "color-mix(in oklab, var(--color-accent) 65%, var(--color-surface-2))",
  },
  {
    key: "cacheRead",
    label: "cache read",
    color: "color-mix(in oklab, var(--color-accent) 35%, var(--color-surface-2))",
  },
  {
    key: "cacheWrite",
    label: "cache write",
    color: "color-mix(in oklab, var(--color-accent) 18%, var(--color-surface-2))",
  },
] as const;

export function TokenMixBar({
  tokens,
  fmt,
}: {
  tokens: Record<(typeof MIX)[number]["key"], number>;
  fmt: (n: number) => string;
}) {
  const total = MIX.reduce((acc, m) => acc + tokens[m.key], 0);
  if (total === 0) return null;
  return (
    <div>
      <div className="flex h-2 gap-[2px] overflow-hidden rounded">
        {MIX.filter((m) => tokens[m.key] > 0).map((m) => (
          <div
            key={m.key}
            style={{
              width: `${(tokens[m.key] / total) * 100}%`,
              background: m.color,
            }}
          />
        ))}
      </div>
      <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 font-mono text-[10px] text-ink-faint">
        {MIX.filter((m) => tokens[m.key] > 0).map((m) => (
          <span key={m.key} className="inline-flex items-center gap-1.5">
            <span
              className="inline-block h-2 w-2 rounded-[2px]"
              style={{ background: m.color }}
            />
            {m.label} {fmt(tokens[m.key])}
          </span>
        ))}
      </div>
    </div>
  );
}

// KindBars: a horizontal magnitude chart for distributions (tool calls by
// kind) — accent bars with mono labels, no identity colors needed.
export function KindBars({
  items,
  fmt = String,
}: {
  items: { label: string; count: number }[];
  fmt?: (n: number) => string;
}) {
  const max = Math.max(...items.map((i) => i.count), 1);
  return (
    <div className="space-y-1.5 px-3 py-2.5">
      {items.map((i) => (
        <div key={i.label} className="flex items-center gap-2">
          <span className="w-24 shrink-0 truncate font-mono text-[11px] text-ink-dim">
            {i.label.replaceAll("_", " ")}
          </span>
          <div className="h-2.5 flex-1 overflow-hidden rounded-sm bg-surface-2/50">
            <div
              className="h-full rounded-sm bg-accent/80"
              style={{ width: `${Math.max((i.count / max) * 100, 1)}%` }}
            />
          </div>
          <span className="w-12 shrink-0 text-right font-mono text-[11px] text-ink-faint tabular-nums">
            {fmt(i.count)}
          </span>
        </div>
      ))}
    </div>
  );
}

// FilterBar: the shared date-range + agent (+ model) row every data view
// carries. Values are controlled by the page (URL or state).
const FILTER_AGENTS = ["", "claude-code", "pi", "codex", "opencode", "cursor"];

export function FilterBar({
  since,
  until,
  onRange,
  agent,
  onAgent,
  model,
  models,
  onModel,
  children,
}: {
  since: string;
  until: string;
  onRange: (since: string, until: string) => void;
  agent?: string;
  onAgent?: (agent: string) => void;
  model?: string;
  models?: string[];
  onModel?: (model: string) => void;
  children?: ReactNode;
}) {
  const dateCls =
    "rounded-md border border-edge bg-surface-1 px-2 py-1 font-mono text-xs text-ink-dim [color-scheme:dark] focus:text-ink";
  return (
    <div className="ml-auto flex flex-wrap items-center gap-2">
      <div className="flex items-center gap-1.5">
        <input
          type="date"
          value={since}
          max={until || undefined}
          onChange={(e) => onRange(e.target.value, until)}
          className={dateCls}
          aria-label="From date"
        />
        <span className="font-mono text-xs text-ink-faint">→</span>
        <input
          type="date"
          value={until}
          min={since || undefined}
          onChange={(e) => onRange(since, e.target.value)}
          className={dateCls}
          aria-label="To date"
        />
        {(since || until) && (
          <button
            onClick={() => onRange("", "")}
            className="rounded border border-edge px-1.5 py-1 font-mono text-[10px] text-ink-faint hover:text-ink"
            title="Clear date range"
          >
            ✕
          </button>
        )}
      </div>
      {onAgent && (
        <select
          value={agent ?? ""}
          onChange={(e) => onAgent(e.target.value)}
          className="rounded-md border border-edge bg-surface-1 px-2 py-1.5 font-mono text-xs"
          aria-label="Filter by agent"
        >
          {FILTER_AGENTS.map((a) => (
            <option key={a} value={a}>
              {a === "" ? "all agents" : a}
            </option>
          ))}
        </select>
      )}
      {onModel && (
        <select
          value={model ?? ""}
          onChange={(e) => onModel(e.target.value)}
          className="max-w-44 rounded-md border border-edge bg-surface-1 px-2 py-1.5 font-mono text-xs"
          aria-label="Filter by model"
        >
          <option value="">all models</option>
          {(models ?? []).map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      )}
      {children}
    </div>
  );
}
