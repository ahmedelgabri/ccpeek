import { test, expect } from "@playwright/test";

test.describe("cursor mixed-source fixture", () => {
  test("projects list shows both Claude and Cursor sources", async ({
    page,
  }) => {
    await page.goto("/projects/");
    const main = page.locator("main");

    await expect(main.getByRole("heading", { name: "Projects" })).toBeVisible();
    await expect(main.getByText("Cursor").first()).toBeVisible();
    await expect(main.getByText("Claude Code").first()).toBeVisible();

    const cursorProject = main.locator(
      "a.list-row[href='/projects/cursor-e2e/']",
    );
    await expect(cursorProject).toBeVisible();
    await cursorProject.click();

    await expect(page).toHaveURL("/projects/cursor-e2e/");
    await expect(main.getByText("Cursor").first()).toBeVisible();
  });

  test("plans list renders Cursor plan badge", async ({ page }) => {
    await page.goto("/plans/");
    const main = page.locator("main");
    await expect(main.getByRole("heading", { name: "Plans" })).toBeVisible();

    const cursorPlan = main
      .locator("a.list-row")
      .filter({ hasText: "Cursor E2E Plan" })
      .first();
    await expect(cursorPlan).toBeVisible();
    await expect(cursorPlan).toContainText("Cursor");
  });
});
