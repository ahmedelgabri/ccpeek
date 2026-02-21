import { test, expect } from "@playwright/test";

test.describe("navigation", () => {
  test("dashboard loads with correct title", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveTitle(/Dashboard - Claude History/);
  });

  test("sidebar navigation links work", async ({ page }) => {
    await page.goto("/");

    const nav = page.locator("nav");
    const links = [
      { text: "Projects", url: "/projects/" },
      { text: "Plans", url: "/plans/" },
      { text: "Shell Snapshots", url: "/shell-snapshots/" },
      { text: "Todos", url: "/todos/" },
      { text: "File History", url: "/file-history/" },
    ];

    for (const { text, url } of links) {
      await nav.getByRole("link", { name: text }).click();
      await expect(page).toHaveURL(url);
    }
  });

  test("dashboard shows type cards", async ({ page }) => {
    await page.goto("/");
    const main = page.locator("main");
    await expect(
      main.getByRole("heading", { name: "Dashboard" }),
    ).toBeVisible();
    // Type cards contain count numbers
    await expect(main.locator("a[href='/projects/']")).toBeVisible();
    await expect(main.locator("a[href='/plans/']")).toBeVisible();
  });

  test("dashboard shows recent conversations", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByText("Recent Conversations")).toBeVisible();
  });
});
