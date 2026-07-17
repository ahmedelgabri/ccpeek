import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { api } from "./api";

interface Item {
  label: string;
  hint?: string;
  go: () => void;
}

// ⌘K command palette: page navigation plus live search, keyboard-first.
export function Palette() {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();

  useEffect(() => {
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
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  const hits = useQuery({
    queryKey: ["palette-search", q],
    queryFn: () => api.search(q, "8"),
    enabled: open && q.trim().length >= 2,
  });

  if (!open) return null;

  const pages: Item[] = [
    { label: "Overview", go: () => void navigate({ to: "/" }) },
    { label: "Sessions", go: () => void navigate({ to: "/sessions" }) },
    { label: "Commands", go: () => void navigate({ to: "/commands" }) },
    { label: "Usage", go: () => void navigate({ to: "/usage" }) },
    { label: "Artifacts", go: () => void navigate({ to: "/artifacts" }) },
    { label: "Secret scan", go: () => void navigate({ to: "/scan" }) },
    { label: "Compare", go: () => void navigate({ to: "/compare" }) },
    { label: "Search", go: () => void navigate({ to: "/search" }) },
  ].filter((p) => p.label.toLowerCase().includes(q.toLowerCase()));

  const results: Item[] = (hits.data ?? [])
    .filter((h) => h.sessionId)
    .map((h) => ({
      label: h.snippet.replaceAll("[", "").replaceAll("]", "").slice(0, 70),
      hint: `${h.agent} · ${h.docType}`,
      // Land on the matching message, mirroring the Search page.
      go: () =>
        void navigate({
          to: "/sessions/$agent/$sessionId",
          params: { agent: h.agent, sessionId: h.sessionId! },
          search: h.seq !== undefined ? { seq: h.seq } : {},
        }),
    }));

  const items = [...pages, ...results];
  const clamped = Math.min(cursor, Math.max(items.length - 1, 0));

  const run = (item: Item | undefined) => {
    if (!item) return;
    setOpen(false);
    item.go();
  };

  return (
    <div
      className="fixed inset-0 z-50 bg-black/50 p-4 pt-24"
      onClick={() => setOpen(false)}
    >
      <div
        className="mx-auto max-w-lg overflow-hidden rounded-lg border border-edge bg-surface-1 shadow-xl"
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
          className="w-full border-b border-edge bg-transparent px-4 py-3 text-sm outline-none placeholder:text-ink-dim"
        />
        <ul className="max-h-80 overflow-y-auto py-1">
          {items.map((item, i) => (
            <li key={`${item.label}-${i}`}>
              <button
                onClick={() => run(item)}
                onMouseEnter={() => setCursor(i)}
                className={`flex w-full items-baseline gap-2 px-4 py-2 text-left text-sm ${
                  i === clamped ? "bg-surface-2" : ""
                }`}
              >
                <span className="truncate">{item.label}</span>
                {item.hint && (
                  <span className="ml-auto shrink-0 text-xs text-ink-dim">
                    {item.hint}
                  </span>
                )}
              </button>
            </li>
          ))}
          {items.length === 0 && (
            <li className="px-4 py-2 text-sm text-ink-dim">No matches.</li>
          )}
        </ul>
      </div>
    </div>
  );
}
