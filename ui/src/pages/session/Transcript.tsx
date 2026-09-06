import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { useHighlight } from "../../highlight";
import { useRowWindow } from "../../windowed";
import {
  fmtCount,
  plural,
  shortPath,
  type ToolCallRow,
  type TranscriptMessage,
} from "../../api";
import { fullWhen, localClock } from "../../time";
import type { TranscriptWindow } from "./useSessionData";
import {
  EmptyNote,
  LoadError,
  onDisclosureKey,
  PALETTE_KEY,
  Segmented,
  SkeletonRows,
  openPalette,
  toolColor,
  useDebounced,
  useSlashFocus,
  useToggleSet,
} from "../../ui";
import { ToolExpansion } from "./ToolExpansion";

// Transcript lenses: the reader's filter over a session that can run to
// thousands of entries.
type Lens = "all" | "talk" | "prompts" | "tools";

// Scrubber bucket count — see the component below.
const SCRUB_BUCKETS = 160;

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
    error,
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
  const [lens, setLens] = useState<Lens>("all");
  const [find, setFind] = useState("");
  const [copiedSeq, setCopiedSeq] = useState<number | null>(null);
  const findBox = useRef<HTMLInputElement>(null);
  useSlashFocus(findBox);
  // Meta entries (toolResult, system, …) render as one-line excerpts;
  // clicking reveals the full stored text.
  const [openMeta, toggleMeta] = useToggleSet<number>();
  // Which tool chips are expanded, keyed by call seq. This lives at the
  // LIST level, not inside the row: rows are virtualized, so state held in
  // one unmounts the moment the reader scrolls it off screen — an opened
  // diff collapsed itself behind their back and re-fetched its payload on
  // the way back up.
  const [openTools, toggleTool] = useToggleSet<number>();
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
  const withContent = useMemo(
    () =>
      msgs.filter(
        (m) =>
          m.text.trim() !== "" ||
          (toolsByMsg.get(m.seq)?.length ?? 0) > 0 ||
          m.seq === focusSeq,
      ),
    [msgs, toolsByMsg, focusSeq],
  );
  // On top of that the reader picks a lens. A thousand-message session is
  // a document, and "show me only what I asked for" or "only what it ran"
  // is how you navigate one — scrolling was previously the only tool.
  // Debounced like every sibling filter (Commands, Sessions, Compare, the
  // palette): a thousand-message session is megabytes of text, and an
  // undebounced needle re-scanned all of it — and re-rendered the windowed
  // list — on every keystroke.
  const needle = useDebounced(find.trim().toLowerCase(), 200);
  // Lowercased ONCE per loaded page rather than once per keystroke.
  const haystack = useMemo(
    () => new Map(withContent.map((m) => [m.seq, m.text.toLowerCase()])),
    [withContent],
  );
  const visible = useMemo(
    () =>
      withContent.filter((m) => {
        if (m.seq === focusSeq) return true;
        const isUser = m.role === "user" && m.kind === "message";
        const isAssistant = m.role === "assistant";
        const hasTools = (toolsByMsg.get(m.seq)?.length ?? 0) > 0;
        if (lens === "prompts" && !isUser) return false;
        if (lens === "tools" && !hasTools) return false;
        if (lens === "talk" && !isUser && !isAssistant) return false;
        if (needle && !haystack.get(m.seq)?.includes(needle)) return false;
        return true;
      }),
    [withContent, haystack, toolsByMsg, focusSeq, lens, needle],
  );
  const hidden = msgs.length - withContent.length;
  const filteredOut = withContent.length - visible.length;

  // Rows are keyed by seq so measured heights survive page prepends. The
  // transcript uses the hook rather than WindowedList because it needs the
  // virtualizer itself: the visible range drives infinite scroll in both
  // directions, and deep links scroll a specific index into view.
  const { listRef, virtualizer, virtualItems } = useRowWindow<
    TranscriptMessage,
    HTMLOListElement
  >(visible, (m) => m.seq, 96);
  const range = virtualizer.range;
  // Highlight re-runs as the mounted window moves — freshly mounted rows
  // need their code blocks processed (already-highlighted ones are
  // skipped by the :not([data-hl]) selector).
  useHighlight(container, [
    msgs,
    virtualItems[0]?.key,
    virtualItems[virtualItems.length - 1]?.key,
  ]);

  // Scroll-spy state, declared before the scrolls that have to silence it.
  // `userScrolled` gates the spy on a real reader gesture so a fresh page
  // load never injects ?seq=0; `spyResumeAt` is the quiet window around a
  // scroll WE caused — a deep link, a permalink, a scrubber jump — during
  // which the resulting scroll events are ours, not the reader's.
  const userScrolled = useRef(false);
  const spyResumeAt = useRef(0);
  const quietSpy = (ms = 800) => {
    spyResumeAt.current = Date.now() + ms;
  };

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
  useLayoutEffect(() => {
    if (focusSeq === undefined || focusDone) return;
    const idx = visible.findIndex((m) => m.seq === focusSeq);
    if (idx < 0) return;
    quietSpy();
    const row = document.getElementById(`seq-${focusSeq}`);
    if (row) {
      // Align the mounted row before paint, then release the gate in the
      // same layout commit. A delayed retry loop can steal the reader's
      // first scroll after the target appears.
      row.scrollIntoView({ block: "center", behavior: "instant" });
      setFocusDone(true);
    } else {
      // Estimated scrolling only mounts the target. The next mounted
      // window completes positioning against its actual DOM geometry.
      virtualizer.scrollToIndex(idx, { align: "center" });
    }
    // quietSpy only touches a ref; window edges identify newly mounted rows.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    focusSeq,
    focusDone,
    visible,
    virtualizer,
    virtualItems[0]?.key,
    virtualItems[virtualItems.length - 1]?.key,
    virtualizer.options.scrollMargin,
  ]);

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
    // The virtual range can lag a programmatic scroll by a frame. Check
    // the real list position too, or releasing the focus gate can prepend
    // old pages while the reader is already at the middle-of-session target.
    const top = listRef.current?.getBoundingClientRect().top;
    if (rangeStart <= 4 && top !== undefined && top >= -window.innerHeight) {
      beforeOlder.current = document.documentElement.scrollHeight;
      onLoadOlder();
    }
  }, [hasOlder, loadingOlder, focusDone, rangeStart, onLoadOlder, listRef]);

  // Scroll-spy: keep the URL pointed at the topmost message in view so the
  // address bar is always a shareable link to the reader's spot. During a
  // quiet window the scroll is one we caused, so neither half of the spy
  // engages — the flag stays down and no URL is written. (The write itself
  // is throttled in useTranscriptWindow: one replaceState per topmost row
  // walks straight into Safari's rate limit on a fast scroll.)
  useEffect(() => {
    const onScroll = () => {
      if (Date.now() < spyResumeAt.current) return;
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
    quietSpy();
    onPermalink(seq);
    setCopiedSeq(seq);
    window.setTimeout(
      () => setCopiedSeq((cur) => (cur === seq ? null : cur)),
      1200,
    );
  };

  return (
    <div ref={container}>
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="font-mono text-meta text-ink-faint">
          {total > msgs.length
            ? `${fmtCount(msgs.length)} of ${fmtCount(total)} messages`
            : plural(total, "message")}
          {hidden > 0 && ` · ${fmtCount(hidden)} empty folded`}
          {filteredOut > 0 && ` · ${fmtCount(filteredOut)} filtered out`}
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <input
            ref={findBox}
            value={find}
            onChange={(e) => setFind(e.target.value)}
            placeholder="Find in transcript…"
            aria-label="Find in loaded transcript"
            title="Press / to focus"
            className="w-44 rounded-md border border-edge bg-surface-1 px-2 py-1 font-mono text-xs placeholder:text-ink-faint"
          />
          <Segmented
            label="Transcript lens"
            value={lens}
            onChange={setLens}
            options={[
              { value: "all", label: "all" },
              { value: "talk", label: "talk" },
              { value: "prompts", label: "prompts" },
              { value: "tools", label: "tools" },
            ]}
          />
          <button
            type="button"
            onClick={() => setTreeView((v) => !v)}
            aria-pressed={treeView}
            className={`rounded-md border border-edge px-2 py-1.5 font-mono text-xs transition-colors hover:border-edge-strong ${
              treeView ? "bg-surface-2 text-ink" : "text-ink-dim hover:text-ink"
            }`}
          >
            tree
          </button>
        </div>
      </div>
      {/* The find box searches the LOADED window, not the whole session —
          saying so is the difference between a filter and a lie. */}
      {needle && (
        <p className="mb-2 font-mono text-micro text-ink-faint">
          Searching the {fmtCount(msgs.length)} loaded messages. Use{" "}
          {/* The needle travels. The escape hatch used to open an empty
              palette, so taking it meant typing the same query a second
              time — the /search doorway has carried its query for as long
              as it has existed. */}
          <button
            type="button"
            onClick={() => openPalette(find.trim())}
            className="text-accent hover:underline"
          >
            {PALETTE_KEY} search
          </button>{" "}
          to cover every session.
        </p>
      )}
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
      {/* The rail is a sibling in a flex row, not an overlay: it reserves
          its own column so it can never sit on top of the transcript. */}
      <div className="flex gap-3">
        <ol
          ref={listRef}
          className="relative min-w-0 flex-1"
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
                  {/* An expandable meta line is a disclosure control, so it
                      carries the semantics of one: reachable by tab, opened
                      with enter or space, and announced as expanded or
                      collapsed. It was a mouse-only <div>. */}
                  <div
                    {...(isMeta && m.text.trim() !== ""
                      ? {
                          role: "button",
                          tabIndex: 0,
                          "aria-expanded": openMeta.has(m.seq),
                          onClick: () => toggleMeta(m.seq),
                          onKeyDown: (e: ReactKeyboardEvent<HTMLElement>) =>
                            onDisclosureKey(e, () => toggleMeta(m.seq)),
                        }
                      : {})}
                    className={`flex min-w-0 gap-2 font-mono text-meta text-ink-faint ${isMeta ? "" : "mb-1"} ${
                      isMeta && m.text.trim() !== ""
                        ? "cursor-pointer hover:text-ink-dim"
                        : ""
                    }`}
                  >
                    {/* Identity and coordinates sit TOGETHER at the head of
                      the message. The seq and timestamp used to be flung
                      to the far right edge — 1200px from the role they
                      belong to, so reading "who said this, and when" meant
                      crossing the full width of the screen and back. */}
                    <span
                      className={`shrink-0 ${
                        isAssistant
                          ? "text-assistant"
                          : isUser
                            ? "font-medium text-accent"
                            : ""
                      }`}
                    >
                      {m.isSidechain ? "↳ " : ""}
                      {glyph} {m.role}
                      {m.kind !== "message" ? ` · ${m.kind}` : ""}
                    </span>
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        copyPermalink(m.seq);
                      }}
                      title="Copy link to this message"
                      className={`shrink-0 tabular-nums ${
                        copiedSeq === m.seq ? "text-ok" : "hover:text-accent"
                      }`}
                    >
                      {copiedSeq === m.seq ? "link copied ✓" : `#${m.seq}`}
                    </button>
                    {m.createdAt && (
                      <span
                        title={fullWhen(m.createdAt)}
                        className="shrink-0 tabular-nums"
                      >
                        {localClock(m.createdAt)}
                      </span>
                    )}
                    {isMeta && m.text.trim() !== "" && (
                      <>
                        <span className="shrink-0 text-accent">
                          {openMeta.has(m.seq) ? "▾" : "▸"}
                        </span>
                        {!openMeta.has(m.seq) && (
                          <span className="min-w-0 truncate text-ink-faint italic">
                            {m.text.slice(0, 120)}
                          </span>
                        )}
                      </>
                    )}
                    {m.model && (
                      <span className="ml-auto shrink-0 truncate">
                        {m.model}
                      </span>
                    )}
                  </div>
                  {isMeta && openMeta.has(m.seq) && (
                    <pre className="mt-1.5 max-h-96 overflow-auto rounded-md border border-edge bg-surface px-3 py-2 font-mono text-meta leading-relaxed whitespace-pre-wrap">
                      {m.text}
                    </pre>
                  )}
                  {!isMeta &&
                    m.text.trim() !== "" &&
                    (m.html ? (
                      <div
                        className="prose-msg measure"
                        dangerouslySetInnerHTML={{ __html: m.html }}
                      />
                    ) : (
                      <div className="measure text-sm leading-relaxed whitespace-pre-wrap">
                        {m.text}
                      </div>
                    ))}
                  {msgTools.length > 0 && (
                    <MessageTools
                      agent={agent}
                      sessionId={sessionId}
                      tools={msgTools}
                      open={openTools}
                      onToggle={toggleTool}
                      className={m.text.trim() !== "" ? "mt-2" : ""}
                    />
                  )}
                </div>
              </li>
            );
          })}
        </ol>
        <Scrubber
          msgs={visible}
          toolsByMsg={toolsByMsg}
          rangeStart={rangeStart}
          rangeEnd={rangeEnd}
          onJump={(i) => {
            quietSpy();
            virtualizer.scrollToIndex(i, { align: "center" });
          }}
        />
      </div>
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
      {/* A failed transcript load is not an empty session. */}
      {error != null && <LoadError error={error} />}
      {!loading && !error && msgs.length === 0 && (
        <EmptyNote>No transcript entries.</EmptyNote>
      )}
    </div>
  );
}
// MessageTools renders a message's tool calls as kind-colored chips;
// expandable chips fetch their full payload (diff excerpts, complete
// command) only when opened — chip rows never carry them.
//
// Expansion state is owned by the transcript and passed in: this component
// mounts inside a virtualized row, so anything it held itself would be
// discarded the moment the row scrolled out of the window.
function MessageTools({
  agent,
  sessionId,
  tools,
  open,
  onToggle,
  className = "",
}: {
  agent: string;
  sessionId: string;
  tools: ToolCallRow[];
  open: ReadonlySet<number>;
  onToggle: (seq: number) => void;
  className?: string;
}) {
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
              onClick={expandable ? () => onToggle(t.seq) : undefined}
              aria-expanded={expandable ? isOpen : undefined}
              title={t.detail}
              className={`inline-flex max-w-full items-baseline gap-1.5 rounded border px-1.5 py-0.5 font-mono text-meta ${
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
            <div className="mb-1 font-mono text-micro text-ink-faint">
              <span style={{ color: toolColor(t.kind) }}>{t.name}</span>
              {t.detail && t.kind !== "shell" && <> · {shortPath(t.detail)}</>}
            </div>
            <ToolExpansion agent={agent} sessionId={sessionId} seq={t.seq} />
          </div>
        ))}
    </div>
  );
}
// Scrubber is the session minimap: one tick per bucket of messages,
// colored by who is speaking and whether the turn ran tools, with the
// on-screen slice marked. A long session is a document, and a document
// needs a sense of where you are in it and a way to reach the far end
// without dragging a scrollbar past a thousand cards.
//
// Ticks are bucketed rather than drawn one-per-message: a 10k-message
// session would otherwise put 10k nodes on screen to save the reader
// scrolling past 10k nodes.
function Scrubber({
  msgs,
  toolsByMsg,
  rangeStart,
  rangeEnd,
  onJump,
}: {
  msgs: TranscriptMessage[];
  toolsByMsg: Map<number, ToolCallRow[]>;
  rangeStart?: number;
  rangeEnd?: number;
  onJump: (index: number) => void;
}) {
  const buckets = useMemo(() => {
    if (msgs.length === 0) return [];
    const n = Math.min(SCRUB_BUCKETS, msgs.length);
    const size = msgs.length / n;
    return Array.from({ length: n }, (_, b) => {
      const from = Math.floor(b * size);
      const to = Math.max(from + 1, Math.floor((b + 1) * size));
      let user = 0;
      let tools = 0;
      for (let i = from; i < to && i < msgs.length; i++) {
        const m = msgs[i];
        if (m.role === "user" && m.kind === "message") user++;
        if ((toolsByMsg.get(m.seq)?.length ?? 0) > 0) tools++;
      }
      return { from, to, user, tools };
    });
  }, [msgs, toolsByMsg]);

  // Below a couple of screens' worth of messages there is nothing to
  // navigate and the rail would be decoration.
  if (buckets.length < 24) return null;

  return (
    <div className="hidden w-2.5 shrink-0 xl:block" aria-hidden>
      <div className="sticky top-6 flex h-[70vh] flex-col overflow-hidden rounded border border-edge bg-surface-1">
        {buckets.map((b, i) => {
          const inView =
            rangeStart !== undefined &&
            rangeEnd !== undefined &&
            b.to > rangeStart &&
            b.from <= rangeEnd;
          const color = b.user
            ? "var(--color-accent)"
            : b.tools
              ? "var(--color-tool-shell)"
              : "var(--color-assistant)";
          return (
            <button
              key={i}
              type="button"
              tabIndex={-1}
              title={`messages ${b.from + 1}–${b.to}`}
              onClick={() => onJump(b.from)}
              style={{ background: color, opacity: inView ? 1 : 0.22 }}
              className="w-full flex-1 transition-opacity hover:opacity-90"
            />
          );
        })}
      </div>
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
