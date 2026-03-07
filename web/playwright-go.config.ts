/// <reference types="node" />
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "../tests/e2e-go",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  webServer: {
    command:
      "cd .. && CGO_ENABLED=1 go run -tags sqlite_fts5 ./cmd/ccpeek --claude-dir testdata --port 4322",
    port: 4322,
    reuseExistingServer: !process.env.CI,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
