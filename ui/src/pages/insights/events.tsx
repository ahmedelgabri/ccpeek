import { Link } from "@tanstack/react-router";
import type { EventRow } from "../../api";
import { fmtDuration, fmtWhen, fullWhen } from "../../time";
import { AgentChip, EmptyNote } from "../../ui";

// EVENT_LABEL names each event kind for reading; the stored slug is what
// filters and URLs carry.
export const EVENT_LABEL: Record<string, string> = {
  api_error: "API error",
  aborted: "aborted",
  tool_denied: "tool refused",
  pr_link: "pull request",
  turn: "turn",
  compaction: "compaction",
  hook: "hook",
};

// PullRequest is one PR with every session that linked it.
export interface PullRequest {
  url: string;
  repository: string;
  number?: number;
  last: string;
  sessions: EventRow[];
}

// groupPRs folds pr_link events (newest first) into one row per PR URL,
// keeping each session once.
export function groupPRs(events: EventRow[]): PullRequest[] {
  const byURL = new Map<string, PullRequest>();
  for (const e of events) {
    const url = e.detail ?? "";
    if (!url) continue;
    let pr = byURL.get(url);
    if (!pr) {
      const n = e.payload?.number;
      pr = {
        url,
        repository: e.label ?? "",
        number: typeof n === "number" ? n : undefined,
        last: e.at ?? "",
        sessions: [],
      };
      byURL.set(url, pr);
    }
    if (!pr.sessions.some((s) => s.sessionId === e.sessionId)) {
      pr.sessions.push(e);
    }
  }
  return [...byURL.values()];
}

// PRLink opens a pull request on its host. The URL comes from an agent
// transcript, so only http(s) links render as links at all.
export function PRLink({ pr }: { pr: PullRequest }) {
  const label = `${pr.repository}${pr.number ? `#${pr.number}` : ""}`;
  if (!/^https?:\/\//.test(pr.url)) return <span>{label}</span>;
  return (
    <a
      href={pr.url}
      target="_blank"
      rel="noreferrer noopener"
      className="font-mono text-accent hover:underline"
    >
      {label}
    </a>
  );
}

// EventList shows session events newest first, each linking to the
// transcript entry it arrived with. The Insights page lists them across
// sessions (showSession); a session's own Events tab drops the session
// column and jumps within the page instead.
export function EventList({
  events,
  showSession = true,
  onJump,
  empty = "No events in this range.",
}: {
  events: EventRow[];
  showSession?: boolean;
  onJump?: (seq: number) => void;
  empty?: string;
}) {
  if (events.length === 0) return <EmptyNote>{empty}</EmptyNote>;
  return (
    <ul className="divide-y divide-edge">
      {events.map((e, i) => (
        <li
          key={`${e.agent}/${e.sessionId}/${e.messageSeq}/${e.kind}/${i}`}
          className="flex min-w-0 items-baseline gap-3 px-3 py-2 text-sm"
        >
          <span
            title={fullWhen(e.at ?? "")}
            className="w-32 shrink-0 font-mono text-meta text-ink-faint tabular-nums"
          >
            {fmtWhen(e.at ?? "")}
          </span>
          {showSession && <AgentChip agent={e.agent} />}
          <span className="shrink-0 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-micro text-accent">
            {EVENT_LABEL[e.kind] ?? e.kind}
          </span>
          {e.durationMs !== undefined && (
            <span className="shrink-0 font-mono text-meta text-ink tabular-nums">
              {fmtDuration(e.durationMs)}
            </span>
          )}
          {(e.label || e.status) && (
            <span className="shrink-0 font-mono text-meta text-ink-dim">
              {[e.label, e.status].filter(Boolean).join(" · ")}
            </span>
          )}
          <span
            title={e.detail}
            className="min-w-0 flex-1 truncate text-meta text-ink-dim"
          >
            {e.detail}
          </span>
          {showSession ? (
            <Link
              to="/sessions/$agent/$sessionId"
              params={{ agent: e.agent, sessionId: e.sessionId }}
              search={{ seq: e.messageSeq }}
              title={e.sessionTitle}
              className="max-w-64 shrink-0 truncate text-meta text-accent hover:underline"
            >
              {e.sessionTitle || e.sessionId.slice(0, 8)} ↗#{e.messageSeq}
            </Link>
          ) : (
            onJump && (
              <button
                type="button"
                onClick={() => onJump(e.messageSeq)}
                title={`Jump to message #${e.messageSeq} in the transcript`}
                className="shrink-0 font-mono text-micro text-ink-faint tabular-nums hover:text-accent"
              >
                ↗ #{e.messageSeq}
              </button>
            )
          )}
        </li>
      ))}
    </ul>
  );
}
