import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type ReactNode,
  type FocusEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent,
  type RefObject,
} from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { AGENT_COLOR, fmtCost } from "./api";
import { todayUTC, utcDay } from "./time";

// The palette's shortcut is Cmd on Apple platforms and Ctrl everywhere
// else. The handler always accepted both; the hint claimed ⌘ on every
// platform, which is simply wrong on the Linux and Windows machines this
// runs on just as happily.
//
// It lives here, not in the router, because pages reference it — and a
// page importing the router that imports the page is a cycle waiting to
// evaluate in the wrong order.
export const isApple =
  typeof navigator !== "undefined" &&
  /Mac|iPhone|iPad/.test(navigator.platform);
export const PALETTE_KEY = isApple ? "⌘K" : "Ctrl K";

declare global {
  interface WindowEventMap {
    "ccpeek-palette": CustomEvent<{ q?: string }>;
  }
}

/** openPalette raises the ⌘K palette from anywhere, including the places
 *  that used to link to the /search page. An initial query prefills it, so
 *  a v1 `/search?q=…` bookmark still lands on its results.
 *
 *  The event is declared on WindowEventMap above so the listener side gets
 *  the detail type for free, instead of casting an Event back to what this
 *  function already knows it dispatched. */
export function openPalette(q?: string) {
  window.dispatchEvent(new CustomEvent("ccpeek-palette", { detail: { q } }));
}

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
    <span className="inline-flex shrink-0 items-center gap-1.5 rounded border border-edge bg-surface-2/60 px-1.5 py-0.5 font-mono text-meta text-ink-dim">
      <AgentDot agent={agent} />
      {agent}
    </span>
  );
}

// Panel is THE instrument card — hairline border, microlabel header,
// divider. It is the only card idiom in the app: charts, tables, lists
// and banners previously each hand-rolled their own heading and radius,
// which is what made one page read as three.
export function Panel({
  label,
  action,
  children,
  className = "",
}: {
  label?: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section
      className={`overflow-hidden rounded-md border border-edge bg-surface-1 ${className}`}
    >
      {(label || action) && (
        <header className="flex items-center gap-3 border-b border-edge px-3 py-2">
          {label && <h2 className="microlabel">{label}</h2>}
          {action && <div className="ml-auto">{action}</div>}
        </header>
      )}
      {children}
    </section>
  );
}

// PageHeader is the shared title row: heading, optional lede figure, and
// the controls that scope the page. Every page carries one, so Overview
// no longer starts abruptly at a row of tiles while its siblings have
// headings.
export function PageHeader({
  title,
  lede,
  children,
}: {
  title: string;
  lede?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2">
      <h1 className="text-xl font-semibold">{title}</h1>
      {lede}
      {children}
    </div>
  );
}

