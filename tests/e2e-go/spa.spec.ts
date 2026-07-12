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
    await expect(
      page.getByRole("button", { name: /transcript/ }),
    ).toBeVisible();
    // Stat tiles include cost.
    await expect(page.getByText("Cost")).toBeVisible();
  });

  test("usage explorer groups by model", async ({ page }) => {
    await page.goto("/usage");
    await page.getByRole("button", { name: "model" }).click();
    // The v1 e2e corpus predates usage capture, so either rollup rows or
    // the explicit empty state must render — never a broken page.
    await expect(
      page
        .locator("table tbody tr")
        .first()
        .or(page.getByText("No usage recorded yet.")),
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

  test("commands endpoint exports shell history", async ({ request }) => {
    const res = await request.get("/api/v1/commands?format=zsh&limit=5");
    expect(res.ok()).toBeTruthy();
    expect(res.headers()["content-type"]).toContain("text/plain");
  });
});
