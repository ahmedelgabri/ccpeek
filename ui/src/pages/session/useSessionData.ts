import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { api, type ToolCallRow, type TranscriptMessage } from "../../api";
import { usePagedList } from "../../paged";

// Tool-call page size: bounds each /tools response; pages auto-fetch
// until the session's full set is loaded.
const TOOLS_PAGE = 500;

// The transcript pages forward through the API's from/limit window (the
// server caps a single response at 1000 rows). A session can run to many
// thousands of messages, so we fetch a page at a time and append rather
// than truncating at the first thousand.
const TRANSCRIPT_PAGE = 1000;

// How far before a deep-linked message the window opens, so the target
// isn't flush against the top and there is context to scroll up into.
const ANCHOR_LEAD = 100;

export interface TranscriptWindow {
  msgs: TranscriptMessage[];
  focusSeq: number | undefined;
  /** Compact tool chips covering exactly the loaded transcript range. */
  chipRows: ToolCallRow[];
  loading: boolean;
  hasMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
  hasOlder: boolean;
  loadingOlder: boolean;
  loadOlder: () => void;
  /** Scroll-spy: point the URL at a message already on screen. */
  trackSeq: (seq: number) => void;
  /** Permalink: same, plus a copy to the clipboard. */
  copyPermalink: (seq: number) => void;
  /** Deliberate navigation from another tab; re-anchors when far away. */
  jumpToSeq: (seq: number) => void;
}

