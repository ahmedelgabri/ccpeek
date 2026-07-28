import { useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

// usePagedList is the offset-paged list every browse page runs on:
// sessions, commands, artifacts, and a session's tool calls.
//
// "A full page means there is another one" is the contract with the
// server's limit clamp, and it was asserted separately on each page with
// its own page-size constant. Flattening the pages was duplicated the same
// number of times. One place now, so a change to either reaches all of
// them.
export function usePagedList<T>(
  queryKey: readonly unknown[],
  fetchPage: (offset: number) => Promise<T[] | null>,
  pageSize: number,
  options: { enabled?: boolean } = {},
) {
  const query = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => fetchPage(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, _all, lastParam) =>
      last && last.length === pageSize ? lastParam + pageSize : undefined,
    placeholderData: (prev) => prev,
    enabled: options.enabled,
  });
  const rows = useMemo(
    () => (query.data?.pages ?? []).flatMap((p) => p ?? []),
    [query.data],
  );
  return {
    rows,
    isLoading: query.isLoading,
    error: query.error,
    hasNextPage: query.hasNextPage,
    isFetchingNextPage: query.isFetchingNextPage,
    fetchNextPage: () => void query.fetchNextPage(),
  };
}

// LoadMore is the button that goes with it — nothing renders when there is
// no next page, so callers can drop it in unconditionally.
export function LoadMore({
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
}: {
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onLoadMore: () => void;
}) {
  if (!hasNextPage) return null;
  return (
    <button
      onClick={onLoadMore}
      disabled={isFetchingNextPage}
      className="mt-4 w-full rounded-md border border-edge bg-surface-1 py-2 font-mono text-xs text-ink-dim hover:text-ink disabled:opacity-50"
    >
      {isFetchingNextPage ? "loading…" : "load more"}
    </button>
  );
}
