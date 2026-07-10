import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The SPA builds into internal/webui/dist and ships inside the Go binary
// via go:embed — distribution stays a single binary (docs/v2-plan.md §4.1).
// In dev, Vite proxies /api to a running `ccpeek` server.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/v2/",
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://localhost:3000",
    },
  },
});
