import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The SPA builds into internal/webui/dist and ships inside the Go binary
// via go:embed — distribution stays a single binary (docs/v2-plan.md §4.1).
// In dev, Vite proxies /api to a running `ccpeek` server.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/",
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
      // Keep the browser-facing Host aligned with Origin. The API rejects
      // mutations from any other origin, including other loopback ports.
      "/api": { target: "http://localhost:3000", changeOrigin: false },
    },
  },
});
