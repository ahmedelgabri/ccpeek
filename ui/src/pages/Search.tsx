import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { api, plural } from "../api";
import { EmptyNote, FilterBar, PageHeader, useDebounced } from "../ui";
import { markSnippet } from "../snippet";

// Global search: every hit resolves to a session (directly, or via the
// artifact's links) — "have I solved this before?" for humans.
export function SearchPage() {
  const [qInput, setQInput] = useState("");
  // Settled before it reaches the index: typing used to run one full-text
  // query per character.
  const q = useDebounced(qInput, 250);
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
      <PageHeader
        title="Search"
        lede={
          enabled &&
          !isFetching && (
            <span className="font-mono text-meta text-ink-faint">
              {plural(hits.length, "hit")}
            </span>
          )
        }
      />
      <div className="mb-6 flex gap-2">
        <input
          autoFocus
          value={qInput}
          onChange={(e) => setQInput(e.target.value)}
          placeholder="Search sessions, plans, todos, memories…"
          aria-label="Search query"
          className="w-full rounded-md border border-edge bg-surface-1 px-4 py-2.5 text-sm placeholder:text-ink-faint"
        />
        {/* The date and model controls hide themselves when their
            handlers are absent, so this is just the agent select. */}
        <FilterBar agent={agent} onAgent={setAgent} />
      </div>

      {/* Each state says which one it is: an untouched box, a query too
          short to run, a query in flight, and a query that found nothing
          all rendered as "No matches." or as nothing at all. */}
      {!enabled && (
        <EmptyNote
          hint={
            qInput.trim().length > 0
              ? "Search needs at least two characters."
              : undefined
          }
        >
          {qInput.trim().length > 0
            ? "Keep typing…"
            : "Search every indexed session, plan, todo and memory."}
        </EmptyNote>
      )}
      {enabled && isFetching && hits.length === 0 && (
        <EmptyNote>Searching…</EmptyNote>
      )}
      {enabled && !isFetching && hits.length === 0 && (
        <EmptyNote hint="Try a shorter phrase, or clear the agent filter.">
          No matches for “{q}”.
        </EmptyNote>
      )}

      <ul className="space-y-2">
        {hits.map((h, i) => (
          <li
            key={i}
            className="rounded-md border border-edge bg-surface-1 px-4 py-3"
          >
            <div className="mb-1 flex min-w-0 gap-2 text-xs text-ink-dim">
              <span className="shrink-0 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-accent">
                {h.agent}
              </span>
              <span className="shrink-0">{h.docType}</span>
              {h.title && <span className="min-w-0 truncate">{h.title}</span>}
            </div>
            <p
              className="text-sm [&>mark]:rounded [&>mark]:bg-accent/30 [&>mark]:px-0.5 [&>mark]:text-ink"
              dangerouslySetInnerHTML={{ __html: markSnippet(h.snippet) }}
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
