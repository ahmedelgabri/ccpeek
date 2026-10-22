import { useEffect, type RefObject } from "react";
import type { Lang, Theme } from "@pierre/highlights";

// Lazy syntax highlighting: @pierre/highlights (its lexers are one inlined
// wasm module) loads as its own chunk the first time a page with code
// mounts, then highlights every unprocessed `pre code` under the given
// container.
//
// The theme is a CSS palette: every syntax color names a suffix of the
// app's `--color-*` tokens, so a token is styled `var(--color-accent)` and
// resolves per element against the active color-scheme — one theme serves
// both modes, here and in the diff view (Diff.tsx registers it too).
// Token types without a rule fall back to the `foreground` suffix, which
// the defaults point at ink.
export const theme: Theme = {
  name: "ccpeek",
  appearance: "dark",
  cssVariables: {
    prefix: "--color-",
    defaults: { foreground: "var(--color-ink)" },
  },
  style: {
    foreground: "ink",
    syntax: {
      comment: { color: "ink-faint", font_style: "italic" },
      keyword: "assistant",
      tag: "assistant",
      "punctuation.bracket.html": "assistant",
      "punctuation.bracket.jsx": "assistant",
      string: "ok",
      "diff.plus": "ok",
      constant: "warn",
      number: "warn",
      boolean: "warn",
      preproc: "warn",
      "punctuation.list_marker": "warn",
      function: "accent",
      attribute: "accent",
      title: "accent",
      "property.json_key": "accent",
      type: "tool-search",
      namespace: "tool-search",
      "diff.minus": "agent-cursor",
      emphasis: { font_style: "italic" },
      "emphasis.strong": { font_weight: 700 },
    },
  },
};

type Highlights = typeof import("@pierre/highlights");
let highlightsPromise: Promise<Highlights> | null = null;

function loadHighlights() {
  highlightsPromise ??= import("@pierre/highlights");
  return highlightsPromise;
}

// Fence labels highlights doesn't know under the name agents write.
const ALIASES: Record<string, string> = { console: "shellsession" };

// The language comes from the block itself — goldmark stamps labeled
// fences with `language-XXX`, and components that know their payload is
// shell stamp `language-bash` — never from content sniffing. Anything
// highlights has no lexer for renders as plain text.
function blockLang(hl: Highlights, block: HTMLElement): Lang | null {
  const m = /(?:^|\s)language-([\w+.#-]+)/.exec(block.className);
  if (!m) return null;
  const name = m[1].toLowerCase();
  const id = ALIASES[name] ?? name;
  return hl.isSupportedLanguage(id) ? id : null;
}

const decoder = new TextDecoder();

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
    void loadHighlights().then((hl) => {
      // Re-read: the container may have changed under the lazy import.
      const el = ref.current;
      if (cancelled || !el) return;
      // data-hl marks a processed block, known language or not, so the
      // re-runs the windowed lists trigger skip everything already done.
      for (const block of el.querySelectorAll<HTMLElement>(
        "pre code:not([data-hl])",
      )) {
        // Cap pathological blocks; a megabyte payload's span soup janks
        // the tab however fast it tokenizes.
        if ((block.textContent?.length ?? 0) > 100_000) continue;
        const lang = blockLang(hl, block);
        if (lang) {
          const html = decoder.decode(
            hl.codeToHtml(block.textContent ?? "", { lang, theme }),
          );
          // Keep only the token spans inside highlights' own <pre><code>
          // wrapper, so the app's pre/code — and all the styling that
          // hangs off it — stays exactly as rendered.
          block.innerHTML = html.slice(
            html.indexOf("<code>") + "<code>".length,
            html.lastIndexOf("</code>"),
          );
        }
        block.dataset.hl = lang ?? "plain";
      }
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
}
