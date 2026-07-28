import { useEffect, type RefObject } from "react";
import type {
  HighlighterCore,
  LanguageRegistration,
  ThemeRegistrationAny,
} from "@shikijs/core";

// Lazy syntax highlighting: the shiki core + its JavaScript regex engine
// (no wasm) load as their own chunk the first time a page with code
// mounts, then highlight every unprocessed `pre code` under the given
// container. Grammars load on demand, one chunk per language, the first
// time that language appears.
//
// The theme maps token scopes straight onto the app palette. Shiki passes
// non-hex colors through verbatim, so every foreground is one of the
// light-dark() tokens from styles.css — the inline `var()` resolves per
// element against the active color-scheme, and one theme serves both
// modes without a dual-theme setup or any shiki-side CSS.
const theme: ThemeRegistrationAny = {
  name: "ccpeek",
  fg: "var(--color-ink)",
  bg: "transparent",
  settings: [
    { settings: { foreground: "var(--color-ink)" } },
    {
      scope: ["comment", "markup.quote"],
      settings: { foreground: "var(--color-ink-faint)", fontStyle: "italic" },
    },
    {
      scope: [
        "keyword",
        "storage",
        "entity.name.tag",
        "punctuation.definition.tag",
      ],
      settings: { foreground: "var(--color-assistant)" },
    },
    {
      scope: ["string", "string.regexp", "markup.inserted"],
      settings: { foreground: "var(--color-ok)" },
    },
    {
      scope: ["constant", "markup.list", "meta.preprocessor"],
      settings: { foreground: "var(--color-warn)" },
    },
    {
      scope: [
        "entity.name.function",
        "entity.other.attribute-name",
        "markup.heading",
        // JSON object keys: deeper than the bare `support` rule below, so
        // this wins and they keep the accent the hljs-attr rule gave them.
        "support.type.property-name",
      ],
      settings: { foreground: "var(--color-accent)" },
    },
    {
      scope: [
        "support",
        "entity.name.type",
        "entity.name.class",
        "entity.name.namespace",
      ],
      settings: { foreground: "var(--color-tool-search)" },
    },
    {
      scope: ["variable", "variable.parameter"],
      settings: { foreground: "var(--color-ink)" },
    },
    {
      scope: ["markup.deleted"],
      settings: { foreground: "var(--color-agent-cursor)" },
    },
    { scope: ["markup.italic"], settings: { fontStyle: "italic" } },
    { scope: ["markup.bold"], settings: { fontStyle: "bold" } },
  ],
};

let shikiPromise: Promise<HighlighterCore> | null = null;

function loadShiki() {
  shikiPromise ??= Promise.all([
    import("@shikijs/core"),
    import("@shikijs/engine-javascript"),
  ]).then(([core, js]) =>
    core.createHighlighterCore({
      themes: [theme],
      langs: [],
      engine: js.createJavaScriptRegexEngine(),
    }),
  );
  return shikiPromise;
}

// The grammars this app can highlight, keyed by canonical id. Anything
// else renders as plain text — deliberately no auto-detection. The set is
// what shows up in practice: shell commands (every agent), plus the fence
// labels markdown transcripts and artifacts actually carry.
type GrammarLoader = () => Promise<{ default: LanguageRegistration[] }>;
const GRAMMARS: Record<string, GrammarLoader> = {
  bash: () => import("@shikijs/langs/bash"),
  shellsession: () => import("@shikijs/langs/shellsession"),
  javascript: () => import("@shikijs/langs/javascript"),
  typescript: () => import("@shikijs/langs/typescript"),
  tsx: () => import("@shikijs/langs/tsx"),
  json: () => import("@shikijs/langs/json"),
  jsonc: () => import("@shikijs/langs/jsonc"),
  go: () => import("@shikijs/langs/go"),
  python: () => import("@shikijs/langs/python"),
  rust: () => import("@shikijs/langs/rust"),
  css: () => import("@shikijs/langs/css"),
  html: () => import("@shikijs/langs/html"),
  yaml: () => import("@shikijs/langs/yaml"),
  toml: () => import("@shikijs/langs/toml"),
  sql: () => import("@shikijs/langs/sql"),
  diff: () => import("@shikijs/langs/diff"),
  markdown: () => import("@shikijs/langs/markdown"),
  docker: () => import("@shikijs/langs/docker"),
  c: () => import("@shikijs/langs/c"),
  cpp: () => import("@shikijs/langs/cpp"),
  java: () => import("@shikijs/langs/java"),
  ruby: () => import("@shikijs/langs/ruby"),
  php: () => import("@shikijs/langs/php"),
  swift: () => import("@shikijs/langs/swift"),
  kotlin: () => import("@shikijs/langs/kotlin"),
  xml: () => import("@shikijs/langs/xml"),
  lua: () => import("@shikijs/langs/lua"),
  make: () => import("@shikijs/langs/make"),
  ini: () => import("@shikijs/langs/ini"),
  nix: () => import("@shikijs/langs/nix"),
};
const ALIASES: Record<string, string> = {
  sh: "bash",
  zsh: "bash",
  shell: "bash",
  shellscript: "bash",
  console: "shellsession",
  js: "javascript",
  jsx: "javascript",
  ts: "typescript",
  py: "python",
  rs: "rust",
  yml: "yaml",
  md: "markdown",
  dockerfile: "docker",
  "c++": "cpp",
  rb: "ruby",
  kt: "kotlin",
  makefile: "make",
};

// One in-flight load per grammar, shared by every concurrent pass.
const langLoads = new Map<string, Promise<void>>();

function ensureLang(shiki: HighlighterCore, id: string) {
  let p = langLoads.get(id);
  if (!p) {
    p = shiki.loadLanguage(GRAMMARS[id]());
    langLoads.set(id, p);
  }
  return p;
}

// The language comes from the block itself — goldmark stamps labeled
// fences with `language-XXX`, and components that know their payload is
// shell stamp `language-bash` — never from content sniffing.
function blockLang(block: HTMLElement): string | null {
  const m = /(?:^|\s)language-([\w+.-]+)/.exec(block.className);
  if (!m) return null;
  const name = m[1].toLowerCase();
  const id = ALIASES[name] ?? name;
  return id in GRAMMARS ? id : null;
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
    void loadShiki().then(async (shiki) => {
      // Re-read: the container may have changed under a lazy import.
      const el = ref.current;
      if (cancelled || !el) return;
      // data-hl marks a processed block, known language or not, so the
      // re-runs the windowed lists trigger skip everything already done.
      for (const block of el.querySelectorAll<HTMLElement>(
        "pre code:not([data-hl])",
      )) {
        // Cap pathological blocks; tokenizing megabyte payloads janks the tab.
        if ((block.textContent?.length ?? 0) > 100_000) continue;
        const lang = blockLang(block);
        if (lang) {
          await ensureLang(shiki, lang);
          if (cancelled) return;
          // Passes nest (a tool payload's own pass runs inside the
          // transcript's), and the grammar await above is where they can
          // interleave — re-check the mark so a block is processed once.
          if (block.dataset.hl) continue;
          // structure "inline" emits only the token spans (with <br> line
          // breaks), so the app's own pre/code — and all the styling that
          // hangs off it — stays exactly as rendered.
          block.innerHTML = shiki.codeToHtml(block.textContent ?? "", {
            lang,
            theme: "ccpeek",
            structure: "inline",
          });
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
