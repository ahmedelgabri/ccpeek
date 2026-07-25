import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { useHighlight } from "../../highlight";
import { shortPath, type ToolCallRow, type TranscriptMessage } from "../../api";
import type { TranscriptWindow } from "./useSessionData";
import { EmptyNote, SkeletonRows, toolColor, useToggleSet } from "../../ui";
import { ToolExpansion } from "./ToolExpansion";

export function Transcript({
  agent,
  sessionId,
  total,
  transcript,
}: {
  agent: string;
  sessionId: string;
  /** The session's message count, which exceeds what is loaded. */
  total: number;
  transcript: TranscriptWindow;
}) {
  const {
    msgs,
    chipRows: tools,
    focusSeq,
    loading,
    hasMore,
    loadingMore,
    loadMore: onLoadMore,
    hasOlder,
    loadingOlder,
    loadOlder: onLoadOlder,
    trackSeq: onScrollSeq,
    copyPermalink: onPermalink,
  } = transcript;
  const [treeView, setTreeView] = useState(false);
  const [copiedSeq, setCopiedSeq] = useState<number | null>(null);
  // Meta entries (toolResult, system, …) render as one-line excerpts;
  // clicking reveals the full stored text.
  const [openMeta, toggleMeta] = useToggleSet<number>();
  const container = useRef<HTMLDivElement>(null);
  const depths = useMemo(() => computeDepths(msgs), [msgs]);
  const toolsByMsg = useMemo(() => {
    const map = new Map<number, ToolCallRow[]>();
    for (const t of tools) {
      const list = map.get(t.messageSeq) ?? [];
      list.push(t);
      map.set(t.messageSeq, list);
    }
    return map;
  }, [tools]);

  // Tool-only messages carry no prose: fold them into their tool chips
  // and drop rows that have neither text nor tools (unless deep-linked).
  // Memoized like its two neighbours above: it runs over the whole loaded
  // transcript, it feeds the virtualizer's count and key function, and its
  // identity is a dependency of two effects — so an unmemoized rebuild
  // costs a pass over every loaded message on every scroll frame.
  const visible = useMemo(
    () =>
      msgs.filter(
        (m) =>
          m.text.trim() !== "" ||
          (toolsByMsg.get(m.seq)?.length ?? 0) > 0 ||
          m.seq === focusSeq,
      ),
    [msgs, toolsByMsg, focusSeq],
  );
  const hidden = msgs.length - visible.length;

  // DOM windowing: only the on-screen slice of a session is mounted —
  // scrolling a 10k-message transcript must not accumulate thousands of
  // markdown nodes and highlight blocks. Rows are keyed by seq so
  // measured heights survive page prepends, and heights are measured
  // (markdown, diffs, expandable chips are all variable).
  const listRef = useRef<HTMLOListElement>(null);
  const listOffset = useRef(0);
  useLayoutEffect(() => {
    listOffset.current = listRef.current?.offsetTop ?? 0;
  }, []);
  const virtualizer = useWindowVirtualizer({
    count: visible.length,
    estimateSize: () => 96,
    overscan: 10,
    scrollMargin: listOffset.current,
    getItemKey: (i) => visible[i].seq,
  });
  const virtualItems = virtualizer.getVirtualItems();
  const range = virtualizer.range;
  // Highlight re-runs as the mounted window moves — freshly mounted rows
  // need their code blocks processed (already-highlighted ones are
  // skipped by the :not(.hljs) selector).
  useHighlight(container, [
    msgs,
    virtualItems[0]?.key,
    virtualItems[virtualItems.length - 1]?.key,
  ]);

  // Deep link (?seq=N): scroll the target into view once, when it first
  // lands in the loaded window. A ref (not state) gates it so later page
  // loads don't yank the reader back to the anchor. focusDone also unlocks
  // the older-direction observer, so the anchored page settles before we
  // start auto-filling context above it.
  // focusDone is both halves of the gate: "the deep link has been
  // scrolled to" and "the older-direction auto-fill may start". With no
  // deep link there is nothing to wait for, so it begins true.
  const [focusDone, setFocusDone] = useState(focusSeq === undefined);
  useEffect(() => {
    setFocusDone(focusSeq === undefined);
  }, [focusSeq]);
  useEffect(() => {
    if (focusSeq === undefined || focusDone) return;
    const idx = visible.findIndex((m) => m.seq === focusSeq);
    if (idx < 0) return;
    virtualizer.scrollToIndex(idx, { align: "center" });
    setFocusDone(true);
  }, [focusSeq, focusDone, visible, virtualizer]);

  // Prepending older messages grows the document above the viewport, which
  // would jump the reader downward. Capture the height just before an older
  // fetch and restore the scroll offset by the delta once it commits.
  const beforeOlder = useRef<number | null>(null);
  const loadOlder = () => {
    if (loadingOlder) return;
    beforeOlder.current = document.documentElement.scrollHeight;
    onLoadOlder();
  };
  useLayoutEffect(() => {
    if (beforeOlder.current === null) return;
    const delta = document.documentElement.scrollHeight - beforeOlder.current;
    beforeOlder.current = null;
    if (delta > 0) window.scrollBy(0, delta);
  }, [msgs]);

  // The virtualizer's visible range drives the infinite scroll: nearing
  // the last rows pulls newer pages, nearing the first pulls older ones
  // (once the deep-link scroll has settled). The buttons above/below stay
  // as the explicit, accessible fallback.
  const rangeStart = range?.startIndex;
  const rangeEnd = range?.endIndex;
  useEffect(() => {
    if (!hasMore || loadingMore || rangeEnd === undefined) return;
    if (rangeEnd >= visible.length - 8) onLoadMore();
  }, [hasMore, loadingMore, rangeEnd, visible.length, onLoadMore]);
  useEffect(() => {
    if (!hasOlder || loadingOlder || !focusDone || rangeStart === undefined)
      return;
    if (rangeStart <= 4) {
      beforeOlder.current = document.documentElement.scrollHeight;
      onLoadOlder();
    }
  }, [hasOlder, loadingOlder, focusDone, rangeStart, onLoadOlder]);

  // Scroll-spy: keep the URL pointed at the topmost message in view so the
  // address bar is always a shareable link to the reader's spot. Gated on a
  // real scroll so a fresh page load doesn't inject ?seq=0; the target of a
  // deep link already sits in the URL until then. A thin band at the top of
  // the viewport marks the "current" row.
  const userScrolled = useRef(false);
  // A permalink click pins the URL to that message; scroll-spy stays quiet
  // for a beat afterwards so a layout settle doesn't overwrite it. Real
  // scrolling resumes tracking.
  const spyResumeAt = useRef(0);
  useEffect(() => {
    const onScroll = () => {
      userScrolled.current = true;
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);
  useEffect(() => {
    if (rangeStart === undefined) return;
    if (!userScrolled.current || Date.now() < spyResumeAt.current) return;
    const m = visible[rangeStart];
    if (m) onScrollSeq(m.seq);
  }, [rangeStart, visible, onScrollSeq]);

  const copyPermalink = (seq: number) => {
    spyResumeAt.current = Date.now() + 800;
    onPermalink(seq);
    setCopiedSeq(seq);
    window.setTimeout(
      () => setCopiedSeq((cur) => (cur === seq ? null : cur)),
      1200,
    );
  };

  return (
    <div ref={container}>
      <div className="mb-2 flex items-center gap-3">
        <span className="font-mono text-[11px] text-ink-faint">
          {total > msgs.length
            ? `${msgs.length} of ${total} messages`
            : `${total} messages`}
          {hidden > 0 && ` · ${hidden} empty folded`}
        </span>
        <button
          onClick={() => setTreeView((v) => !v)}
          className={`ml-auto rounded-md border border-edge px-2 py-1 font-mono text-xs ${
            treeView ? "bg-surface-2 text-ink" : "text-ink-dim hover:text-ink"
          }`}
        >
          tree view
        </button>
      </div>
      {hasOlder && (
        <div className="mb-3 flex justify-center">
          <button
            onClick={loadOlder}
            disabled={loadingOlder}
            className="rounded-md border border-edge px-3 py-1.5 font-mono text-xs text-ink-dim hover:text-ink disabled:opacity-50"
          >
            {loadingOlder ? "Loading…" : "Load older"}
          </button>
        </div>
      )}
      <ol
        ref={listRef}
        className="relative"
        style={{ height: virtualizer.getTotalSize() }}
      >
        {virtualItems.map((vi) => {
          const m = visible[vi.index];
          const msgTools = toolsByMsg.get(m.seq) ?? [];
          // Three visual registers: the user's prompts (accent rule,
          // raised surface), the assistant's replies (quiet card), and
          // meta entries — system events, summaries, tool-only rows —
          // (compact, dimmed, dashed).
          const isUser = m.role === "user" && m.kind === "message";
          const isAssistant = m.role === "assistant";
          const isMeta = !isUser && !isAssistant;
          const glyph = isUser ? "❯" : isAssistant ? "✦" : "·";
          return (
            <li
              key={vi.key}
              id={`seq-${m.seq}`}
              data-index={vi.index}
              ref={virtualizer.measureElement}
              className="absolute top-0 left-0 w-full pb-2"
              style={{
                transform: `translateY(${vi.start - virtualizer.options.scrollMargin}px)`,
              }}
            >
              <div
                style={
                  treeView
                    ? {
                        marginLeft: `${Math.min(depths.get(m.seq) ?? 0, 12) * 16}px`,
                      }
                    : undefined
                }
                className={`rounded-md border border-l-2 ${
                  m.seq === focusSeq
                    ? "border-accent"
                    : isUser
                      ? "border-edge border-l-accent"
                      : isMeta
                        ? "border-edge border-dashed"
                        : "border-edge border-l-assistant/60"
                } ${
                  isUser
                    ? "bg-[color-mix(in_oklab,var(--color-accent)_7%,var(--color-surface-1))] p-3"
                    : isMeta
                      ? "bg-transparent px-3 py-1.5"
                      : "bg-surface-1 p-3"
                } ${m.isSidechain && !treeView ? "ml-8 border-dashed" : ""} ${
                  m.isSidechain && treeView ? "border-dashed" : ""
                }`}
              >
                <div
                  onClick={
                    isMeta && m.text.trim() !== ""
                      ? () => toggleMeta(m.seq)
                      : undefined
                  }
                  className={`flex gap-2 font-mono text-[11px] text-ink-faint ${isMeta ? "" : "mb-1"} ${
                    isMeta && m.text.trim() !== ""
                      ? "cursor-pointer hover:text-ink-dim"
                      : ""
                  }`}
                >
                  <span
                    className={
                      isAssistant
                        ? "text-assistant"
                        : isUser
                          ? "font-medium text-accent"
                          : ""
                    }
                  >
                    {m.isSidechain ? "↳ " : ""}
                    {glyph} {m.role}
                    {m.kind !== "message" ? ` · ${m.kind}` : ""}
                  </span>
                  {m.model && <span>{m.model}</span>}
                  {isMeta && m.text.trim() !== "" && (
                    <>
                      <span className="text-accent">
                        {openMeta.has(m.seq) ? "▾" : "▸"}
                      </span>
                      {!openMeta.has(m.seq) && (
                        <span className="truncate text-ink-faint italic">
                          {m.text.slice(0, 120)}
                        </span>
                      )}
                    </>
                  )}
                  <span className="ml-auto flex items-center gap-1 tabular-nums">
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        copyPermalink(m.seq);
                      }}
                      title="Copy link to this message"
                      className={
                        copiedSeq === m.seq ? "text-ok" : "hover:text-accent"
                      }
                    >
                      {copiedSeq === m.seq ? "link copied ✓" : `#${m.seq}`}
                    </button>
                    {m.createdAt && <span>· {m.createdAt.slice(11, 19)}</span>}
                  </span>
                </div>
                {isMeta && openMeta.has(m.seq) && (
                  <pre className="mt-1.5 max-h-96 overflow-auto rounded-md border border-edge bg-surface px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap">
                    {m.text}
                  </pre>
                )}
                {!isMeta &&
                  m.text.trim() !== "" &&
                  (m.html ? (
                    <div
                      className="prose-msg"
                      dangerouslySetInnerHTML={{ __html: m.html }}
                    />
                  ) : (
                    <div className="text-sm leading-relaxed whitespace-pre-wrap">
                      {m.text}
                    </div>
                  ))}
                {msgTools.length > 0 && (
                  <MessageTools
                    agent={agent}
                    sessionId={sessionId}
                    tools={msgTools}
                    className={m.text.trim() !== "" ? "mt-2" : ""}
                  />
                )}
              </div>
            </li>
          );
        })}
      </ol>
      {hasMore && (
        <div className="mt-3 flex justify-center">
          <button
            onClick={onLoadMore}
            disabled={loadingMore}
            className="rounded-md border border-edge px-3 py-1.5 font-mono text-xs text-ink-dim hover:text-ink disabled:opacity-50"
          >
            {loadingMore
              ? "Loading…"
              : `Load newer — ${msgs.length} of ${total}`}
          </button>
        </div>
      )}
      {loading && msgs.length === 0 && <SkeletonRows rows={6} />}
      {!loading && msgs.length === 0 && (
        <EmptyNote>No transcript entries.</EmptyNote>
      )}
    </div>
  );
}
// MessageTools renders a message's tool calls as kind-colored chips;
// expandable chips fetch their full payload (diff excerpts, complete
// command) only when opened — chip rows never carry them.
function MessageTools({
  agent,
  sessionId,
  tools,
  className = "",
}: {
  agent: string;
  sessionId: string;
  tools: ToolCallRow[];
  className?: string;
}) {
  const [open, toggle] = useToggleSet<number>();

  return (
    <div className={className}>
      <div className="flex flex-wrap gap-1.5">
        {tools.map((t) => {
          // Expand only when there is more than the chip already shows: a
          // diff (edits and writes carry payloads behind the detail
          // lookup), a full shell command, or a truncated detail.
          const expandable =
            t.kind === "file_edit" ||
            t.kind === "file_write" ||
            (t.kind === "shell" && Boolean(t.detail)) ||
            Boolean(t.detail && t.detail.length > 80);
          const isOpen = open.has(t.seq);
          return (
            <button
              key={t.seq}
              onClick={expandable ? () => toggle(t.seq) : undefined}
              title={t.detail}
              className={`inline-flex max-w-full items-baseline gap-1.5 rounded border px-1.5 py-0.5 font-mono text-[11px] ${
                isOpen
                  ? "border-edge-strong bg-surface-2"
                  : "border-edge bg-surface-2/60"
              } ${expandable ? "cursor-pointer hover:border-edge-strong" : "cursor-default"}`}
            >
              <span
                aria-hidden
                className="inline-block h-1.5 w-1.5 shrink-0 self-center rounded-full"
                style={{ background: toolColor(t.kind) }}
              />
              <span style={{ color: toolColor(t.kind) }}>{t.name}</span>
              {t.detail && (
                <span className="truncate text-ink-dim">
                  {t.kind === "shell"
                    ? t.detail.split("\n")[0].slice(0, 80)
                    : shortPath(t.detail)}
                </span>
              )}
              {expandable && (
                <span className="text-ink-faint">{isOpen ? "▾" : "▸"}</span>
              )}
            </button>
          );
        })}
      </div>
      {tools
        .filter((t) => open.has(t.seq))
        .map((t) => (
          <div key={t.seq} className="mt-2">
            <div className="mb-1 font-mono text-[10px] text-ink-faint">
              <span style={{ color: toolColor(t.kind) }}>{t.name}</span>
              {t.detail && t.kind !== "shell" && <> · {shortPath(t.detail)}</>}
            </div>
            <ToolExpansion agent={agent} sessionId={sessionId} seq={t.seq} />
          </div>
        ))}
    </div>
  );
}
// computeDepths walks the agent-native entry tree (Claude parentUuid, Pi
// id/parentId): an entry's depth is parent depth + 1 when the parent is
// NOT the immediately preceding entry (a real branch), else parent depth —
// keeping the main thread flat while branches indent.
function computeDepths(msgs: TranscriptMessage[]): Map<number, number> {
  const bySeq = new Map<number, number>();
  const byExternal = new Map<string, TranscriptMessage>();
  for (const m of msgs) {
    if (m.externalId) byExternal.set(m.externalId, m);
  }
  let prev: TranscriptMessage | undefined;
  for (const m of msgs) {
    let depth = 0;
    const parent = m.parentId ? byExternal.get(m.parentId) : undefined;
    if (parent) {
      const parentDepth = bySeq.get(parent.seq) ?? 0;
      depth = prev && parent.seq !== prev.seq ? parentDepth + 1 : parentDepth;
    } else if (m.isSidechain) {
      depth = 1;
    }
    bySeq.set(m.seq, depth);
    prev = m;
  }
  return bySeq;
}
