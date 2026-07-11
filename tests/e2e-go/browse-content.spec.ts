import { test, expect } from "@playwright/test";

// Browsing coverage for the unified artifact pages that replaced the v1
// per-sidecar browsers (plans, todos, snapshots, memories, …).

test.describe("browse artifacts", () => {
  test("lists artifacts and can view one", async ({ page }) => {
    await page.goto("/artifacts");
    await expect(
      page.getByRole("heading", { name: "Artifacts" }),
    ).toBeVisible();

    await page.locator("ul li a").first().click();
    await expect(page).toHaveURL(/\/artifacts\//);
    // Detail header shows "<agent> · <size> bytes".
    await expect(page.getByText(/bytes/)).toBeVisible();
  });

  test("kind filter narrows the list to plans", async ({ page }) => {
    await page.goto("/artifacts");
    await page.getByLabel("Filter by kind").selectOption("plan");

    const rows = page.locator("ul li");
    await expect(rows.first()).toBeVisible();
    // Every visible row carries the plan badge.
    await expect(rows.first().getByText("plan", { exact: true })).toBeVisible();
  });

  test("a plan renders as prose, not raw markdown", async ({ page }) => {
    await page.goto("/artifacts");
    await page.getByLabel("Filter by kind").selectOption("plan");
    await page.locator("ul li a").first().click();

    // Markdown artifacts render via the server-side goldmark hook.
    await expect(page.locator("[class*=prose]")).toBeVisible();
  });
});

test.describe("browse sessions", () => {
  test("filter narrows the session stream", async ({ page }) => {
    await page.goto("/sessions");
    const rows = page.locator("ul li a");
    await expect(rows.first()).toBeVisible();

    await page.getByPlaceholder("Filter by title…").fill("zzz-no-such-title");
    await expect(rows.first()).toBeHidden();
  });

  test("session detail links back from an artifact", async ({ page }) => {
    // Artifacts linked to sessions expose session chips that navigate to
    // the session page (session-centric model: artifacts attach to
    // sessions, not directories).
    await page.goto("/artifacts");
    await page.getByLabel("Filter by kind").selectOption("todo_list");
    await page.locator("ul li a").first().click();

    const chip = page.locator("a[href*='/sessions/']").first();
    await chip.click();
    await expect(page).toHaveURL(/\/sessions\//);
    await expect(
      page.getByRole("button", { name: /transcript/ }),
    ).toBeVisible();
  });
});
