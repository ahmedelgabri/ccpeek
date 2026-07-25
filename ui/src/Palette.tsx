import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { api, type SearchHit } from "./api";
import { markSnippet, stripMarkers } from "./snippet";
import { agentLabel, useDebounced } from "./ui";

interface Item {
  key: string;
  /** Plain label for pages; hits render their marked snippet instead. */
  label?: string;
  hit?: SearchHit;
  go: () => void;
}

interface Group {
  heading: string;
  items: Item[];
}

const PAGES: { label: string; to: string }[] = [
  { label: "Overview", to: "/" },
  { label: "Sessions", to: "/sessions" },
  { label: "Commands", to: "/commands" },
  { label: "Usage", to: "/usage" },
  { label: "Artifacts", to: "/artifacts" },
  { label: "Secret scan", to: "/scan" },
  { label: "Compare", to: "/compare" },
];

const AGENTS = ["", "claude-code", "pi", "codex", "opencode", "cursor"];

// The ⌘K palette IS search. There was a whole /search page whose only job
// was one input and a list of hits — a destination you had to navigate to
// in order to start looking, sitting beside a palette that already
// searched from anywhere and was one keystroke away. The page is gone and
// everything it could do lives here: the agent filter, the full result
// set, marked snippets, and artifact hits as well as session hits.
export function Palette() {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [agent, setAgent] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const navigate = useNavigate();
  // Typing runs a full-text query; without settling, every keystroke sent
  // one.
  const debouncedQ = useDebounced(q, 200);

  useEffect(() => {
    const openPalette = (e: Event) => {
      const q = (e as CustomEvent<{ q?: string }>).detail?.q;
      if (q !== undefined) setQ(q);
      setOpen(true);
      setCursor(0);
    };
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((v) => !v);
        setCursor(0);
      }
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener("ccpeek-palette", openPalette);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("ccpeek-palette", openPalette);
    };
  }, []);

  useEffect(() => {
    if (open) inputRef.current?.select();
  }, [open]);

  const searching = debouncedQ.trim().length >= 2;
  // The agent filter narrows on the SERVER: filtering the global top-N
  // client-side hid valid matches behind "No matches".
  const hits = useQuery({
    queryKey: ["search", debouncedQ, agent],
    queryFn: () => api.search(debouncedQ, agent, "30"),
    enabled: open && searching,
  });

  if (!open) return null;

  const pages: Item[] = PAGES.filter((p) =>
    p.label.toLowerCase().includes(q.toLowerCase()),
  ).map((p) => ({
    key: `page:${p.to}`,
    label: p.label,
    go: () => void navigate({ to: p.to }),
  }));

  const results: Item[] = (hits.data ?? []).map((h, i) => ({
    key: `hit:${i}`,
    hit: h,
    go: () => {
      if (h.sessionId) {
        // Land on the matching message.
        void navigate({
          to: "/sessions/$agent/$sessionId",
          params: { agent: h.agent, sessionId: h.sessionId },
          search: h.seq !== undefined ? { seq: h.seq } : {},
        });
      } else if (h.artifact) {
        void navigate({
          to: "/artifacts/$agent/$kind/$name",
          params: { agent: h.agent, kind: h.docType, name: h.artifact },
        });
      }
    },
  }));

  // Grouped, not one flat list: a page jump and a transcript hit are
  // different kinds of answer, and a mixed list made it impossible to see
  // which was which without reading every row.
  const groups: Group[] = [];
  if (pages.length > 0) groups.push({ heading: "Go to", items: pages });
  if (results.length > 0)
    groups.push({ heading: "Matches", items: results });

  const items = groups.flatMap((g) => g.items);
  const clamped = Math.min(cursor, Math.max(items.length - 1, 0));

  const run = (item: Item | undefined) => {
    if (!item) return;
    setOpen(false);
    item.go();
  };

  const move = (delta: number) => {
    setCursor((c) => {
      const next = Math.max(0, Math.min(c + delta, items.length - 1));
      // Keep the cursor in view — a 30-hit list scrolls.
      listRef.current
        ?.querySelector(`[data-idx="${next}"]`)
        ?.scrollIntoView({ block: "nearest" });
      return next;
    });
  };

  let index = -1;
  return (
    <div
      className="fixed inset-0 z-50 bg-black/50 p-4 pt-20"
      onClick={() => setOpen(false)}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Search and command palette"
        className="mx-auto flex max-h-[80vh] max-w-2xl flex-col overflow-hidden rounded-md border border-edge bg-surface-1 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-edge pr-3">
          <input
            ref={inputRef}
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setCursor(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                move(1);
              }
              if (e.key === "ArrowUp") {
                e.preventDefault();
                move(-1);
              }
              if (e.key === "Enter") run(items[clamped]);
            }}
            placeholder="Search sessions, plans, todos, memories — or jump to a page…"
            aria-label="Search query"
            className="min-w-0 flex-1 bg-transparent px-4 py-3 text-sm outline-none placeholder:text-ink-faint"
          />
          <select
            value={agent}
            onChange={(e) => setAgent(e.target.value)}
            aria-label="Filter by agent"
            className="shrink-0 rounded border border-edge bg-surface-1 px-1.5 py-1 font-mono text-xs text-ink-dim"
          >
            {AGENTS.map((a) => (
              <option key={a} value={a}>
                {a === "" ? "all agents" : agentLabel(a)}
              </option>
            ))}
          </select>
        </div>

        <ul ref={listRef} className="min-h-0 flex-1 overflow-y-auto py-1">
          {groups.map((g) => (
            <li key={g.heading}>
              <div className="microlabel px-4 pt-2 pb-1">{g.heading}</div>
              <ul>
                {g.items.map((item) => {
                  index += 1;
                  const i = index;
                  return (
                    <li key={item.key}>
                      <button
                        type="button"
                        data-idx={i}
                        onClick={() => run(item)}
                        onMouseEnter={() => setCursor(i)}
                        className={`block w-full px-4 py-2 text-left ${
                          i === clamped ? "bg-surface-2" : ""
                        }`}
                      >
                        {item.hit ? (
                          <HitRow hit={item.hit} />
                        ) : (
                          <span className="text-sm">{item.label}</span>
                        )}
                      </button>
                    </li>
                  );
                })}
              </ul>
            </li>
          ))}
          {/* The states typing can be in are distinguishable: untouched,
              too short to run, in flight, and genuinely empty. */}
          {items.length === 0 && (
            <li className="px-4 py-3 text-sm text-ink-dim">
              {q.trim().length === 0
                ? "Search every indexed session, plan, todo and memory."
                : q.trim().length < 2
                  ? "Keep typing — search needs two characters."
                  : hits.isFetching
                    ? "Searching…"
                    : `No matches for “${q.trim()}”.`}
            </li>
          )}
        </ul>

        <div className="flex items-center gap-3 border-t border-edge px-4 py-1.5 font-mono text-micro text-ink-faint">
          <span>↑↓ move</span>
          <span>↵ open</span>
          <span>esc close</span>
          {searching && (
            <span className="ml-auto tabular-nums">
              {hits.isFetching ? "searching…" : `${results.length} matches`}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

// HitRow shows what matched and where, with the FTS match marked — the
// palette used to render a stripped 70-character prefix, which for a code
// corpus was usually the part before anything interesting.
function HitRow({ hit }: { hit: SearchHit }) {
  return (
    <>
      <div className="mb-0.5 flex min-w-0 items-baseline gap-2 font-mono text-micro text-ink-faint">
        <span className="shrink-0 text-accent">{hit.agent}</span>
        <span className="shrink-0">{hit.docType.replaceAll("_", " ")}</span>
        {hit.title && <span className="min-w-0 truncate">{hit.title}</span>}
        <span className="ml-auto shrink-0">
          {hit.sessionId ? "session" : "artifact"}
        </span>
      </div>
      <p
        className="line-clamp-2 text-sm [&>mark]:rounded [&>mark]:bg-accent/30 [&>mark]:px-0.5 [&>mark]:text-ink"
        // markSnippet escapes the snippet before turning the FTS
        // delimiters into <mark>; stripMarkers is the plain-text fallback.
        dangerouslySetInnerHTML={{ __html: markSnippet(hit.snippet) }}
        title={stripMarkers(hit.snippet)}
      />
    </>
  );
}
