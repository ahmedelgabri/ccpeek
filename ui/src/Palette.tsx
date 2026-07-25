import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { api, type SearchHit } from "./api";
import { markSnippet, stripMarkers } from "./snippet";
import { agentLabel, useDebounced } from "./ui";

interface Item {
  key: string;
  /** Plain label for pages and actions; hits render their snippet. */
  label?: string;
  hint?: string;
  hit?: SearchHit;
  run: () => void;
}

interface Group {
  heading: string;
  items: Item[];
}

// Mirrors the sidebar's order (see NAV in router.tsx): the jump list and
// the rail are two views of the same map, and disagreeing about the order
// makes the reader re-scan one of them.
const PAGES: { label: string; to: string }[] = [
  { label: "Overview", to: "/" },
  { label: "Sessions", to: "/sessions" },
  { label: "Usage", to: "/usage" },
  { label: "Commands", to: "/commands" },
  { label: "Artifacts", to: "/artifacts" },
  { label: "Secret scan", to: "/scan" },
  { label: "Compare", to: "/compare" },
];

const AGENTS = ["", "claude-code", "pi", "codex", "opencode", "cursor"];

// The palette has two modes, and jumping is the default one.
//
// "Jump" is what a palette is for: type two letters, hit enter, you are on
// the page. It is instant because it touches nothing but a local list.
// Search is a different act — it queries the whole corpus, it wants an
// agent filter, its results are passages rather than destinations — so it
// is an OPTION you step into rather than something that happens to every
// keystroke. Running both at once made every jump wait on a full-text
// query it never asked for.
type Mode = "jump" | "search";

