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

export function useHighlight(
  ref: RefObject<HTMLElement | null>,
  deps: unknown[],
) {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let cancelled = false;
    void loadHljs().then(({ default: hljs }) => {
      if (cancelled || !ref.current) return;
      ref.current
        .querySelectorAll<HTMLElement>("pre code:not(.hljs)")
        .forEach((block) => {
          // Cap pathological blocks; hljs on megabyte payloads janks the tab.
          if ((block.textContent?.length ?? 0) > 100_000) return;
          hljs.highlightElement(block);
        });
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
}
