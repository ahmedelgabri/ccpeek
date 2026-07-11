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
}: {
  label: string;
  value: string;
  detail?: string;
  to?: string;
  tone?: "ok" | "warn";
}) {
  const body = (
    <>
      <div className="microlabel">{label}</div>
      <div
        className={`mt-1 font-mono text-xl leading-none font-medium tabular-nums ${
          tone === "ok" ? "text-ok" : tone === "warn" ? "text-warn" : "text-ink"
        }`}
      >
        {value}
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
  return <p className="px-3 py-6 text-center text-sm text-ink-faint">{children}</p>;
}