// Money renders a cost figure. Cost is deliberately NOT green: green
// reads as "good", and it collided with the ok/warn semantics the scan
// findings depend on. The number carries itself in tabular mono; warn is
// reserved for the cases that genuinely warrant attention — incomplete
// cost coverage.
export function Money({
  usd,
  incomplete = false,
  className = "",
  title,
}: {
  usd: number;
  incomplete?: boolean;
  className?: string;
  title?: string;
}) {
  const zero = !(usd > 0);
  return (
    <span
      title={
        title ??
        (incomplete
          ? "API-equivalent value; some usage has no resolvable price — a lower bound"
          : "API-equivalent value; reported by the agent where available, otherwise estimated from list rates")
      }
      className={`font-mono tabular-nums ${
        incomplete ? "text-warn" : zero ? "text-ink-faint" : "text-ink"
      } ${className}`}
    >
      {fmtCost(usd, incomplete)}
    </span>
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
  value: ReactNode;
  detail?: string;
  to?: string;
  /** Amber value, for a number that is itself the bad news. */
  tone?: "warn";
  spark?: ReactNode;
}) {
  const body = (
    <>
      <div className="microlabel">{label}</div>
      <div className="flex items-end gap-2">
        <div
          className={`mt-1 shrink-0 font-mono text-xl leading-none font-medium tabular-nums ${
            tone === "warn" ? "text-warn" : "text-ink"
          }`}
        >
          {value}
        </div>
        {spark && <div className="ml-auto w-14 min-w-0 sm:w-20">{spark}</div>}
      </div>
      {detail && (
        <div className="mt-1 font-mono text-meta text-ink-faint">{detail}</div>
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

// GhostButton is the quiet inline action — an outlined chip that only
// gains contrast on hover, for the choices that sit beside content
// rather than in front of it (ignore/restore, and the like).
export function GhostButton({
  children,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      {...props}
      className="shrink-0 rounded-md border border-edge px-2 py-0.5 font-mono text-meta text-ink-dim transition-colors hover:border-edge-strong hover:text-ink disabled:opacity-40"
    >
      {children}
    </button>
  );
}

export function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard.writeText(text);
        setDone(true);
        setTimeout(() => setDone(false), 1200);
      }}
      className="shrink-0 rounded border border-edge px-1.5 py-0.5 font-mono text-micro text-ink-faint transition-colors hover:border-edge-strong hover:text-ink"
      title="Copy to clipboard"
    >
      {done ? "copied" : "copy"}
    </button>
  );
}

// EmptyNote states what is absent and, where there is one, the action
// that would fill it. An empty panel with no words at all — which two
// Overview panels used to render — reads as a broken box.
//
// It also owns the OTHER reason a panel has nothing in it. "No workspaces
// recorded" is a claim about the archive, and it must not be made when the
// request simply failed — so an `error` replaces the note with the failure
// instead. Six panels wrote that ternary out by hand, each with its own
// wrapper, which is precisely how a panel ends up forgetting it.
type EmptyNoteProps =
  | { children: ReactNode; hint?: ReactNode; error?: undefined }
  | { children?: ReactNode; hint?: ReactNode; error: unknown };

export function EmptyNote({ children, hint, error }: EmptyNoteProps) {
  if (error != null) {
    return (
      <div className="px-3 py-3">
        <LoadError error={error} />
      </div>
    );
  }
  return (
    <div className="px-3 py-6 text-center">
      <p className="text-sm text-ink-dim">{children}</p>
      {hint && <p className="mt-1 text-meta text-ink-faint">{hint}</p>}
    </div>
  );
}

// onDisclosureKey opens a disclosure that is NOT a <button> with enter or
// space — the tool rows and the transcript's meta lines, which cannot BE
// buttons because each carries its own controls. The target check leaves
// those inner controls their own keys.
export function onDisclosureKey(
  e: ReactKeyboardEvent<HTMLElement>,
  toggle: () => void,
) {
  if (e.target !== e.currentTarget) return;
  if (e.key !== "Enter" && e.key !== " ") return;
  e.preventDefault();
  toggle();
}

// Skeleton rows: loading states shaped like the content they replace.
// groupRuns splits an already-ordered list into contiguous runs sharing a
// key — the browse pages' day and kind headings. It groups what the
// server ordered rather than re-sorting, so a page never quietly
// disagrees with the order it was given.
export function groupRuns<T>(
  items: readonly T[],
  keyOf: (item: T) => string,
): { key: string; items: T[] }[] {
  const out: { key: string; items: T[] }[] = [];
  for (const item of items) {
    const key = keyOf(item);
    const last = out[out.length - 1];
    if (last && last.key === key) last.items.push(item);
    else out.push({ key, items: [item] });
  }
  return out;
}

// SectionHeading is the labelled rule between those runs: the group's
// name, a hairline, and a count with a noun on it — a bare number
// floating at the right edge says nothing.
export function SectionHeading({
  children,
  count,
}: {
  children: ReactNode;
  count: ReactNode;
}) {
  return (
    <h2 className="microlabel mb-1 flex items-center gap-2">
      {children}
      <span className="h-px flex-1 bg-edge" />
      <span className="tabular-nums">{count}</span>
    </h2>
  );
}

