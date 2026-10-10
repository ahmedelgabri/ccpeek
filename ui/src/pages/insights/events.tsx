import { Link } from "@tanstack/react-router";
import type { EventRow } from "../../api";
import { fmtWhen, fullWhen } from "../../time";
import { AgentChip, EmptyNote } from "../../ui";

// EVENT_LABEL names each event kind for reading; the stored slug is what
// filters and URLs carry.
export const EVENT_LABEL: Record<string, string> = {
  api_error: "API error",
  aborted: "aborted",
  tool_denied: "tool refused",
};

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
