// Build-time stand-in for the full-bundle `shiki` package, wired up via
// the aliases in ui/vite.config.ts. @pierre/diffs imports `shiki`, whose
// real entry maps every bundled grammar (~220) and theme (~60) as dynamic
// imports — all of which the bundler would emit and go:embed would ship.
// This module exposes ONLY the names the library actually uses, backed by
// the same fine-grained @shikijs/* packages the app's own highlighter is
// built from, restricted to the curated grammar set.
//
// Coupled to @pierre/diffs internals — see the constraint comment at the
// alias site for what to re-verify on a version bump.
import { createHighlighterCore } from "@shikijs/core";
import { GRAMMARS } from "../highlight";

// Instance helpers the library calls on rendered tokens; plain re-exports
// of the same implementations the real `shiki` re-exports.
export {
  createCssVariablesTheme,
  getTokenStyleObject,
  stringifyTokenStyle,
} from "@shikijs/core";
export { createJavaScriptRegexEngine } from "@shikijs/engine-javascript";

// The library's language resolver looks loaders up by id in this map and
// treats anything absent as unknown. Reusing the app's curated map keeps
// diff highlighting in lockstep with every other code block; the diff
// renderer additionally pins each file's language explicitly (see
// langForPath), so unknown ids never reach this map at runtime.
export const bundledLanguages = GRAMMARS;

type CoreOptions = NonNullable<Parameters<typeof createHighlighterCore>[0]>;

// The library boots its highlighter with empty theme/language lists and
// attaches everything later via loadLanguageSync/loadThemeSync, so the
// bundle-flavored factory reduces to the core one. ("text" needs no
// registration — the core treats it as plain.)
export function createHighlighter(options: { engine: CoreOptions["engine"] }) {
  return createHighlighterCore({
    themes: [],
    langs: [],
    engine: options.engine,
  });
}

// Reachable only under `preferredHighlighter: "shiki-wasm"`, which this
// app never sets — the JS engine is forced. Loud on purpose.
export function createOnigurumaEngine(): never {
  throw new Error(
    "ccpeek shiki shim: the oniguruma/wasm engine is stubbed out; use the JavaScript engine",
  );
}

// Re-exported by the @pierre/diffs barrel but unused by this app; the
// real one drags in the full bundle. Loud on purpose.
export function codeToHtml(): never {
  throw new Error("ccpeek shiki shim: codeToHtml is stubbed out");
}
