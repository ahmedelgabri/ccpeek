import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { api } from "../api";
import { agentLabel } from "../ui";

const AGENTS = ["", "claude-code", "pi", "codex", "opencode", "cursor"];

// Global search: every hit resolves to a session (directly, or via the
// artifact's links) — "have I solved this before?" for humans.
export function SearchPage() {
  const [q, setQ] = useState("");
  const [agent, setAgent] = useState("");
  const enabled = q.trim().length >= 2;

  // The agent filter narrows on the SERVER: filtering the global top-N
  // client-side hid valid matches behind "No matches".
  const { data, isFetching } = useQuery({
    queryKey: ["search", q, agent],
    queryFn: () => api.search(q, agent),
    enabled,
  });

  const hits = data ?? [];

  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Search</h1>
      <div className="mb-6 flex gap-2">
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search sessions, plans, todos, memories…"
          className="w-full rounded-lg border border-edge bg-surface-1 px-4 py-2.5 text-sm placeholder:text-ink-dim"
        />
        <select
          value={agent}
          onChange={(e) => setAgent(e.target.value)}
          className="rounded-md border border-edge bg-surface-1 px-2 py-1.5 font-mono text-xs"
          aria-label="Filter by agent"
        >
          {AGENTS.map((a) => (
            <option key={a} value={a}>
              {a === "" ? "all agents" : agentLabel(a)}
            </option>
          ))}
        </select>
      </div>

      {enabled && !isFetching && hits.length === 0 && (
        <p className="text-ink-dim">No matches.</p>
      )}

      <ul className="space-y-2">
        {hits.map((h, i) => (
          <li
            key={i}
            className="rounded-lg border border-edge bg-surface-1 px-4 py-3"
          >
            <div className="mb-1 flex gap-2 text-xs text-ink-dim">
              <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-accent">
                {h.agent}
              </span>
              <span>{h.docType}</span>
              {h.title && <span className="truncate">{h.title}</span>}
            </div>
            <p
              className="text-sm [&>mark]:rounded [&>mark]:bg-accent/30 [&>mark]:px-0.5 [&>mark]:text-ink"
              dangerouslySetInnerHTML={{ __html: highlight(h.snippet) }}
            />
            {h.sessionId && (
              <Link
                to="/sessions/$agent/$sessionId"
                params={{ agent: h.agent, sessionId: h.sessionId }}
                search={{ seq: h.seq }}
                className="mt-1 inline-block text-xs text-accent hover:underline"
              >
                open at match →
              </Link>
            )}
            {h.artifact && (
              <Link
                to="/artifacts/$agent/$kind/$name"
                params={{ agent: h.agent, kind: h.docType, name: h.artifact }}
                className="mt-1 inline-block text-xs text-accent hover:underline"
              >
                open {h.docType.replaceAll("_", " ")} →
              </Link>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

// The API marks matches with [ and ]; escape everything else and convert
// the markers to <mark>.
function highlight(snippet: string): string {
  const escaped = snippet
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
  return escaped.replaceAll("[", "<mark>").replaceAll("]", "</mark>");
}
