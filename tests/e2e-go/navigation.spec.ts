import { test, expect } from "@playwright/test";

test.describe("navigation", () => {
  test("dashboard loads with correct title", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveTitle(/Dashboard - CCPeek/);
  });

  test("sidebar navigation links work", async ({ page }) => {
    await page.goto("/");

    const nav = page.locator("nav");
    const links = [
      { text: "Projects", url: "/projects/" },
      { text: "Plans", url: "/plans/" },
      { text: "Memories", url: "/memories/" },
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
    await expect(main.locator("a[href='/projects/']")).toBeVisible();
    await expect(main.locator("a[href='/plans/']")).toBeVisible();
  });

  test("dashboard shows recent conversations", async ({ page }) => {
    await page.goto("/");
    await expect(
      page.getByRole("heading", { name: "Recent Conversations" }),
    ).toBeVisible();
  });

  test("sidebar search form is visible", async ({ page }) => {
    await page.goto("/");
    const nav = page.locator("nav");
    await expect(nav.locator(".global-search-input")).toBeVisible();
  });

  test("sidebar search submits to search page", async ({ page }) => {
    await page.goto("/");
    const nav = page.locator("nav");
    const searchInput = nav.locator(".global-search-input");
    await searchInput.fill("hello");
    await searchInput.press("Enter");
    await expect(page).toHaveURL(/\/search\/\?q=hello/);
    await expect(page.locator("main").getByText('for "hello"')).toBeVisible();
  });
});
