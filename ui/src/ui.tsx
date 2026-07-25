import { useCallback, useEffect, useState, type ReactNode } from "react";
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
  children,
  className = "",
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section
      className={`rounded-md border border-edge bg-surface-1 ${className}`}
    >
      <header className="flex items-center border-b border-edge px-3 py-2">
        <h2 className="microlabel">{label}</h2>
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
          className={`mt-1 shrink-0 font-mono text-xl leading-none font-medium tabular-nums ${
            tone === "ok"
              ? "text-ok"
              : tone === "warn"
                ? "text-warn"
                : "text-ink"
          }`}
        >
          {value}
        </div>
        {spark && <div className="ml-auto w-14 min-w-0 sm:w-20">{spark}</div>}
      </div>
      {detail && (
        <div className="mt-1 font-mono text-[11px] text-ink-faint">
          {detail}
        </div>
      )}
    </>
  );
  const cls =
    "block overflow-hidden rounded-md border border-edge bg-surface-1 px-3 py-2.5 transition-colors";
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
// identity — accent only). Scales to its container so long values never
// push it out of the tile.
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
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      aria-hidden
      className="h-6 w-full opacity-80"
    >
      <polyline
        points={pts}
        fill="none"
        stroke="var(--color-accent)"
        strokeWidth="1.5"
        strokeLinejoin="round"
        vectorEffect="non-scaling-stroke"
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
    color:
      "color-mix(in oklab, var(--color-accent) 65%, var(--color-surface-2))",
  },
  {
    key: "cacheRead",
    label: "cache read",
    color:
      "color-mix(in oklab, var(--color-accent) 35%, var(--color-surface-2))",
  },
  {
    key: "cacheWrite",
    label: "cache write",
    color:
      "color-mix(in oklab, var(--color-accent) 18%, var(--color-surface-2))",
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

// TOOL_COLOR maps tool-call kinds to their fixed hues (CSS vars from
// styles.css). Chips and badges always pair the color with the kind or
// tool name as text.
const TOOL_COLOR: Record<string, string> = {
  shell: "var(--color-tool-shell)",
  file_edit: "var(--color-tool-edit)",
  file_write: "var(--color-tool-write)",
  file_read: "var(--color-tool-read)",
  search: "var(--color-tool-search)",
  discovery: "var(--color-tool-discovery)",
  web: "var(--color-tool-web)",
  subagent: "var(--color-tool-subagent)",
};

export function toolColor(kind: string): string {
  return TOOL_COLOR[kind] ?? "var(--color-ink-faint)";
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

// agentLabel marks experimental adapters wherever agents are offered as
// options: Cursor's schema is fixture-derived and not yet validated
// against a real store.db, and users must be able to tell complete
// support from experimental ingestion.
export function agentLabel(slug: string): string {
  return slug === "cursor" ? "cursor (experimental)" : slug;
}

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
  // Date range renders only when onRange is provided — views whose data
  // has no date dimension (e.g. rolling blocks) hide it rather than
  // showing controls that silently do nothing.
  since?: string;
  until?: string;
  onRange?: (since: string, until: string) => void;
  agent?: string;
  onAgent?: (agent: string) => void;
  model?: string;
  models?: string[];
  onModel?: (model: string) => void;
  children?: ReactNode;
}) {
  const dateCls =
    "rounded-md border border-edge bg-surface-1 px-2 py-1 font-mono text-xs text-ink-dim focus:text-ink";
  return (
    <div className="ml-auto flex flex-wrap items-center gap-2">
      {onRange && (
        <div className="flex items-center gap-1.5">
          <input
            type="date"
            value={since ?? ""}
            max={until || undefined}
            onChange={(e) => onRange(e.target.value, until ?? "")}
            className={dateCls}
            aria-label="From date"
          />
          <span className="font-mono text-xs text-ink-faint">→</span>
          <input
            type="date"
            value={until ?? ""}
            min={since || undefined}
            onChange={(e) => onRange(since ?? "", e.target.value)}
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
      )}
      {onAgent && (
        <select
          value={agent ?? ""}
          onChange={(e) => onAgent(e.target.value)}
          className="rounded-md border border-edge bg-surface-1 px-2 py-1.5 font-mono text-xs"
          aria-label="Filter by agent"
        >
          {FILTER_AGENTS.map((a) => (
            <option key={a} value={a}>
              {a === "" ? "all agents" : agentLabel(a)}
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

// useToggleSet tracks which of a list's items are expanded — the shape
// every "click a row to reveal more" surface needs. Returns the set and a
// toggle; membership is read with .has().
export function useToggleSet<T>(): [ReadonlySet<T>, (item: T) => void] {
  const [open, setOpen] = useState<ReadonlySet<T>>(new Set<T>());
  const toggle = useCallback((item: T) => {
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(item)) next.delete(item);
      else next.add(item);
      return next;
    });
  }, []);
  return [open, toggle];
}

// useDebounced trails a fast-changing value (a filter box) so the query
// keyed on it fires once the typing settles instead of once per
// keystroke. The input stays fully controlled and responsive; only what
// hits the server waits.
export function useDebounced<T>(value: T, ms = 250): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = window.setTimeout(() => setSettled(value), ms);
    return () => window.clearTimeout(t);
  }, [value, ms]);
  return settled;
}

// LoadError is the query-failure line. It carries role="alert" so a failed
// load is announced — eight pages hand-rolled this string and only three
// of them did, which is exactly the kind of split no single file owned.
// `compact` is the inline variant used inside an expanded row.
export function LoadError({
  error,
  compact = false,
}: {
  error: unknown;
  compact?: boolean;
}) {
  return (
    <p
      role="alert"
      className={compact ? "font-mono text-[11px] text-warn" : "text-warn"}
    >
      Failed to load: {String(error)}
    </p>
  );
}
