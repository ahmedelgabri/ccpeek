# Code highlighting and diffs with @pierre/highlights and @pierre/diffs

## Summary

The UI's syntax highlighter moved from Shiki (core + JS regex engine + 30 curated grammars) to `@pierre/highlights` 0.2.0, and the file-change view moved from a hand-written LCS diff to `@pierre/diffs` 1.6.1 with the `highlights` backend. Both use one theme, so code blocks and diffs share colors.

## What changed

- `ui/src/highlight.ts`: `useHighlight` keeps its API and DOM pass. The engine is now `@pierre/highlights`, loaded lazily as its own chunk. The wasm lexers are inlined as base64 in that chunk, so no `.wasm` file needs serving.
- The theme is a Zed-format "CSS palette" theme (`cssVariables: { prefix: "--color-" }`). Each syntax color names one of the app's `--color-*` tokens, so tokens resolve through `light-dark()` like before. The same object is exported and registered with diffs.
- Language support grew from 30 grammars to about 80 lexers (GraphQL, Terraform, Zig, Elixir, and more). `ini` has no lexer and now renders as plain text. `console` is aliased to `shellsession`.
- `ui/src/Diff.tsx`: edits render with diffs' `FileDiff` (unified, word-level change marks, syntax highlighting). New-file writes render with diffs' `File`. The app's own header bar (+N / −M, "new file · N lines") stays, and the diffs header is disabled. The 400-line "diff too large" limit and the 1200-line truncation are gone; payloads are already capped at 16 KB server-side.
- Edit excerpts get a trailing newline before parsing, so diffs does not print "No newline at end of file" under every snippet.
- `themeType` follows the app's theme toggle, because the diff's shadow root otherwise defaults to the OS color scheme.
- `ui/src/styles.css`: `diffs-container` maps diffs' CSS variables (background, foreground, add/delete colors, fonts) to the app tokens.
- `ui/src/pages/session/ToolExpansion.tsx`: the diff view is `React.lazy`, so `@pierre/diffs` loads only when a file change opens.
- `internal/webui/webui.go`: the SPA CSP adds `'wasm-unsafe-eval'` to `script-src`. Without it the browser refuses to compile the highlighter's WebAssembly and nothing renders. The keyword permits WebAssembly compilation only, not JS `eval`.
- `flake.nix`: refreshed `pnpmDeps` hash.
- `ui/vite.config.ts` + `ui/src/diffs-stubs.ts`: stub out diffs' unused Shiki backends (see below).
- `tests/e2e-go/browse-content.spec.ts`: the tools-tab test asserts rendered diff lines inside `diffs-container` instead of the removed "diff too large" text.

## Unused Shiki backends are stubbed out

`@pierre/diffs` imports all of Shiki for its other backends, so Vite emitted every Shiki grammar and theme, the oniguruma wasm, and two bundled theme catalogs as lazy chunks. None of them load with the `highlights` backend and our registered theme, but they were embedded in the binary: `internal/webui/dist` grew from 4.0 MB (42 files) to 14 MB (420 files).

`ui/vite.config.ts` now aliases `shiki/langs`, `shiki/wasm`, `@pierre/theming/themes` and `@pierre/highlights/themes/loader` to `ui/src/diffs-stubs.ts`. `dist` is now 2.5 MB (24 files), smaller than before the switch. `shiki/core` and the JS regex engine (~140 KB) still ship, because diffs imports them by name in several places.

If a future diffs version starts using one of these imports on the `highlights` path, the diff view breaks: an empty grammar list, a "no theme" error, or a thrown `shiki/wasm` call. Re-check the browser after bumping `@pierre/diffs`.

## Known costs

- **Fresh dependencies.** Both packages are 1–2 days old at install time (pre-1.0 highlights, many releases per month). They were installed past the 7-day release-age policy on purpose. Local pnpm 11 rejects `pnpm install --frozen-lockfile` until `@pierre/diffs@1.6.1` is one day old (2026-10-22 18:05 UTC). CI pins pnpm 10, which has no age gate.

## Verification

- `tsc`, `vite build`, `oxlint --type-aware --type-check`, `go vet` (both variants), `go test ./...` and the `withui` webui test pass.
- `nix build .#ccpeek-ui` passes with the new hash.
- Playwright e2e: 70/70 pass.
- Manual browser checks on the fixture corpus: edit diff and new-file view in light, dark, and dark-pinned-over-light-OS; shell command highlighting in the commands tab; GraphQL fence in a plan artifact. No console errors.
