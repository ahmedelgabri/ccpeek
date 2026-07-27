import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import {
  api,
  fmtCost,
  fmtCount,
  fmtTokens,
  shortPath,
  type SessionDetail,
} from "../api";
import { fmtWhen } from "../time";
import {
  AgentChip,
  AgentDot,
  EmptyNote,
  inputCls,
  LoadError,
  Loading,
  Money,
  PageHeader,
  SkeletonRows,
  useDebounced,
} from "../ui";

const PICKER_PAGE = 100;

// Session compare: any two sessions, any agents — v1 could only compare
// within one Claude project. Each picker filters server-side by title,
// so ANY session is reachable — the old single 200-row fetch silently
// hid everything older.
export function ComparePage() {
  const search = useSearch({ from: "/compare" });
  const navigate = useNavigate({ from: "/compare" });
  const a = search.a ?? "";
  const b = search.b ?? "";

  const [agentA, idA] = a.split("|");
  const [agentB, idB] = b.split("|");
  const left = useQueryDetail(agentA, idA);
  const right = useQueryDetail(agentB, idB);

  return (
    <div>
      <PageHeader title="Compare sessions">
        <button
          type="button"
          onClick={() =>
            void navigate({
              search: { a: b || undefined, b: a || undefined },
              replace: true,
            })
          }
          disabled={!a && !b}
          className="ml-auto rounded-md border border-edge px-2 py-1.5 font-mono text-xs text-ink-dim transition-colors hover:border-edge-strong hover:text-ink disabled:opacity-40"
          aria-label="Swap compared sessions"
        >
          ⇄ swap
        </button>
      </PageHeader>
      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        <SessionPicker
          value={a}
          onChange={(value) =>
            void navigate({
              search: (prev: { a?: string; b?: string }) => ({
                ...prev,
                a: value || undefined,
              }),
              replace: true,
            })
          }
          label="Session A"
        />
        <SessionPicker
          value={b}
          onChange={(value) =>
            void navigate({
              search: (prev: { a?: string; b?: string }) => ({
                ...prev,
                b: value || undefined,
              }),
              replace: true,
            })
          }
          label="Session B"
        />
      </div>

      {(!a || !b) && (
        <div role="status">
          <EmptyNote hint="Pick one on each side — any two sessions, from any two agents.">
            {a || b
              ? "Pick one more session to compare."
              : "Nothing selected yet."}
          </EmptyNote>
        </div>
      )}
      {a && b && (left.error || right.error) && (
        <LoadError error={left.error ?? right.error} />
      )}
      {a &&
        b &&
        !left.error &&
        !right.error &&
        (left.isLoading || right.isLoading) && (
          <Loading label="Loading session comparison…">
            <SkeletonRows rows={8} />
          </Loading>
        )}
      {!left.error && !right.error && left.data && right.data && (
        <ComparisonTable left={left.data} right={right.data} />
      )}
    </div>
  );
}

function ComparisonTable({
  left,
  right,
}: {
  left: SessionDetail;
  right: SessionDetail;
}) {
  const leftDuration = durationMs(left);
  const rightDuration = durationMs(right);
  return (
    <div className="overflow-x-auto rounded-md border border-edge">
      <table className="w-full min-w-[720px] text-sm">
        <thead className="bg-surface-2 text-left text-xs text-ink-dim">
          <tr>
            <th className="px-4 py-2 uppercase tracking-wide">Metric</th>
            <SessionHeading session={left} />
            <SessionHeading session={right} />
            <th className="px-4 py-2 text-right font-mono text-meta uppercase tracking-wide">
              Δ B−A
            </th>
          </tr>
        </thead>
        <tbody className="divide-y divide-edge bg-surface-1">
          <TextRow
            label="Started"
            a={fmtWhen(left.createdAt)}
            b={fmtWhen(right.createdAt)}
          />
          <NumericRow
            label="Duration"
            a={leftDuration}
            b={rightDuration}
            format={fmtDuration}
            deltaFormat={fmtDuration}
          />
          <TextRow
            label="Workspace"
            a={shortPath(left.cwd)}
            b={shortPath(right.cwd)}
            mono
          />
          <NumericRow
            label="Cost"
            a={left.costUSD}
            b={right.costUSD}
            formatA={(n) => fmtCost(n, left.unpricedTokens)}
            formatB={(n) => fmtCost(n, right.unpricedTokens)}
            deltaFormat={(n) => fmtCost(n)}
            direction="lower"
            cost
          />
          <NumericRow label="Messages" a={left.messages} b={right.messages} />
          <NumericRow
            label="Tool calls"
            a={left.toolCalls}
            b={right.toolCalls}
          />
          <NumericRow
            label="Input tokens"
            a={left.tokens.input}
            b={right.tokens.input}
            format={fmtTokens}
            deltaFormat={fmtTokens}
          />
          <NumericRow
            label="Output tokens"
            a={left.tokens.output}
            b={right.tokens.output}
            format={fmtTokens}
            deltaFormat={fmtTokens}
          />
          <NumericRow
            label="Cache read"
            a={left.tokens.cacheRead}
            b={right.tokens.cacheRead}
            format={fmtTokens}
            deltaFormat={fmtTokens}
          />
          <NumericRow
            label="Cache write"
            a={left.tokens.cacheWrite}
            b={right.tokens.cacheWrite}
            format={fmtTokens}
            deltaFormat={fmtTokens}
          />
          <ModelsRow a={left.models ?? []} b={right.models ?? []} />
        </tbody>
      </table>
    </div>
  );
}

