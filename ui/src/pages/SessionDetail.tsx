import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useHighlight } from "../highlight";
import { DiffView } from "../Diff";
import {
  api,
  fmtCost,
  fmtTokens,
  shortPath,
  type ToolCallRow,
  type TranscriptMessage,
} from "../api";
import {
  AgentChip,
  CopyButton,
  EmptyNote,
  KindBars,
  SkeletonRows,
  SkeletonTiles,
  StatTile,
  TokenMixBar,
  toolColor,
} from "../ui";

const TABS = ["transcript", "commands", "tools", "files", "artifacts"] as const;

// The transcript pages forward through the API's from/limit window (the
// server caps a single response at 1000 rows). A session can run to many
// thousands of messages, so we fetch a page at a time and append rather
// than truncating at the first thousand.
const TRANSCRIPT_PAGE = 1000;

// The gathering point of the session-centric model: one session with its
// transcript, commands, tool calls, files touched, usage, relations, and
// linked artifacts — each facet a deep-linkable tab (?tab=).
export function SessionDetailPage() {
  const { agent, sessionId } = useParams({
    from: "/sessions/$agent/$sessionId",
  });
  const search = useSearch({ from: "/sessions/$agent/$sessionId" });
  const navigate = useNavigate({ from: "/sessions/$agent/$sessionId" });
  const tab =
    search.tab && (TABS as readonly string[]).includes(search.tab)
      ? search.tab
      : "transcript";

  const detail = useQuery({
    queryKey: ["session", agent, sessionId],
    queryFn: () => api.session(agent, sessionId),
  });
  // The transcript window is anchored at a target seq. Scroll-spy rewrites
  // ?seq as the reader moves, so those self-writes (tracked in lastSeqInURL)
  // are distinguished from deliberate navigation — a deep link, a permalink
  // opened elsewhere, or a "jump to message" from the commands/tools/files
  // tabs. Only deliberate navigation re-anchors the window and re-scrolls;
  // scroll-spy never does (its target is always already in view).
  const [anchor, setAnchor] = useState(() =>
    search.seq !== undefined ? Math.max(0, search.seq - 100) : 0,
  );
  const [focusSeq, setFocusSeq] = useState(search.seq);
  const lastSeqInURL = useRef<number | undefined>(search.seq);

  const transcript = useInfiniteQuery({
    // Anchor a little before the target so it isn't flush against the top
    // and there is context to scroll up into; the anchor is in the query
    // key so a far jump loads the page AROUND the target, never everything
    // up to it.
    queryKey: ["transcript", agent, sessionId, anchor],
    queryFn: ({ pageParam }) =>
      api.transcript(agent, sessionId, {
        from: String(pageParam),
        limit: String(TRANSCRIPT_PAGE),
      }),
    initialPageParam: anchor,
    // A full page implies more ahead, continuing just past the last seq.
    getNextPageParam: (last) =>
      last && last.length === TRANSCRIPT_PAGE
        ? last[last.length - 1].seq + 1
        : undefined,
    // Older messages sit one page-width before the current first page's
    // start; nothing precedes seq 0.
    getPreviousPageParam: (_first, _all, firstParam) =>
      firstParam > 0 ? Math.max(0, firstParam - TRANSCRIPT_PAGE) : undefined,
  });
  const msgs = useMemo(
    () => (transcript.data?.pages ?? []).flatMap((p) => p ?? []),
    [transcript.data],
  );

  // The route reuses this component across sessions: reset the window when
  // the session id changes.
  useEffect(() => {
    setAnchor(search.seq !== undefined ? Math.max(0, search.seq - 100) : 0);
    setFocusSeq(search.seq);
    lastSeqInURL.current = search.seq;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  // Deliberate navigation to a seq (anything but our own scroll-spy write):
  // focus it, and re-anchor when the target sits outside the loaded range,
  // so a far jump loads the page around it instead of paging all the way
  // there.
  useEffect(() => {
    if (search.seq === undefined || search.seq === lastSeqInURL.current) return;
    lastSeqInURL.current = search.seq;
    setFocusSeq(search.seq);
    const first = msgs[0]?.seq;
    const last = msgs[msgs.length - 1]?.seq;
    if (first === undefined || search.seq < first || search.seq > last) {
      setAnchor(Math.max(0, search.seq - 100));
    }
  }, [search.seq, msgs]);

  // Scroll-spy and permalinks write ?seq without re-anchoring (their target
  // is on screen); a jump from another tab writes ?seq via a plain navigate
  // so the deliberate-navigation effect above handles it.
  const putSeqInURL = useCallback(
    (seq: number, copy = false) => {
      if (!copy && lastSeqInURL.current === seq) return;
      lastSeqInURL.current = seq;
      void navigate({
        search: (prev: { tab?: string; seq?: number }) => ({ ...prev, seq }),
        replace: true,
        resetScroll: false,
      });
      if (copy) {
        const link = `${window.location.origin}${window.location.pathname}?seq=${seq}`;
        void navigator.clipboard?.writeText(link);
      }
    },
    [navigate],
  );
  const jumpToMessage = useCallback(
    (seq: number) =>
      void navigate({ search: { seq }, resetScroll: false }),
    [navigate],
  );

  const tools = useQuery({
    queryKey: ["tools", agent, sessionId],
    queryFn: () => api.sessionTools(agent, sessionId),
  });

  if (detail.isLoading)
    return (
      <div className="space-y-4">
        <SkeletonTiles />
        <SkeletonRows rows={6} />
      </div>
    );
  if (detail.error) return <p className="text-warn">{String(detail.error)}</p>;
  const s = detail.data!;
  const toolRows = tools.data ?? [];
  const commands = toolRows.filter((t) => t.kind === "shell" && t.detail);
  const files = groupFiles(toolRows);

  return (
    <div>
      <div className="mb-1 flex items-baseline gap-3">
        <AgentChip agent={s.agent} />
        <h1 className="truncate text-xl font-semibold">
          {s.title || "(untitled)"}
        </h1>
      </div>
      <p className="mb-4 font-mono text-xs text-ink-faint">
        {s.cwd && (
          <Link
            to="/sessions"
            search={{ project: s.cwd }}
            className="hover:text-accent"
          >
            {shortPath(s.cwd)}
          </Link>
        )}
        {s.gitBranch && <> · ⎇ {s.gitBranch}</>} · {s.id}
      </p>

      <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4 xl:grid-cols-6">
        <StatTile
          label="Cost"
          value={fmtCost(s.costUSD, s.unpricedTokens)}
          tone="ok"
        />
        <StatTile label="Input" value={fmtTokens(s.tokens.input)} />
        <StatTile label="Output" value={fmtTokens(s.tokens.output)} />
        <StatTile label="Cache read" value={fmtTokens(s.tokens.cacheRead)} />
        <StatTile label="Messages" value={String(s.messages)} />
        <StatTile label="Tool calls" value={String(s.toolCalls)} />
      </div>

      {s.tokens.input +
        s.tokens.output +
        s.tokens.cacheRead +
        s.tokens.cacheWrite >
        0 && (
        <div className="mb-4 rounded-md border border-edge bg-surface-1 px-3 py-2.5">
          <div className="microlabel mb-2">Token mix</div>
          <TokenMixBar tokens={s.tokens} fmt={fmtTokens} />
        </div>
      )}

      {s.unpricedTokens ? (
        <p className="mb-4 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
          {fmtTokens(s.unpricedTokens)} tokens use a model the pricing table
          can't resolve — the cost shown is a lower bound.
        </p>
      ) : null}

      {(s.relations?.length || s.models?.length) && (
        <div className="mb-4 flex flex-wrap gap-2 text-xs">
          {s.models?.map((m) => (
            <span
              key={m}
              className="rounded border border-edge px-2 py-0.5 font-mono text-ink-dim"
            >
              {m}
            </span>
          ))}
          {s.relations?.map((r) => (
            <Link
              key={`${r.kind}-${r.sessionId}`}
              to="/sessions/$agent/$sessionId"
              params={{ agent: s.agent, sessionId: r.sessionId }}
              className="rounded border border-accent/40 px-2 py-0.5 font-mono text-accent hover:bg-surface-2"
            >
              {r.direction === "out" ? r.kind : `${r.kind} (incoming)`} →{" "}
              {r.sessionId.slice(0, 8)}
            </Link>
          ))}
        </div>
      )}

      <div className="mb-3 flex rounded-md border border-edge font-mono text-xs">
        {TABS.map((t) => {
          const count =
            t === "commands"
              ? commands.length
              : t === "tools"
                ? toolRows.length
                : t === "files"
                  ? files.length
                  : t === "artifacts"
                    ? (s.artifacts?.length ?? 0)
                    : null;
          return (
            <button
              key={t}
              onClick={() =>
                void navigate({
                  search: t === "transcript" ? {} : { tab: t },
                  replace: true,
                })
              }
              className={`px-3 py-1.5 first:rounded-l-md last:rounded-r-md ${
                t === tab
                  ? "bg-surface-2 text-ink"
                  : "text-ink-dim hover:text-ink"
              }`}
            >
              {t}
              {count !== null && count > 0 && (
                <span className="ml-1.5 text-ink-faint tabular-nums">
                  {count}
                </span>
              )}
            </button>
          );
        })}
      </div>

      {tab === "transcript" && (
        <Transcript
          key={sessionId}
          msgs={msgs}
          tools={toolRows}
          focusSeq={focusSeq}
          total={s.messages}
          loading={transcript.isLoading}
          hasMore={transcript.hasNextPage}
          loadingMore={transcript.isFetchingNextPage}
          onLoadMore={() => void transcript.fetchNextPage()}
          hasOlder={transcript.hasPreviousPage}
          loadingOlder={transcript.isFetchingPreviousPage}
          onLoadOlder={() => void transcript.fetchPreviousPage()}
          onScrollSeq={(seq) => putSeqInURL(seq)}
          onPermalink={(seq) => putSeqInURL(seq, true)}
        />
      )}
      {tab === "commands" && (
        <CommandsTab commands={commands} onJump={jumpToMessage} />
      )}
      {tab === "tools" && (
        <ToolsTab tools={toolRows} onJump={jumpToMessage} />
      )}
      {tab === "files" && <FilesTab files={files} onJump={jumpToMessage} />}
      {tab === "artifacts" && (
        <ArtifactsTab
          agent={s.agent}
          artifacts={s.artifacts ?? []}
        />
      )}
    </div>
  );
}

function Transcript({
  msgs,
  tools,
  focusSeq,
  total,
  loading,
  hasMore,
  loadingMore,
  onLoadMore,
  hasOlder,
  loadingOlder,
  onLoadOlder,
  onScrollSeq,
  onPermalink,
}: {
  msgs: TranscriptMessage[];
  tools: ToolCallRow[];
  focusSeq?: number;
  total: number;
  loading: boolean;
  hasMore: boolean;
  loadingMore: boolean;
  onLoadMore: () => void;
  hasOlder: boolean;
  loadingOlder: boolean;
  onLoadOlder: () => void;
  onScrollSeq: (seq: number) => void;
  onPermalink: (seq: number) => void;
}) {
  const [treeView, setTreeView] = useState(false);
  const [copiedSeq, setCopiedSeq] = useState<number | null>(null);
  // Meta entries (toolResult, system, …) render as one-line excerpts;
  // clicking reveals the full stored text.
  const [openMeta, setOpenMeta] = useState<ReadonlySet<number>>(new Set());
  const toggleMeta = (seq: number) =>
    setOpenMeta((prev) => {
      const next = new Set(prev);
      if (next.has(seq)) next.delete(seq);
      else next.add(seq);
      return next;
    });
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
  const visible = msgs.filter(
    (m) =>
      m.text.trim() !== "" ||
      (toolsByMsg.get(m.seq)?.length ?? 0) > 0 ||
      m.seq === focusSeq,
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
  const focused = useRef(false);
  const [focusDone, setFocusDone] = useState(focusSeq === undefined);
  useEffect(() => {
    focused.current = false;
    setFocusDone(focusSeq === undefined);
  }, [focusSeq]);
  useEffect(() => {
    if (focusSeq === undefined || focused.current) return;
    const idx = visible.findIndex((m) => m.seq === focusSeq);
    if (idx < 0) return;
    virtualizer.scrollToIndex(idx, { align: "center" });
    focused.current = true;
    setFocusDone(true);
  }, [focusSeq, visible, virtualizer]);

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
    if (!hasOlder || loadingOlder || !focusDone || rangeStart === undefined) return;
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
                      copiedSeq === m.seq
                        ? "text-ok"
                        : "hover:text-accent"
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
// every chip with a payload expands inline — file edits into their line
// diff, everything else into the full command/path.
function MessageTools({
  tools,
  className = "",
}: {
  tools: ToolCallRow[];
  className?: string;
}) {
  const [open, setOpen] = useState<ReadonlySet<number>>(new Set());
  const toggle = (seq: number) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(seq)) next.delete(seq);
      else next.add(seq);
      return next;
    });

  return (
    <div className={className}>
      <div className="flex flex-wrap gap-1.5">
        {tools.map((t) => {
          // Expand only when there is more than the chip already shows: a
          // diff (edits and writes both carry payloads), a full shell
          // command, or a truncated detail.
          const expandable =
            ((t.kind === "file_edit" || t.kind === "file_write") &&
              Boolean(t.old || t.new)) ||
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
            {(t.kind === "file_edit" || t.kind === "file_write") &&
            (t.old || t.new) ? (
              <DiffView old={t.old ?? ""} new={t.new ?? ""} />
            ) : (
              <pre className="max-h-64 overflow-auto rounded-md border border-edge bg-surface px-3 py-2 text-[11px] leading-relaxed">
                <code
                  className={
                    t.kind === "shell"
                      ? "language-bash block whitespace-pre-wrap"
                      : "block whitespace-pre-wrap"
                  }
                >
                  {t.detail}
                </code>
              </pre>
            )}
          </div>
        ))}
    </div>
  );
}

// JumpButton links a tool call / file change back to the message that
// issued it: it navigates the transcript to that seq, where the shared
// anchoring loads the window around it and scrolls it into view.
function JumpButton({
  seq,
  onJump,
}: {
  seq: number;
  onJump: (seq: number) => void;
}) {
  return (
    <button
      onClick={(e) => {
        e.stopPropagation();
        onJump(seq);
      }}
      title={`Jump to message #${seq} in the transcript`}
      className="shrink-0 font-mono text-[10px] text-ink-faint tabular-nums hover:text-accent"
    >
      ↗ #{seq}
    </button>
  );
}

function CommandsTab({
  commands,
  onJump,
}: {
  commands: ToolCallRow[];
  onJump: (seq: number) => void;
}) {
  const container = useRef<HTMLDivElement>(null);
  useHighlight(container, [commands]);
  if (commands.length === 0)
    return <EmptyNote>No shell commands in this session.</EmptyNote>;
  return (
    <div ref={container}>
      <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
        {commands.map((c) => (
          <li
            key={c.seq}
            className="flex items-start gap-3 bg-surface-1 px-3 py-2"
          >
            <pre className="min-w-0 flex-1 text-xs leading-relaxed">
              <code className="language-bash block break-words whitespace-pre-wrap">
                {c.detail}
              </code>
            </pre>
            <JumpButton seq={c.messageSeq} onJump={onJump} />
            <span className="shrink-0 font-mono text-[10px] text-ink-faint tabular-nums">
              {c.at?.slice(11, 19)}
            </span>
            <CopyButton text={c.detail ?? ""} />
          </li>
        ))}
      </ul>
    </div>
  );
}

function ToolsTab({
  tools,
  onJump,
}: {
  tools: ToolCallRow[];
  onJump: (seq: number) => void;
}) {
  if (tools.length === 0) return <EmptyNote>No tool calls recorded.</EmptyNote>;
  const byKind = new Map<string, number>();
  for (const t of tools) byKind.set(t.kind, (byKind.get(t.kind) ?? 0) + 1);
  const kinds = Array.from(byKind, ([label, count]) => ({ label, count })).toSorted(
    (a, b) => b.count - a.count,
  );
  return (
    <div className="space-y-3">
      <div className="rounded-md border border-edge bg-surface-1">
        <div className="microlabel border-b border-edge px-3 py-2">
          Calls by kind
        </div>
        <KindBars items={kinds} />
      </div>
      <div className="overflow-hidden rounded-md border border-edge">
        <table className="w-full text-sm">
          <thead className="bg-surface-2 text-left font-mono text-[10px] tracking-wider text-ink-faint uppercase">
            <tr>
              <th className="px-3 py-1.5">#</th>
              <th className="px-3 py-1.5">tool</th>
              <th className="px-3 py-1.5">kind</th>
              <th className="px-3 py-1.5">detail</th>
              <th className="px-3 py-1.5 text-right">msg</th>
              <th className="px-3 py-1.5 text-right">at</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge bg-surface-1">
            {tools.map((t) => (
              <ToolRow key={t.seq} t={t} onJump={onJump} />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ToolRow({
  t,
  onJump,
}: {
  t: ToolCallRow;
  onJump: (seq: number) => void;
}) {
  const [open, setOpen] = useState(false);
  const hasDiff =
    (t.kind === "file_edit" || t.kind === "file_write") && (t.old || t.new);
  return (
    <>
      <tr
        onClick={hasDiff ? () => setOpen((v) => !v) : undefined}
        className={hasDiff ? "cursor-pointer hover:bg-surface-2/40" : undefined}
      >
        <td className="px-3 py-1.5 font-mono text-xs text-ink-faint tabular-nums">
          {t.seq}
        </td>
        <td className="px-3 py-1.5 font-mono text-xs">{t.name}</td>
        <td className="px-3 py-1.5">
          <span className="inline-flex items-center gap-1.5 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-dim">
            <span
              aria-hidden
              className="inline-block h-1.5 w-1.5 rounded-full"
              style={{ background: toolColor(t.kind) }}
            />
            {t.kind}
          </span>
        </td>
        <td className="max-w-0 truncate px-3 py-1.5 font-mono text-xs text-ink-dim">
          {hasDiff && (
            <span className="mr-1.5 text-accent">{open ? "▾" : "▸"} diff</span>
          )}
          {t.detail && shortPath(t.detail)}
        </td>
        <td className="px-3 py-1.5 text-right">
          <JumpButton seq={t.messageSeq} onJump={onJump} />
        </td>
        <td className="px-3 py-1.5 text-right font-mono text-[11px] text-ink-faint tabular-nums">
          {t.at?.slice(11, 19)}
        </td>
      </tr>
      {hasDiff && open && (
        <tr>
          <td colSpan={6} className="px-3 py-2">
            <DiffView old={t.old ?? ""} new={t.new ?? ""} />
          </td>
        </tr>
      )}
    </>
  );
}

interface FileGroup {
  path: string;
  reads: number;
  writes: number;
  edits: number;
  // changes holds the edit/write rows with payloads, for diff expansion.
  changes: ToolCallRow[];
}

function groupFiles(tools: ToolCallRow[]): FileGroup[] {
  const map = new Map<string, FileGroup>();
  for (const t of tools) {
    if (!t.detail || !t.kind.startsWith("file_")) continue;
    const g = map.get(t.detail) ?? {
      path: t.detail,
      reads: 0,
      writes: 0,
      edits: 0,
      changes: [],
    };
    if (t.kind === "file_read") g.reads++;
    if (t.kind === "file_write") g.writes++;
    if (t.kind === "file_edit") g.edits++;
    if (
      (t.kind === "file_edit" || t.kind === "file_write") &&
      (t.old || t.new)
    ) {
      g.changes.push(t);
    }
    map.set(t.detail, g);
  }
  return Array.from(map.values()).toSorted(
    (a, b) => b.writes + b.edits - (a.writes + a.edits),
  );
}

function FilesTab({
  files,
  onJump,
}: {
  files: FileGroup[];
  onJump: (seq: number) => void;
}) {
  if (files.length === 0)
    return <EmptyNote>No files touched in this session.</EmptyNote>;
  return (
    <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
      {files.map((f) => (
        <FileRow key={f.path} f={f} onJump={onJump} />
      ))}
    </ul>
  );
}

function FileRow({
  f,
  onJump,
}: {
  f: FileGroup;
  onJump: (seq: number) => void;
}) {
  const [open, setOpen] = useState(false);
  const diffs = f.changes;
  return (
    <li className="bg-surface-1">
      <div
        onClick={diffs.length > 0 ? () => setOpen((v) => !v) : undefined}
        className={`flex items-baseline gap-3 px-3 py-1.5 ${
          diffs.length > 0 ? "cursor-pointer hover:bg-surface-2/40" : ""
        }`}
      >
        {diffs.length > 0 && (
          <span className="shrink-0 font-mono text-[11px] text-accent">
            {open ? "▾" : "▸"}
          </span>
        )}
        <span className="truncate font-mono text-xs">{shortPath(f.path)}</span>
        <span className="ml-auto flex shrink-0 gap-2 font-mono text-[10px] text-ink-faint tabular-nums">
          {f.edits > 0 && <span className="text-warn">{f.edits} edits</span>}
          {f.writes > 0 && <span className="text-ok">{f.writes} writes</span>}
          {f.reads > 0 && <span>{f.reads} reads</span>}
        </span>
        <CopyButton text={f.path} />
      </div>
      {open && (
        <div className="space-y-2 border-t border-edge px-3 py-2">
          {diffs.map((e) => (
            <div key={e.seq}>
              <div className="mb-1 flex items-center gap-2 font-mono text-[10px] text-ink-faint tabular-nums">
                <span>
                  {e.kind === "file_write" ? "write" : "edit"} #{e.seq} ·{" "}
                  {e.at?.slice(11, 19)}
                </span>
                <JumpButton seq={e.messageSeq} onJump={onJump} />
              </div>
              <DiffView old={e.old ?? ""} new={e.new ?? ""} />
            </div>
          ))}
        </div>
      )}
    </li>
  );
}

function ArtifactsTab({
  agent,
  artifacts,
}: {
  agent: string;
  artifacts: { kind: string; name: string; relation: string; evidence: string }[];
}) {
  if (artifacts.length === 0)
    return <EmptyNote>No artifacts linked to this session.</EmptyNote>;
  return (
    <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
      {artifacts.map((a) => (
        <li key={`${a.kind}-${a.name}`}>
          <Link
            to="/artifacts/$agent/$kind/$name"
            params={{ agent, kind: a.kind, name: a.name }}
            className="flex items-baseline gap-3 bg-surface-1 px-3 py-2 transition-colors hover:bg-surface-2/40"
          >
            <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-accent">
              {a.kind.replaceAll("_", " ")}
            </span>
            <span className="truncate font-mono text-xs">{a.name}</span>
            <span className="ml-auto shrink-0 font-mono text-[10px] text-ink-faint">
              {a.relation} · {a.evidence}
            </span>
          </Link>
        </li>
      ))}
    </ul>
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
