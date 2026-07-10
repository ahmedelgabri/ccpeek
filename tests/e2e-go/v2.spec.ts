import { test, expect } from "@playwright/test";

// Smoke coverage for the v2 SPA served at /v2/ and its /api/v1 backend.
// This is the seed of the parity suite that gates the v2.0 cutover
// (docs/v2-plan.md §9 P1 exit criteria).

test.describe("v2 SPA", () => {
  test("serves the shell and lists sessions", async ({ page }) => {
    await page.goto("/v2/");
    await expect(page).toHaveTitle("CCPeek");
    // Fixture corpus sessions render in the stream with cost badges.
    await expect(page.locator("ul li a").first()).toBeVisible();
  });

  test("navigates to a session detail with transcript", async ({ page }) => {
    await page.goto("/v2/");
    await page.locator("ul li a").first().click();
    await expect(page).toHaveURL(/\/v2\/sessions\//);
    await expect(page.getByText("Transcript")).toBeVisible();
    // Stat tiles include cost.
    await expect(page.getByText("Cost")).toBeVisible();
  });

  test("usage explorer groups by model", async ({ page }) => {
    await page.goto("/v2/usage");
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
    await page.goto("/v2/search");
    await page.getByPlaceholder(/Search sessions/).fill("hello");
    await expect(page.locator("ul li").first()).toBeVisible();
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
});