function SessionHeading({ session }: { session: SessionDetail }) {
  return (
    <th className="max-w-72 px-4 py-2">
      <Link
        to="/sessions/$agent/$sessionId"
        params={{ agent: session.agent, sessionId: session.id }}
        className="flex items-center justify-end gap-2 hover:text-accent"
      >
        <AgentChip agent={session.agent} />
        <span className="truncate font-medium text-ink">
          {session.title || session.id}
        </span>
      </Link>
    </th>
  );
}

// SessionPicker is a title-filtered list, not a native <select>. A
// session is identified by four things at once — when, which agent, what
// it was called, what it cost — and an <option> can only be one line of
// unstyled text, so the four were crammed into one string that told the
// reader almost nothing at a glance. The filter still narrows on the
// server, and a full page is flagged so nobody assumes the list is
// complete.
function SessionPicker({
  value,
  onChange,
  label,
}: {
  value: string;
  onChange: (v: string) => void;
  label: string;
}) {
  const [qInput, setQInput] = useState("");
  const q = useDebounced(qInput, 250);
  const sessions = useQuery({
    queryKey: ["compare-sessions", q],
    queryFn: () => api.sessions({ q, limit: String(PICKER_PAGE) }),
    placeholderData: (prev) => prev,
  });
  const list = sessions.data ?? [];
  const truncated = list.length === PICKER_PAGE;
  const selected = list.find((s) => `${s.agent}|${s.id}` === value);

  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="microlabel">{label}</div>
      <input
        value={qInput}
        onChange={(e) => setQInput(e.target.value)}
        placeholder="Filter by title…"
        aria-label={`Filter ${label} by title`}
        className={inputCls}
      />
      {value && !selected && (
        <div className="rounded-md border border-accent/40 px-2 py-1 font-mono text-meta text-ink-dim">
          {value.replace("|", " · ")} (selected, outside the current filter)
        </div>
      )}
      <ul
        role="listbox"
        aria-label={label}
        className="h-56 divide-y divide-edge overflow-y-auto rounded-md border border-edge bg-surface-1"
      >
        {list.length === 0 && (
          <li className="px-3 py-4 text-center text-sm text-ink-dim">
            {sessions.isLoading ? "Loading…" : "No sessions match."}
          </li>
        )}
        {list.map((s) => {
          const id = `${s.agent}|${s.id}`;
          const on = id === value;
          return (
            <li key={id} role="option" aria-selected={on}>
              <button
                type="button"
                onClick={() => onChange(on ? "" : id)}
                className={`flex w-full min-w-0 flex-col gap-0.5 px-3 py-1.5 text-left transition-colors ${
                  on ? "bg-surface-2" : "hover:bg-surface-2/40"
                }`}
              >
                <span className="flex min-w-0 items-baseline gap-2">
                  <AgentDot agent={s.agent} />
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {s.title || s.id}
                  </span>
                  <Money usd={s.costUSD} className="shrink-0 text-meta" />
                </span>
                <span className="font-mono text-meta text-ink-faint">
                  {fmtWhen(s.modifiedAt)} · {shortPath(s.cwd)}
                </span>
              </button>
            </li>
          );
        })}
        {truncated && (
          <li className="px-3 py-1.5 font-mono text-micro text-ink-faint">
            showing the newest {PICKER_PAGE} — refine the filter for older
            sessions
          </li>
        )}
      </ul>
    </div>
  );
}