// Loading wraps a skeleton in the announcement a screen reader needs:
// the placeholders themselves are aria-hidden, so without this a page in
// flight is silence. Four pages hand-rolled this region and the rest
// simply went without — it belongs with the skeletons, not copied beside
// each one.
export function Loading({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div role="status" className="space-y-4">
      <span className="sr-only">{label}</span>
      {children}
    </div>
  );
}

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

/** dropEmpty merges a filter patch into a route's search object and drops
 *  everything blank, so a cleared filter leaves no `?agent=` behind and
 *  every filtered view is the shortest URL that describes it. Sessions,
 *  Commands and Usage all patch their search the same way; the rule about
 *  what an empty value means lives here rather than in each of them. */
export function dropEmpty(
  prev: Record<string, string | undefined>,
  patch: Record<string, string>,
): Record<string, string | undefined> {
  const merged: Record<string, string | undefined> = { ...prev, ...patch };
  for (const k of Object.keys(merged)) {
    if (!merged[k]) delete merged[k];
  }
  return merged;
}

/** The routes whose search IS their filter state. */
type FilterRoute = "/sessions" | "/commands" | "/usage" | "/artifacts";

/** useSetFilter is the one way those routes change a filter: merge the
 *  patch into the URL's search through dropEmpty, and REPLACE rather than
 *  push, so narrowing a list is not a pile of history entries to walk back
 *  through one keystroke at a time.
 *
 *  Four pages carried this closure. Three were byte-identical; the fourth
 *  spread `{...prev, ...patch}` by hand and so kept the blanks — clearing
 *  its kind facet left a `?kind=` behind, which is the one thing dropEmpty
 *  exists to prevent. */
export function useSetFilter(from: FilterRoute) {
  const navigate = useNavigate({ from });
  return (patch: Record<string, string>) =>
    void navigate({
      search: (prev: Record<string, string | undefined>) =>
        dropEmpty(prev, patch),
      replace: true,
    });
}

/** useUrlText drives a text filter whose value lives in the URL.
 *
 *  Typing stays local and responsive, and only the settled value is pushed
 *  — but the URL is the authority in the other direction too: any external
 *  navigation (the sidebar's Sessions link, a heatmap day, the browser's
 *  back button) overwrites the box. Syncing on the DEBOUNCED value alone
 *  left the two out of step — the address bar said "no filter" while the
 *  list stayed narrowed by text the user could no longer see. */
export function useUrlText(
  urlValue: string,
  push: (value: string) => void,
  ms = 250,
): [string, (value: string) => void] {
  const [input, setInput] = useState(urlValue);
  const settled = useDebounced(input, ms);
  // The last value the two sides agreed on, so a change can be attributed
  // to the typist or to the URL.
  const agreed = useRef(urlValue);
  useEffect(() => {
    if (urlValue !== agreed.current) {
      agreed.current = urlValue;
      setInput(urlValue);
    }
  }, [urlValue]);
  useEffect(() => {
    if (settled === agreed.current) return;
    agreed.current = settled;
    push(settled);
    // `push` is rebuilt every render (it closes over navigate); the settled
    // value is the trigger, and re-running on push identity would fight the
    // user's typing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settled]);
  return [input, setInput];
}

// Single-key shortcuts stand down wherever a key is being typed — inputs,
// textareas, selects, contenteditable — so the key stays an ordinary
// character there. Nor do they act while the palette (or any dialog) is
// up: acting on the page behind a modal is a worse answer than doing
// nothing.
function inTypingContext(e: KeyboardEvent): boolean {
  const target = e.target instanceof HTMLElement ? e.target : null;
  if (
    target?.isContentEditable ||
    (target?.tagName &&
      ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName))
  ) {
    return true;
  }
  return document.querySelector('[role="dialog"]') !== null;
}

/** useSlashFocus points the `/` key at a page's primary filter box — the
 *  one gesture every list-shaped tool shares. It stands down inside any
 *  typing context (including the palette), so `/` remains an ordinary
 *  character wherever one is being typed. */
export function useSlashFocus(ref: RefObject<HTMLInputElement | null>) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      if (inTypingContext(e)) return;
      const el = ref.current;
      if (!el) return;
      e.preventDefault();
      el.focus();
      el.select();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ref]);
}

