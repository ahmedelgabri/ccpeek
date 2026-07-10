import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { api } from "../api";

// Global search: every hit resolves to a session (directly, or via the
// artifact's links) — "have I solved this before?" for humans.
export function SearchPage() {
  const [q, setQ] = useState("");
  const enabled = q.trim().length >= 2;

  const { data, isFetching } = useQuery({
    queryKey: ["search", q],
    queryFn: () => api.search(q),
    enabled,
  });

  const hits = data ?? [];

  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Search</h1>
      <input
        autoFocus
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Search sessions, plans, todos, memories…"
        className="mb-6 w-full rounded-lg border border-edge bg-surface-1 px-4 py-2.5 text-sm placeholder:text-ink-dim"
      />

      {enabled && !isFetching && hits.length === 0 && (
        <p className="text-ink-dim">No matches.</p>
      )}

      <ul className="space-y-2">
        {hits.map((h, i) => (
          <li key={i} className="rounded-lg border border-edge bg-surface-1 px-4 py-3">
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
                className="mt-1 inline-block text-xs text-accent hover:underline"
              >
                open session →
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
