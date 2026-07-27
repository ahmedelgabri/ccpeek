import { useEffect, type RefObject } from "react";

// Lazy syntax highlighting: highlight.js core + common languages load as
// their own chunk the first time a page with code mounts, then highlight
// every un-highlighted `pre code` under the given container. Token colors
// live in styles.css (mapped to the app palette, not a stock theme).
let hljsPromise: Promise<typeof import("highlight.js/lib/common")> | null =
  null;

function loadHljs() {
  hljsPromise ??= import("highlight.js/lib/common");
  return hljsPromise;
}

/** A null ref turns the pass off — the caller that renders code only on
 *  request (WindowedList's `highlight`) says so by passing null, rather
 *  than keeping a second, permanently-empty ref alive to feed this. */
export function useHighlight(
  ref: RefObject<HTMLElement | null> | null,
  deps: unknown[],
) {
  useEffect(() => {
    if (!ref?.current) return undefined;
    let cancelled = false;
    void loadHljs().then(({ default: hljs }) => {
      // Re-read: the container may have changed under a lazy import.
      const el = ref.current;
      if (cancelled || !el) return;
      el.querySelectorAll<HTMLElement>("pre code:not(.hljs)").forEach(
        (block) => {
          // Cap pathological blocks; hljs on megabyte payloads janks the tab.
          if ((block.textContent?.length ?? 0) > 100_000) return;
          hljs.highlightElement(block);
        },
      );
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
}