/** useKeyShortcut runs an action on a bare press of `key` — the `[`
 *  sidebar toggle — under the same rules as `/`: modified presses pass
 *  through, and typing contexts keep the key to themselves. */
export function useKeyShortcut(key: string, action: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== key || e.metaKey || e.ctrlKey || e.altKey) return;
      if (inTypingContext(e)) return;
      e.preventDefault();
      action();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [key, action]);
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
      className={compact ? "font-mono text-meta text-warn" : "text-warn"}
    >
      Failed to load: {String(error)}
    </p>
  );
}

// Hover tooltip: a cursor-anchored popup for data marks (richer than the
// native <title> delay).
//
// `bind` gives a mark every way in, not just the mouse: hover, keyboard
// focus, and touch all show it. Pointer-only tooltips made the figures
// behind them — a day's cost, a reported/estimated split — simply
// unreachable on a touch device or by keyboard.
export function useTooltip() {
  const [tip, setTip] = useState<{
    x: number;
    y: number;
    content: ReactNode;
  } | null>(null);
  const show = (e: { clientX: number; clientY: number }, content: ReactNode) =>
    setTip({ x: e.clientX, y: e.clientY, content });
  const hide = () => setTip(null);
  // Focus has no cursor position, so the mark's own box anchors the popup.
  const showAtElement = (el: Element, content: ReactNode) => {
    const r = el.getBoundingClientRect();
    setTip({ x: r.left + r.width / 2, y: r.bottom - 6, content });
  };
  const bind = (content: ReactNode) => ({
    onMouseEnter: (e: MouseEvent) => show(e, content),
    onMouseLeave: hide,
    onFocus: (e: FocusEvent) => showAtElement(e.currentTarget, content),
    onBlur: hide,
  });
  const node = tip ? (
    <div
      role="tooltip"
      className="pointer-events-none fixed z-50 max-w-xs rounded-md border border-edge-strong bg-surface-2 px-2.5 py-1.5 font-mono text-meta leading-relaxed shadow-xl"
      style={{
        left: Math.min(tip.x + 12, window.innerWidth - 260),
        top: tip.y + 14,
      }}
    >
      {tip.content}
    </div>
  ) : null;
  return { show, hide, bind, node };
}

// Sparkline: an inline single-hue trend for stat tiles (magnitude, not
// identity — accent only).
//
// A flat series renders nothing. With mostly-zero data the polyline drew
// a straight rule beside the value, which read as a stray underscore or a
// rendering artifact rather than a chart — worse than no chart at all.
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
  const max = Math.max(...values);
  const min = Math.min(...values);
  if (!(max > 0) || max === min) return null;
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
      <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 font-mono text-micro text-ink-faint">
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

/** kindLabel renders a tool kind for reading: the stored slug is
 *  snake_case, and half the UI un-slugged it while the other half showed
 *  it raw. */
// Code is the inline monospace chip for a path, a root, or any literal
// the prose refers to. Two pages defined it privately in the same commit.
export function Code({ children }: { children: ReactNode }) {
  return (
    <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-xs text-ink">
      {children}
    </code>
  );
}

export function kindLabel(kind: string): string {
  return kind.replaceAll("_", " ");
}

