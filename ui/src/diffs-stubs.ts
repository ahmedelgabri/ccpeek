// Stand-ins for what only @pierre/diffs' Shiki backends and bundled theme
// catalogs import. The app always renders with the highlights backend and
// its own registered theme (Diff.tsx), so none of this ever runs — but
// without the stand-ins Vite emits every Shiki grammar and theme plus the
// oniguruma wasm into dist, and so into the binary (~9 MB). vite.config.ts
// aliases those imports here.

// shiki/langs: no bundled grammars to resolve.
export const bundledLanguages = {};

// @pierre/theming/themes and @pierre/highlights/themes/loader: no bundled
// themes, so an unknown name fails with diffs' own "no theme" error.
export const themes = { getTheme: () => undefined };

// shiki/wasm: the oniguruma engine's binary.
export default function onigurumaUnavailable(): never {
  throw new Error("shiki/wasm is not bundled; use the highlights backend");
}
