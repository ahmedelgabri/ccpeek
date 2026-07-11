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
  use: {
    baseURL: "http://127.0.0.1:4322",
  },
  webServer: {
    command: `go run ./cmd/ccpeek --claude-dir testdata --port 4322 --data-file ${testDb} --rebuild`,
    // The server binds before the first index pass finishes; readiness
    // flips to 200 once data is queryable, so tests never race the ingest.
    url: "http://127.0.0.1:4322/api/v1/ready",
    // Pin every agent root at the fixture corpus: --claude-dir only covers
    // Claude, and without these the suite would ingest (and secret-scan)
    // the developer's real ~/.codex, ~/.cursor, etc. into the test db.
    // Each adapter finds none of its layout under testdata and yields
    // zero sources.
    env: {
      PI_CODING_AGENT_DIR: "testdata",
      CODEX_HOME: "testdata",
      OPENCODE_DATA_DIR: "testdata",
      CCPEEK_CURSOR_DIR: "testdata",
    },
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