// KindBars: a horizontal magnitude chart for distributions (tool calls by
// kind) — accent bars with mono labels, no identity colors needed.
//
// The track is capped rather than stretched to the panel: a full-width
// 1400px bar for a count of 3 is a lot of ink claiming a lot of
// significance for a small number.
export function KindBars({
  items,
  fmt = String,
}: {
  items: { label: string; count: number }[];
  fmt?: (n: number) => string;
}) {
  if (items.length === 0) {
    return <EmptyNote>No tool calls recorded.</EmptyNote>;
  }
  const max = Math.max(...items.map((i) => i.count), 1);
  return (
    <div className="space-y-1.5 px-3 py-2.5">
      {items.map((i) => (
        <div key={i.label} className="flex items-center gap-2">
          <span className="w-24 shrink-0 truncate font-mono text-meta text-ink-dim">
            {kindLabel(i.label)}
          </span>
          <div className="h-2.5 max-w-96 flex-1 overflow-hidden rounded-sm bg-surface-2/50">
            <div
              className="h-full rounded-sm bg-accent/80"
              style={{ width: `${Math.max((i.count / max) * 100, 1)}%` }}
            />
          </div>
          <span className="w-12 shrink-0 text-right font-mono text-meta text-ink-faint tabular-nums">
            {fmt(i.count)}
          </span>
        </div>
      ))}
    </div>
  );
}

