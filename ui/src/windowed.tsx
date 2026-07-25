import { useLayoutEffect, useRef, type ReactNode } from "react";
import { useWindowVirtualizer } from "@tanstack/react-virtual";

// DOM windowing against the WINDOW scroller: only the on-screen slice of a
// long list is mounted, so a session with thousands of rows does not
// accumulate thousands of nodes (each of the tool rows carries its own
// expand state, and transcript rows carry markdown and highlighted code).
//
// The fiddly part, shared here, is the scroll margin: the list starts part
// way down the page, and the virtualizer has to know that offset or every
// row is positioned relative to the wrong origin. It is captured once
// after layout, before the first row is measured.
export function useRowWindow<T, E extends HTMLElement = HTMLElement>(
  items: T[],
  getKey: (item: T, index: number) => number | string,
  estimateSize: number,
) {
  const listRef = useRef<E>(null);
  const listOffset = useRef(0);
  useLayoutEffect(() => {
    listOffset.current = listRef.current?.offsetTop ?? 0;
  }, []);
  const virtualizer = useWindowVirtualizer({
    count: items.length,
    estimateSize: () => estimateSize,
    overscan: 10,
    scrollMargin: listOffset.current,
    getItemKey: (i) => getKey(items[i], i),
  });
  return { listRef, virtualizer, virtualItems: virtualizer.getVirtualItems() };
}

// WindowedList renders a windowed <ul>. Heights are MEASURED rather than
// assumed, because rows here expand in place — a tool row opens a diff
// below itself.
//
// Callers that need more than rendering (the transcript drives its
// infinite scroll off the visible range and scrolls deep links into view)
// use useRowWindow directly.
export function WindowedList<T>({
  items,
  getKey,
  estimateSize = 34,
  className = "",
  children,
}: {
  items: T[];
  getKey: (item: T, index: number) => number | string;
  estimateSize?: number;
  className?: string;
  children: (item: T, index: number) => ReactNode;
}) {
  const { listRef, virtualizer, virtualItems } = useRowWindow<
    T,
    HTMLUListElement
  >(items, getKey, estimateSize);
  return (
    <ul
      ref={listRef}
      className={`relative ${className}`}
      style={{ height: virtualizer.getTotalSize() }}
    >
      {virtualItems.map((vi) => (
        <li
          key={vi.key}
          data-index={vi.index}
          ref={virtualizer.measureElement}
          className="absolute top-0 left-0 w-full"
          style={{
            transform: `translateY(${vi.start - virtualizer.options.scrollMargin}px)`,
          }}
        >
          {children(items[vi.index], vi.index)}
        </li>
      ))}
    </ul>
  );
}
