import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { useHighlight } from "./highlight";

// DOM windowing against the WINDOW scroller: only the on-screen slice of a
// long list is mounted, so a session with thousands of rows does not
// accumulate thousands of nodes (transcript rows carry markdown and
// highlighted code).
//
// The fiddly part, shared here, is the scroll margin: the list starts part
// way down the page, and the virtualizer has to know that offset — it is
// what every row's position is measured from — or the whole list sits
// wrong by that distance.
//
// It is TRACKED, not captured once. The offset moves while the page is
// open: the indexing banner unmounts when the first pass finishes, the
// transcript grows a "Load older" button, a filter bar wraps to two rows on
// a narrow viewport. A one-shot reading left the arithmetic off by that
// height for the rest of the session, which reads as rows drifting out from
// under the cursor.
export function useRowWindow<T, E extends HTMLElement = HTMLElement>(
  items: T[],
  getKey: (item: T, index: number) => number | string,
  estimateSize: number,
) {
  const listRef = useRef<E>(null);
  const [scrollMargin, setScrollMargin] = useState(0);
  useLayoutEffect(() => {
    const el = listRef.current;
    if (!el) return undefined;
    // Document coordinates, not offsetTop: offsetTop is relative to the
    // nearest positioned ancestor, while scrollMargin is measured from the
    // top of the window scroller.
    const measure = () => {
      const top = el.getBoundingClientRect().top + window.scrollY;
      // Sub-pixel jitter must not loop back through the observer.
      setScrollMargin((prev) => (Math.abs(prev - top) > 0.5 ? top : prev));
    };
    // Coalesced to one measurement per frame: a burst of observations is
    // still a single forced layout read, not one per notification.
    let frame = 0;
    const schedule = () => {
      frame ||= requestAnimationFrame(() => {
        frame = 0;
        measure();
      });
    };
    measure();
    // The ANCESTORS are observed, from the parent up to (not including)
    // <body>: what moves a list is a height change ABOVE it, which surfaces
    // as a resize of some container the list sits inside. Observing the list
    // ITSELF fired on every change to the virtualizer's total size — which
    // is to say on every scroll frame while rows are still measuring, each
    // one a forced layout read that could re-render the list.
    const ro = new ResizeObserver(schedule);
    for (
      let node: HTMLElement | null = el.parentElement;
      node && node !== document.body;
      node = node.parentElement
    ) {
      ro.observe(node);
    }
    // The viewport itself resizes without resizing any of them.
    window.addEventListener("resize", schedule);
    return () => {
      if (frame) cancelAnimationFrame(frame);
      ro.disconnect();
      window.removeEventListener("resize", schedule);
    };
  }, []);
  const virtualizer = useWindowVirtualizer({
    count: items.length,
    estimateSize: () => estimateSize,
    overscan: 10,
    scrollMargin,
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
  highlight = false,
  children,
}: {
  items: T[];
  getKey: (item: T, index: number) => number | string;
  estimateSize?: number;
  className?: string;
  /** Syntax-highlight the rows as they mount. Rows arrive lazily, so the
   *  pass has to follow the window — a caller highlighting its own
   *  container against the full item list only ever reaches the first
   *  screenful, and everything below the fold stays plain. */
  highlight?: boolean;
  children: (item: T, index: number) => ReactNode;
}) {
  const { listRef, virtualizer, virtualItems } = useRowWindow<
    T,
    HTMLUListElement
  >(items, getKey, estimateSize);
  // Keyed on the window's EDGES, not the virtualItems array: that array is
  // a fresh identity every render, so the pass re-ran on each one. What
  // actually needs re-highlighting is a window that has moved.
  useHighlight(highlight ? listRef : null, [
    items,
    virtualItems[0]?.key,
    virtualItems[virtualItems.length - 1]?.key,
  ]);
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