// Segmented is the shared one-of-N control — usage groupings, session
// facets, transcript lenses. A row of styled <button>s announced itself
// as a row of unrelated buttons; this carries the real semantics, so
// assistive tech reports "3 of 5, selected" and the group is one tab stop
// rather than five.
//
// `variant` picks which relationship the control expresses: "radio" for
// choosing a parameter (how to group a table), "tab" for choosing which
// panel of the same subject is on screen.
export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
  variant = "radio",
  className = "",
}: {
  value: T;
  options: { value: T; label: string; badge?: ReactNode }[];
  onChange: (v: T) => void;
  label: string;
  variant?: "radio" | "tab";
  className?: string;
}) {
  const isTab = variant === "tab";
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  // Roving tabindex: arrow keys move within the group, Tab leaves it.
  // Focus has to travel with the selection. Moving selection alone left
  // focus on a button that had just become tabIndex -1, so the next arrow
  // press was still handled by the ORIGINAL index and recomputed the same
  // neighbour — a keyboard user could never get more than one step from
  // where they started.
  const select = (i: number) => {
    const next = (i + options.length) % options.length;
    onChange(options[next].value);
    buttons.current[next]?.focus();
  };
  return (
    <div
      role={isTab ? "tablist" : "radiogroup"}
      aria-label={label}
      // Scrolls rather than overflowing the page: five facets with counts
      // are wider than a 390px viewport, and a control that cannot fit
      // should carry its own scrollbar, not give the whole document one.
      className={`inline-flex max-w-full overflow-x-auto rounded-md border border-edge font-mono text-xs ${className}`}
    >
      {options.map((o, i) => {
        const active = o.value === value;
        return (
          <button
            key={o.value}
            ref={(el) => {
              buttons.current[i] = el;
            }}
            type="button"
            role={isTab ? "tab" : "radio"}
            {...(isTab
              ? { "aria-selected": active }
              : { "aria-checked": active })}
            tabIndex={active ? 0 : -1}
            onKeyDown={(e) => {
              if (e.key === "ArrowRight" || e.key === "ArrowDown") {
                e.preventDefault();
                select(i + 1);
              } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
                e.preventDefault();
                select(i - 1);
              } else if (e.key === "Home") {
                e.preventDefault();
                select(0);
              } else if (e.key === "End") {
                e.preventDefault();
                select(options.length - 1);
              }
            }}
            onClick={() => onChange(o.value)}
            className={`px-2.5 py-1.5 whitespace-nowrap first:rounded-l-md last:rounded-r-md transition-colors ${
              active
                ? "bg-surface-2 text-ink"
                : "text-ink-dim hover:bg-surface-2/40 hover:text-ink"
            }`}
          >
            {o.label}
            {o.badge != null && (
              <span className="ml-1.5 text-ink-faint tabular-nums">
                {o.badge}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

// FilterBar: the shared date-range + agent (+ model) row every data view
// carries. Values are controlled by the page (URL or state).
// FILTER_AGENTS is the roster every agent picker offers — the leading ""
// is "all agents". Exported so the palette's picker cannot drift from the
// FilterBar's.
export const FILTER_AGENTS = [
  "",
  "claude-code",
  "pi",
  "codex",
  "opencode",
  "cursor",
];

// agentLabel marks experimental adapters wherever agents are offered as
// options: Cursor's schema is fixture-derived and not yet validated
// against a real store.db, and users must be able to tell complete
// support from experimental ingestion.
export function agentLabel(slug: string): string {
  return slug === "cursor" ? "cursor (experimental)" : slug;
}

export const selectCls =
  "rounded-md border border-edge bg-surface-1 px-2 py-1.5 font-mono text-xs text-ink";
export const inputCls =
  "rounded-md border border-edge bg-surface-1 px-3 py-1.5 text-sm text-ink placeholder:text-ink-faint";

// Date presets. Reaching for "the last 30 days" through two native date
// pickers — which render as mm/dd/yyyy in US locale and match nothing
// else in the UI — was the most common range request taking the most
// work. The custom inputs stay, one disclosure away, for the rest.
const PRESETS: { label: string; days: number | "month" | "all" }[] = [
  { label: "7d", days: 7 },
  { label: "30d", days: 30 },
  { label: "90d", days: 90 },
  { label: "month", days: "month" },
  { label: "all", days: "all" },
];

// Presets are UTC days, like every other date key the API takes — see the
// UTC calendar section of time.ts.
function presetRange(days: number | "month" | "all"): [string, string] {
  if (days === "all") return ["", ""];
  const today = new Date(todayUTC());
  if (days === "month") {
    const first = new Date(
      Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1),
    );
    return [utcDay(first), utcDay(today)];
  }
  const from = new Date(today.getTime() - (days - 1) * 86_400_000);
  return [utcDay(from), utcDay(today)];
}

function activePreset(since: string, until: string): string | null {
  for (const p of PRESETS) {
    const [s, u] = presetRange(p.days);
    if (s === since && u === until) return p.label;
  }
  return null;
}

export function DateRange({
  since,
  until,
  onRange,
}: {
  since: string;
  until: string;
  onRange: (since: string, until: string) => void;
}) {
  const [custom, setCustom] = useState(
    () => Boolean(since || until) && activePreset(since, until) === null,
  );
  const active = activePreset(since, until);
  const dateCls =
    "rounded-md border border-edge bg-surface-1 px-2 py-1.5 font-mono text-xs text-ink-dim focus:text-ink";
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <div
        role="radiogroup"
        aria-label="Date range preset"
        className="inline-flex rounded-md border border-edge font-mono text-xs"
      >
        {PRESETS.map((p) => {
          const on = !custom && active === p.label;
          return (
            <button
              key={p.label}
              type="button"
              role="radio"
              aria-checked={on}
              onClick={() => {
                setCustom(false);
                const [s, u] = presetRange(p.days);
                onRange(s, u);
              }}
              className={`px-2 py-1.5 first:rounded-l-md last:rounded-r-md transition-colors ${
                on
                  ? "bg-surface-2 text-ink"
                  : "text-ink-dim hover:bg-surface-2/40 hover:text-ink"
              }`}
            >
              {p.label}
            </button>
          );
        })}
        <button
          type="button"
          role="radio"
          aria-checked={custom}
          onClick={() => setCustom((v) => !v)}
          title="Custom date range"
          className={`rounded-r-md border-l border-edge px-2 py-1.5 transition-colors ${
            custom
              ? "bg-surface-2 text-ink"
              : "text-ink-dim hover:bg-surface-2/40 hover:text-ink"
          }`}
        >
          ⋯
        </button>
      </div>
      {custom && (
        <>
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
        </>
      )}
    </div>
  );
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
  return (
    <div className="ml-auto flex flex-wrap items-center gap-2">
      {onRange && (
        <DateRange since={since ?? ""} until={until ?? ""} onRange={onRange} />
      )}
      {onAgent && (
        <select
          value={agent ?? ""}
          onChange={(e) => onAgent(e.target.value)}
          className={selectCls}
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
          className={`max-w-44 ${selectCls}`}
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
