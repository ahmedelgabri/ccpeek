import { test, expect } from "@playwright/test";

test.describe("browse plans", () => {
  test("lists plans and can view a plan", async ({ page }) => {
    await page.goto("/plans/");
    const main = page.locator("main");
    await expect(main.getByRole("heading", { name: "Plans" })).toBeVisible();

    // Click on first plan link (scoped to main to avoid nav links)
    const firstLink = main.locator("a.list-row").first();
    await firstLink.click();

    // Verify we're on a plan detail page with rendered markdown
    await expect(main.locator(".prose")).toBeVisible();
  });
});

test.describe("browse shell snapshots", () => {
  test("lists snapshots and can view one", async ({ page }) => {
    await page.goto("/shell-snapshots/");
    const main = page.locator("main");
    await expect(
      main.getByRole("heading", { name: "Shell Snapshots" }),
    ).toBeVisible();

    const firstLink = main.locator("a.list-row").first();
    await firstLink.click();

    // Chroma renders code blocks
    await expect(main.locator("pre")).toBeVisible();
  });
});

test.describe("browse todos", () => {
  test("lists todos with status badges", async ({ page }) => {
    await page.goto("/todos/");
    const main = page.locator("main");
    await expect(main.getByRole("heading", { name: "Todos" })).toBeVisible();

    const firstLink = main.locator("a.list-row").first();
    await firstLink.click();

    // Verify todo items render
    await expect(main.locator("ul")).toBeVisible();
  });
});

test.describe("browse projects", () => {
  test("lists projects and can navigate to sessions", async ({ page }) => {
    await page.goto("/projects/");
    const main = page.locator("main");
    await expect(main.getByRole("heading", { name: "Projects" })).toBeVisible();

    // Click on first project
    const firstProject = main.locator("a.list-row").first();
    await firstProject.click();

    // Verify sessions heading
    await expect(main.getByText("sessions").first()).toBeVisible();
  });

  test("can view a conversation", async ({ page }) => {
    await page.goto("/projects/");
    const main = page.locator("main");

    // Navigate to first project
    await main.locator("a.list-row").first().click();

    // Click on a session link (contains "msgs" text)
    const sessionRow = main
      .locator(".list-row")
      .filter({ hasText: "msgs" })
      .first();
    await sessionRow.locator("a").click();

    // Verify conversation page loaded with message count
    await expect(main.getByText(/\d+ messages/).first()).toBeVisible();
  });
});

test.describe("browse file history", () => {
  test("lists file history entries", async ({ page }) => {
    await page.goto("/file-history/");
    const main = page.locator("main");
    await expect(
      main.getByRole("heading", { name: "File History" }),
    ).toBeVisible();

    const firstLink = main.locator("a.list-row").first();
    await expect(firstLink).toBeVisible();
  });
});

test.describe("browse conversations", () => {
  test("can navigate conversation tabs", async ({ page }) => {
    // Navigate directly to a session known to have commands, todos, file history, and code
    await page.goto(
      "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/",
    );
    const main = page.locator("main");

    // Should be on conversation page
    await expect(main.getByText(/\d+ messages/).first()).toBeVisible();

    // Click Commands tab
    const commandsTab = main.getByRole("link", { name: "Commands" });
    await commandsTab.click();
    await expect(page).toHaveURL(/\/commands\/$/);
    await expect(main.getByText(/\d+ commands/).first()).toBeVisible();

    // Click Conversation tab to go back
    const conversationTab = main.getByRole("link", { name: "Conversation" });
    await conversationTab.click();
    await expect(main.getByText(/\d+ messages/).first()).toBeVisible();
  });
});

test.describe("browse file history detail", () => {
  test("can view a file history detail", async ({ page }) => {
    await page.goto("/file-history/");
    const main = page.locator("main");
    await expect(
      main.getByRole("heading", { name: "File History" }),
    ).toBeVisible();

    const firstLink = main.locator("a.list-row").first();
    await firstLink.click();

    // Verify detail page has file version info
    await expect(main.getByText(/\d+ file versions/)).toBeVisible();
  });
});

test.describe("browse memories", () => {
  test("lists memories and can view one", async ({ page }) => {
    await page.goto("/memories/");
    const main = page.locator("main");
    await expect(main.getByRole("heading", { name: "Memories" })).toBeVisible();

    const firstLink = main.locator("a.list-row").first();
    await firstLink.click();

    // Verify detail page has rendered markdown and View Project link
    await expect(main.locator(".prose")).toBeVisible();
    await expect(main.getByText("View Project")).toBeVisible();
  });
});

test.describe("search", () => {
  test("plans search filters results", async ({ page }) => {
    await page.goto("/plans/");
    const main = page.locator("main");

    const searchInput = main.getByPlaceholder("Filter plans...");
    await expect(searchInput).toBeVisible();

    await searchInput.fill("implementation");

    const countText = main.getByText(/of \d+ items/);
    await expect(countText).toBeVisible();
  });

  test("projects search filters results", async ({ page }) => {
    await page.goto("/projects/");
    const main = page.locator("main");

    const searchInput = main.getByPlaceholder("Filter projects...");
    await expect(searchInput).toBeVisible();

    await searchInput.fill("dotfiles");

    const countText = main.getByText(/of \d+ items/);
    await expect(countText).toBeVisible();
  });
});
