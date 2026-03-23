/// <reference types="node" />
import { defineConfig, devices } from "@playwright/test";
import { tmpdir } from "node:os";
import { join } from "node:path";

const testDb = join(tmpdir(), "ccpeek-e2e-cursor-test.db");

export default defineConfig({
  testDir: "./tests/e2e-go",
  testMatch: "**/cursor-mixed.spec.ts",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  webServer: {
    command: `CGO_ENABLED=1 go run -tags sqlite_fts5 ./cmd/ccpeek --claude-dir testdata --cursor-dir testdata/cursor-fixture --port 4323 --data-file ${testDb} --rebuild --skip-scan`,
    port: 4323,
    reuseExistingServer: !process.env.CI,
  },
  use: {
    baseURL: "http://127.0.0.1:4323",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
