import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const shim = (name: string) =>
  fileURLToPath(new URL(`./src/shims/${name}.ts`, import.meta.url));

// The SPA builds into internal/webui/dist and ships inside the Go binary
// via go:embed — distribution stays a single binary (docs/v2-plan.md §4.1).
// In dev, Vite proxies /api to a running `ccpeek` server.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/",
  resolve: {
    // CONSTRAINT: these shims exist because @pierre/diffs (pinned EXACT in
    // package.json for this reason) imports full-bundle `shiki` and
    // `@pierre/theming/themes`, whose loader maps make the bundler emit
    // every shiki grammar/theme plus the wasm engine (~9 MB of chunks the
    // app can never fetch) into the go:embed'd assets. Each shim serves the
    // library's exact import surface from the app's fine-grained @shikijs/*
    // deps instead. On ANY @pierre/diffs bump, re-verify (a) the names its
    // dist imports from "shiki"/"shiki/*"/"@pierre/theming/themes" still
    // match the shims' exports (rg 'from "shiki|from "@pierre/theming' in
    // the package dist), (b) `vite build` emits no grammar chunks beyond
    // the curated set in highlight.ts, and (c) an expanded diff still
    // highlights in both color schemes.
    alias: [
      // Dead wasm-engine branch: the bare `import("shiki/wasm")` expression
      // alone would emit the ~620 kB wasm chunk.
      { find: "shiki/wasm", replacement: shim("shiki-wasm") },
      // Exact match only, so any NEW `shiki/<subpath>` import a future
      // version introduces fails to resolve loudly instead of silently
      // re-pulling the full bundle.
      { find: /^shiki$/, replacement: shim("shiki") },
      {
        find: "@pierre/theming/themes",
        replacement: shim("pierre-theming-themes"),
      },
    ],
  },
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    // Assets ship embedded in the binary and serve from localhost, so
    // chunk size is a non-issue (docs/v2-plan.md §4.1) — the echarts
    // chunk trips the default 500 kB warning.
    chunkSizeWarningLimit: 1024,
  },
  server: {
    proxy: {
      "/api": "http://localhost:3000",
    },
  },
});
