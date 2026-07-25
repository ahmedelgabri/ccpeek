import { test, expect } from "@playwright/test";

// Smoke coverage for the SPA served at / and its /api/v1 backend
// (docs/v2-plan.md §8.2: the v2.0 cutover serves the SPA at the root).

test.describe("SPA", () => {
  test("serves the overview shell", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveTitle("CCPeek");
    // Headline stat tiles render (fixture corpus has sessions).
    await expect(
      page.getByText("Sessions", { exact: true }).first(),
    ).toBeVisible();
  });

  test("lists sessions grouped by day", async ({ page }) => {
    await page.goto("/sessions");
    await expect(page.locator("ul li a").first()).toBeVisible();
  });

  test("navigates to a session detail with transcript", async ({ page }) => {
    await page.goto("/sessions");
    await page.locator("ul li a").first().click();
    await expect(page).toHaveURL(/\/sessions\//);
    await expect(page.getByRole("tab", { name: /transcript/ })).toBeVisible();
    // Stat tiles include cost.
    await expect(page.getByText("Cost", { exact: true })).toBeVisible();
  });

  test("usage explorer groups by model", async ({ page }) => {
    await page.goto("/usage");
    await page.getByRole("radio", { name: "model" }).click();
    // The v1 e2e corpus predates usage capture, so either rollup rows or
    // the explicit empty state must render — never a broken page.
    await expect(
      page
        .locator("table tbody tr")
        .first()
        .or(page.getByText(/No usage (recorded yet|in this range)\./)),
    ).toBeVisible();
  });

  test("search returns hits with session links", async ({ page }) => {
    await page.goto("/search");
    await page.getByPlaceholder(/Search sessions/).fill("hello");
    await expect(page.locator("ul li").first()).toBeVisible();
  });

  test("commands browser lists shell commands", async ({ page }) => {
    await page.goto("/commands");
    await expect(
      page
        .locator("ul li pre")
        .first()
        .or(page.getByText("No commands match.")),
    ).toBeVisible();
  });
});

test.describe("/api/v1", () => {
  test("sessions endpoint returns the envelope", async ({ request }) => {
    const res = await request.get("/api/v1/sessions?limit=5");
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.schema).toBe("ccpeek/v1");
    expect(Array.isArray(body.data)).toBeTruthy();
  });

  test("usage endpoint rejects bogus groups", async ({ request }) => {
    const res = await request.get("/api/v1/usage?group=bogus");
    expect(res.status()).toBe(400);
  });

  test("stats endpoint returns overview counts", async ({ request }) => {
    const res = await request.get("/api/v1/stats");
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.data.sessions).toBeGreaterThan(0);
  });

  test("all five agents ingest their fixture corpora", async ({ request }) => {
    const res = await request.get("/api/v1/stats");
    const body = await res.json();
    const agents = (body.data.agents ?? []).map(
      (a: { agent: string }) => a.agent,
    );
    for (const slug of ["claude-code", "pi", "codex", "opencode", "cursor"]) {
      expect(agents, `agent ${slug} missing from stats`).toContain(slug);
    }
  });

  test("commands endpoint exports shell history", async ({ request }) => {
    const res = await request.get("/api/v1/commands?format=zsh&limit=5");
    expect(res.ok()).toBeTruthy();
    expect(res.headers()["content-type"]).toContain("text/plain");
  });
});

test.describe("resilience", () => {
  // The app had no error boundary: any uncaught render error unmounted
  // the whole tree and left a blank page with the reason only in the
  // console. An unknown URL must say so, with the shell still usable.
  test("an unknown route explains itself instead of blanking", async ({
    page,
  }) => {
    await page.goto("/no-such-view");
    await expect(page.getByText("Nothing here.")).toBeVisible();
    // The shell survives: navigation is still there and still works.
    await page.getByRole("link", { name: "Back to the overview" }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(
      page.getByText("Sessions", { exact: true }).first(),
    ).toBeVisible();
  });

  // A non-JSON error reply used to surface as "Unexpected token < in
  // JSON" because the client parsed before checking the status. The Host
  // guard returns exactly such a reply.
  test("rejects a rebound Host with a plain-text 403", async ({ request }) => {
    const res = await request.get("/api/v1/sessions", {
      headers: { Host: "evil.example" },
    });
    expect(res.status()).toBe(403);
    expect(res.headers()["content-type"]).toContain("text/plain");
    expect(await res.text()).toContain("127.0.0.1 only");
  });

  // Search hits keep literal brackets literal: the FTS delimiters used to
  // be [ and ], indistinguishable from brackets in source code. The
  // control characters that replaced them must never reach the DOM.
  test("search marks matches without leaking delimiters", async ({ page }) => {
    await page.goto("/search");
    await page.getByPlaceholder(/Search sessions/).fill("rate");
    const results = page.locator("ul li");
    await expect(results.first()).toBeVisible({ timeout: 10_000 });
    const text = await results.first().innerText();
    expect(text).not.toContain("\u0002");
    expect(text).not.toContain("\u0003");
    // The match itself is marked.
    await expect(results.first().locator("mark").first()).toBeVisible();
  });
});