function NumericRow({
  label,
  a,
  b,
  format = fmtCount,
  formatA = format,
  formatB = format,
  deltaFormat = format,
  direction,
  cost = false,
}: {
  label: string;
  a: number;
  b: number;
  format?: (n: number) => string;
  formatA?: (n: number) => string;
  formatB?: (n: number) => string;
  deltaFormat?: (n: number) => string;
  direction?: "lower";
  cost?: boolean;
}) {
  const delta = b - a;
  const valueClass = (value: number, other: number) =>
    cost
      ? "font-mono text-ink"
      : value > other
        ? "font-medium text-ink"
        : value < other
          ? "text-ink-dim"
          : "text-ink";
  const deltaTone =
    direction === "lower" && delta !== 0
      ? delta < 0
        ? "text-ok"
        : "text-warn"
      : "text-ink-dim";
  return (
    <tr>
      <td className="px-4 py-2 text-ink-dim">{label}</td>
      <td className={`px-4 py-2 text-right tabular-nums ${valueClass(a, b)}`}>
        {formatA(a)}
      </td>
      <td className={`px-4 py-2 text-right tabular-nums ${valueClass(b, a)}`}>
        {formatB(b)}
      </td>
      <td
        className={`px-4 py-2 text-right font-mono tabular-nums ${deltaTone}`}
      >
        {formatDelta(delta, deltaFormat)}
      </td>
    </tr>
  );
}

function TextRow({
  label,
  a,
  b,
  mono = false,
}: {
  label: string;
  a: string;
  b: string;
  mono?: boolean;
}) {
  return (
    <tr>
      <td className="px-4 py-2 text-ink-dim">{label}</td>
      <td
        className={`max-w-72 truncate px-4 py-2 text-right ${mono ? "font-mono text-xs" : ""}`}
        title={a}
      >
        {a || "—"}
      </td>
      <td
        className={`max-w-72 truncate px-4 py-2 text-right ${mono ? "font-mono text-xs" : ""}`}
        title={b}
      >
        {b || "—"}
      </td>
      <td className="px-4 py-2 text-right font-mono text-ink-faint">—</td>
    </tr>
  );
}

function ModelsRow({ a, b }: { a: string[]; b: string[] }) {
  const left = new Set(a);
  const right = new Set(b);
  return (
    <tr>
      <td className="px-4 py-2 text-ink-dim">Models</td>
      <td className="px-4 py-2">
        <ModelChips models={a} other={right} />
      </td>
      <td className="px-4 py-2">
        <ModelChips models={b} other={left} />
      </td>
      <td className="px-4 py-2 text-right font-mono text-ink-faint">—</td>
    </tr>
  );
}

function ModelChips({
  models,
  other,
}: {
  models: string[];
  other: Set<string>;
}) {
  if (models.length === 0)
    return <span className="block text-right text-ink-faint">—</span>;
  return (
    <div className="flex flex-wrap justify-end gap-1">
      {models.map((model) => {
        const shared = other.has(model);
        return (
          <span
            key={model}
            className={`rounded border px-2 py-0.5 font-mono text-xs ${
              shared
                ? "border-edge text-ink-faint"
                : "border-accent/40 text-accent"
            }`}
          >
            {model}
          </span>
        );
      })}
    </div>
  );
}

function formatDelta(delta: number, format: (n: number) => string): string {
  if (delta === 0) return "—";
  return `${delta > 0 ? "+" : "−"}${format(Math.abs(delta))}`;
}

function durationMs(session: SessionDetail): number {
  const start = new Date(session.createdAt).getTime();
  const end = new Date(session.modifiedAt).getTime();
  return Number.isFinite(start) && Number.isFinite(end)
    ? Math.max(0, end - start)
    : 0;
}

function fmtDuration(ms: number): string {
  const seconds = Math.round(ms / 1_000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  if (hours < 24)
    return remainder > 0 ? `${hours}h ${remainder}m` : `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

function useQueryDetail(agent?: string, id?: string) {
  return useQuery({
    queryKey: ["session", agent, id],
    queryFn: () => api.session(agent!, id!),
    enabled: Boolean(agent && id),
  });
}
