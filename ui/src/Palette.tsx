import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { api } from "./api";
import { stripMarkers } from "./snippet";
import { useDebounced } from "./ui";

interface Item {
  label: string;
  hint?: string;
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
  { label: "Search", to: "/search" },
];

// ⌘K command palette: page navigation plus live search, keyboard-first.
export function Palette() {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  // Typing runs a full-text query; without settling, every keystroke sent
  // one.
  const debouncedQ = useDebounced(q, 200);

  useEffect(() => {
    const openPalette = () => {
      setOpen(true);
      setQ("");
      setCursor(0);
    };
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((v) => !v);
        setQ("");
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
    if (open) inputRef.current?.focus();
  }, [open]);

  const searching = debouncedQ.trim().length >= 2;
  const hits = useQuery({
    queryKey: ["palette-search", debouncedQ],
    queryFn: () => api.search(debouncedQ, "", "8"),
    enabled: open && searching,
  });

  if (!open) return null;

  const pages: Item[] = PAGES.filter((p) =>
    p.label.toLowerCase().includes(q.toLowerCase()),
  ).map((p) => ({ label: p.label, go: () => void navigate({ to: p.to }) }));

  const results: Item[] = (hits.data ?? [])
    .filter((h) => h.sessionId)
    .map((h) => ({
      label: stripMarkers(h.snippet).slice(0, 70),
      hint: `${h.agent} · ${h.docType}`,
      // Land on the matching message, mirroring the Search page.
      go: () =>
        void navigate({
          to: "/sessions/$agent/$sessionId",
          params: { agent: h.agent, sessionId: h.sessionId! },
          search: h.seq !== undefined ? { seq: h.seq } : {},
        }),
    }));

  // Grouped, not one flat list: a page jump and a transcript hit are
  // different kinds of answer, and a mixed list made it impossible to see
  // which was which without reading every row.
  const groups: Group[] = [];
  if (pages.length > 0) groups.push({ heading: "Pages", items: pages });
  if (results.length > 0) groups.push({ heading: "Sessions", items: results });

  const items = groups.flatMap((g) => g.items);
  const clamped = Math.min(cursor, Math.max(items.length - 1, 0));

  const run = (item: Item | undefined) => {
    if (!item) return;
    setOpen(false);
    item.go();
  };

  let index = -1;
  return (
    <div
      className="fixed inset-0 z-50 bg-black/50 p-4 pt-24"
      onClick={() => setOpen(false)}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="mx-auto max-w-lg overflow-hidden rounded-md border border-edge bg-surface-1 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
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
              setCursor((c) => Math.min(c + 1, items.length - 1));
            }
            if (e.key === "ArrowUp") {
              e.preventDefault();
              setCursor((c) => Math.max(c - 1, 0));
            }
            if (e.key === "Enter") run(items[clamped]);
          }}
          placeholder="Jump to page or search sessions…"
          className="w-full border-b border-edge bg-transparent px-4 py-3 text-sm outline-none placeholder:text-ink-faint"
        />
        <ul className="max-h-80 overflow-y-auto py-1">
          {groups.map((g) => (
            <li key={g.heading}>
              <div className="microlabel px-4 pt-2 pb-1">{g.heading}</div>
              <ul>
                {g.items.map((item) => {
                  index += 1;
                  const i = index;
                  return (
                    <li key={`${item.label}-${i}`}>
                      <button
                        type="button"
                        onClick={() => run(item)}
                        onMouseEnter={() => setCursor(i)}
                        className={`flex w-full items-baseline gap-2 px-4 py-2 text-left text-sm ${
                          i === clamped ? "bg-surface-2" : ""
                        }`}
                      >
                        <span className="min-w-0 truncate">{item.label}</span>
                        {item.hint && (
                          <span className="ml-auto shrink-0 font-mono text-meta text-ink-faint">
                            {item.hint}
                          </span>
                        )}
                      </button>
                    </li>
                  );
                })}
              </ul>
            </li>
          ))}
          {/* The three states typing can be in were previously
              indistinguishable — all of them rendered "No matches." */}
          {items.length === 0 && (
            <li className="px-4 py-3 text-sm text-ink-dim">
              {q.trim().length === 0
                ? "Type to search your sessions."
                : q.trim().length < 2
                  ? "Keep typing — search needs two characters."
                  : hits.isFetching
                    ? "Searching…"
                    : "No matches."}
            </li>
          )}
          {items.length > 0 && searching && hits.isFetching && (
            <li className="px-4 py-2 font-mono text-meta text-ink-faint">
              searching…
            </li>
          )}
        </ul>
        <div className="flex items-center gap-3 border-t border-edge px-4 py-1.5 font-mono text-micro text-ink-faint">
          <span>↑↓ move</span>
          <span>↵ open</span>
          <span>esc close</span>
        </div>
      </div>
    </div>
  );
}
