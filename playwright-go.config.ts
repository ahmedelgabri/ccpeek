/// <reference types="node" />
import { defineConfig, devices } from "@playwright/test";
import { tmpdir } from "node:os";
import { join } from "node:path";

const testDb = join(tmpdir(), "ccpeek-e2e-test.db");

export default defineConfig({
  testDir: "./tests/e2e-go",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  webServer: {
    command: `CGO_ENABLED=1 go run -tags sqlite_fts5 ./cmd/ccpeek --claude-dir testdata --port 4322 --data-file ${testDb} --rebuild`,
    port: 4322,
    reuseExistingServer: !process.env.CI,
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // Sandboxed environments pre-install a Chromium that may not match
        // this Playwright version's expected download; point at it instead
        // of re-fetching (e.g. PLAYWRIGHT_CHROMIUM_PATH=/opt/pw-browsers/chromium).
        launchOptions: process.env.PLAYWRIGHT_CHROMIUM_PATH
          ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH }
          : {},
      },
    },
  ],
});