export function Palette() {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<Mode>("jump");
  const [q, setQ] = useState("");
  const [agent, setAgent] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const navigate = useNavigate();
  const debouncedQ = useDebounced(q, 200);

  const enterSearch = () => {
    setMode("search");
    setCursor(0);
    inputRef.current?.focus();
  };
  const leaveSearch = () => {
    setMode("jump");
    setCursor(0);
    inputRef.current?.focus();
  };
  const close = () => {
    setOpen(false);
    setMode("jump");
  };

  useEffect(() => {
    // An explicit query (the /search doorway, a v1 bookmark) opens straight
    // into search — that request is unambiguous.
    const openPalette = (e: Event) => {
      const q = (e as CustomEvent<{ q?: string }>).detail?.q;
      if (q) {
        setQ(q);
        setMode("search");
      }
      setOpen(true);
      setCursor(0);
    };
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((v) => !v);
        setCursor(0);
      }
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

  // Escape steps back one level before it closes: out of search first,
  // then out of the palette. It listens on the window rather than the
  // input because clicking a row moves focus off the box, and escape has
  // to keep working from there.
  useEffect(() => {
    if (!open) return undefined;
    const onEsc = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      if (mode === "search") leaveSearch();
      else close();
    };
    window.addEventListener("keydown", onEsc);
    return () => window.removeEventListener("keydown", onEsc);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, mode]);

  const searching = mode === "search" && debouncedQ.trim().length >= 2;
  // The agent filter narrows on the SERVER: filtering the global top-N
  // client-side hid valid matches behind "No matches".
  const hits = useQuery({
    queryKey: ["search", debouncedQ, agent],
    queryFn: () => api.search(debouncedQ, agent, "30"),
    enabled: open && searching,
  });

  if (!open) return null;

  const groups: Group[] = [];

  if (mode === "jump") {
    const pages: Item[] = PAGES.filter((p) =>
      p.label.toLowerCase().includes(q.toLowerCase()),
    ).map((p) => ({
      key: `page:${p.to}`,
      label: p.label,
      run: () => {
        close();
        void navigate({ to: p.to });
      },
    }));
    if (pages.length > 0) groups.push({ heading: "Jump to", items: pages });
    // Search is an ACTION in the list, so it is discoverable without
    // knowing a shortcut and reachable with the same enter key as
    // everything else. It carries whatever has been typed, so "rate" +
    // enter on this row starts that search rather than discarding it.
    groups.push({
      heading: "Actions",
      items: [
        {
          key: "action:search",
          label: q.trim()
            ? `Search history for “${q.trim()}”`
            : "Search sessions, plans, todos, memories…",
          hint: "→",
          run: enterSearch,
        },
      ],
    });
  } else {
    const results: Item[] = (hits.data ?? []).map((h, i) => ({
      key: `hit:${i}`,
      hit: h,
      run: () => {
        close();
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
    if (results.length > 0) groups.push({ heading: "Matches", items: results });
  }

  const items = groups.flatMap((g) => g.items);
  const clamped = Math.min(cursor, Math.max(items.length - 1, 0));

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

  return (
    <div
      className="fixed inset-0 z-50 bg-black/50 p-4 pt-20"
      onClick={close}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="mx-auto flex max-h-[80vh] max-w-2xl flex-col overflow-hidden rounded-md border border-edge bg-surface-1 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-edge pr-3">
          {/* The mode is on screen, not remembered: a palette that behaves
              differently with no visible reason is a palette you stop
              trusting. */}
          {mode === "search" && (
            <button
              type="button"
              onClick={leaveSearch}
              title="Back to jump (esc)"
              className="ml-3 shrink-0 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-micro text-accent"
            >
              search ✕
            </button>
          )}
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
              if (e.key === "Enter") items[clamped]?.run();
              // Backspace on an empty search box leaves the mode, the way
              // a removable filter chip behaves everywhere else.
              if (e.key === "Backspace" && mode === "search" && q === "") {
                leaveSearch();
              }
            }}
            placeholder={
              mode === "search"
                ? "Search every session, plan, todo and memory…"
                : "Jump to a page…"
            }
            aria-label={mode === "search" ? "Search query" : "Jump to a page"}
            className={`min-w-0 flex-1 bg-transparent py-3 text-sm outline-none placeholder:text-ink-faint ${
              mode === "search" ? "px-2" : "px-4"
            }`}
          />
          {mode === "search" && (
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
          )}
        </div>

        <ul ref={listRef} className="min-h-0 flex-1 overflow-y-auto py-1">
          {(() => {
            let index = -1;
            return groups.map((g) => (
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
                          onClick={item.run}
                          onMouseEnter={() => setCursor(i)}
                          className={`flex w-full items-baseline gap-2 px-4 py-2 text-left ${
                            i === clamped ? "bg-surface-2" : ""
                          }`}
                        >
                          {item.hit ? (
                            <HitRow hit={item.hit} />
                          ) : (
                            <>
                              <span className="min-w-0 flex-1 truncate text-sm">
                                {item.label}
                              </span>
                              {item.hint && (
                                <span className="shrink-0 font-mono text-meta text-ink-faint">
                                  {item.hint}
                                </span>
                              )}
                            </>
                          )}
                        </button>
                      </li>
                    );
                  })}
                </ul>
              </li>
            ));
          })()}
          {mode === "search" && items.length === 0 && (
            <li className="px-4 py-3 text-sm text-ink-dim">
              {q.trim().length < 2
                ? "Keep typing — search needs two characters."
                : hits.isFetching
                  ? "Searching…"
                  : `No matches for “${q.trim()}”.`}
            </li>
          )}
        </ul>

        <div className="flex items-center gap-3 border-t border-edge px-4 py-1.5 font-mono text-micro text-ink-faint">
          <span>↑↓ move</span>
          <span>↵ {mode === "search" ? "open" : "go"}</span>
          <span>esc {mode === "search" ? "back" : "close"}</span>
          {mode === "search" && searching && (
            <span className="ml-auto tabular-nums">
              {hits.isFetching
                ? "searching…"
                : `${(hits.data ?? []).length} matches`}
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
    <div className="min-w-0 flex-1">
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
    </div>
  );
}