// useTranscriptWindow owns the paged, anchored transcript and the URL's
// ?seq. It is the whole reason the page used to be so long: the window,
// the deep-link anchoring, and the scroll-spy write-back are one mechanism
// with several entry points, and separating them from the rendering makes
// both readable.
//
// The window is anchored at a target seq. Scroll-spy rewrites ?seq as the
// reader moves, so those self-writes (tracked in lastSeqInURL) are
// distinguished from deliberate navigation — a deep link, a permalink
// opened elsewhere, or a "jump to message" from another tab. Only
// deliberate navigation re-anchors the window and re-scrolls; scroll-spy
// never does, since its target is always already in view.
export function useTranscriptWindow(
  agent: string,
  sessionId: string,
  searchSeq: number | undefined,
): TranscriptWindow {
  const navigate = useNavigate({ from: "/sessions/$agent/$sessionId" });
  const [anchor, setAnchor] = useState(() =>
    searchSeq !== undefined ? Math.max(0, searchSeq - ANCHOR_LEAD) : 0,
  );
  const [focusSeq, setFocusSeq] = useState(searchSeq);
  const lastSeqInURL = useRef<number | undefined>(searchSeq);

  const transcript = useInfiniteQuery({
    // The anchor is in the query key so a far jump loads the page AROUND
    // the target, never everything up to it. Page parameters carry BOTH
    // from and limit so pages tile: a fixed backward limit used to
    // re-cover most of the anchored page (anchor 400 → previous request
    // 0..999 overlapping 400..999), producing duplicate seqs and duplicate
    // virtualizer keys.
    queryKey: ["transcript", agent, sessionId, anchor],
    queryFn: ({ pageParam }) =>
      api.transcript(agent, sessionId, {
        from: String(pageParam.from),
        limit: String(pageParam.limit),
      }),
    initialPageParam: { from: anchor, limit: TRANSCRIPT_PAGE },
    // A full page implies more ahead, continuing just past the last seq.
    getNextPageParam: (last, _all, lastParam) =>
      last && last.length === lastParam.limit
        ? { from: last[last.length - 1].seq + 1, limit: TRANSCRIPT_PAGE }
        : undefined,
    // Older messages: step back one page width, but never past what is
    // already loaded — the final backward page's limit is exactly the
    // uncovered gap, so it ends where the anchored page begins.
    getPreviousPageParam: (_first, _all, firstParam) => {
      if (firstParam.from <= 0) return undefined;
      const from = Math.max(0, firstParam.from - TRANSCRIPT_PAGE);
      return { from, limit: firstParam.from - from };
    },
  });

  // Defensive seq-dedupe on flatten: tiling makes overlap impossible by
  // construction, but duplicate seqs would corrupt the virtualizer's
  // keyed heights, so the invariant is enforced here too.
  const msgs = useMemo(() => {
    const seen = new Set<number>();
    const out: TranscriptMessage[] = [];
    for (const page of transcript.data?.pages ?? []) {
      for (const m of page ?? []) {
        if (!seen.has(m.seq)) {
          seen.add(m.seq);
          out.push(m);
        }
      }
    }
    return out;
  }, [transcript.data]);

  // The route reuses this component across sessions: reset the window when
  // the session id changes.
  useEffect(() => {
    setAnchor(
      searchSeq !== undefined ? Math.max(0, searchSeq - ANCHOR_LEAD) : 0,
    );
    setFocusSeq(searchSeq);
    lastSeqInURL.current = searchSeq;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  // Deliberate navigation to a seq (anything but our own scroll-spy write):
  // focus it, and re-anchor when the target sits outside the loaded range,
  // so a far jump loads the page around it instead of paging all the way
  // there.
  useEffect(() => {
    if (searchSeq === undefined || searchSeq === lastSeqInURL.current) return;
    lastSeqInURL.current = searchSeq;
    setFocusSeq(searchSeq);
    const first = msgs[0]?.seq;
    const last = msgs[msgs.length - 1]?.seq;
    if (first === undefined || searchSeq < first || searchSeq > last) {
      setAnchor(Math.max(0, searchSeq - ANCHOR_LEAD));
    }
  }, [searchSeq, msgs]);

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
  const trackSeq = useCallback(
    (seq: number) => putSeqInURL(seq),
    [putSeqInURL],
  );
  const copyPermalink = useCallback(
    (seq: number) => putSeqInURL(seq, true),
    [putSeqInURL],
  );
  const jumpToSeq = useCallback(
    (seq: number) => void navigate({ search: { seq }, resetScroll: false }),
    [navigate],
  );

  // Compact chips for exactly the loaded transcript range: tiny rows
  // (capped detail, no diff payloads), re-requested as the range grows.
  // The transcript never triggers the full tool list.
  const firstSeq = msgs[0]?.seq;
  const lastSeq = msgs[msgs.length - 1]?.seq;
  const chips = useQuery({
    queryKey: ["toolchips", agent, sessionId, firstSeq, lastSeq],
    queryFn: () =>
      api.sessionTools(agent, sessionId, {
        compact: true,
        fromSeq: firstSeq,
        toSeq: lastSeq,
      }),
    enabled: firstSeq !== undefined,
    placeholderData: (prev) => prev,
  });

  return {
    msgs,
    focusSeq,
    chipRows: chips.data ?? [],
    loading: transcript.isLoading,
    hasMore: transcript.hasNextPage,
    loadingMore: transcript.isFetchingNextPage,
    loadMore: () => void transcript.fetchNextPage(),
    hasOlder: transcript.hasPreviousPage,
    loadingOlder: transcript.isFetchingPreviousPage,
    loadOlder: () => void transcript.fetchPreviousPage(),
    trackSeq,
    copyPermalink,
    jumpToSeq,
  };
}

// useSessionTools loads the FULL tool rows, and only once a tab that needs
// them has been opened — never on transcript mount, which is served by the
// compact chips above. Rows carry no diff payloads, so excerpts still load
// per expansion.
export function useSessionTools(
  agent: string,
  sessionId: string,
  wanted: boolean,
): { rows: ToolCallRow[]; loading: boolean; requested: boolean } {
  const [requested, setRequested] = useState(wanted);
  useEffect(() => {
    if (wanted) setRequested(true);
  }, [wanted]);

  const tools = usePagedList(
    ["tools", agent, sessionId],
    (offset) =>
      api.sessionTools(agent, sessionId, { limit: TOOLS_PAGE, offset }),
    TOOLS_PAGE,
    { enabled: requested },
  );

  // Unlike the browse pages, this one runs itself to completion: the tabs
  // show counts and group by file, both of which need every row.
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = tools;
  useEffect(() => {
    if (requested && hasNextPage && !isFetchingNextPage) fetchNextPage();
  }, [requested, hasNextPage, isFetchingNextPage, fetchNextPage]);

  return {
    rows: tools.rows,
    loading: requested && (tools.isLoading || hasNextPage),
    // Whether the rows exist YET is a fact the tab bar needs: a count of
    // zero before the fetch has been asked for is not a count, it is a
    // wrong answer.
    requested,
  };
}
