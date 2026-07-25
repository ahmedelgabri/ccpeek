import { test, expect } from "@playwright/test";

test.describe("navigation", () => {
  test("shell loads with correct title", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveTitle("CCPeek");
  });

  test("sidebar navigation links work", async ({ page }) => {
    await page.goto("/");

    const nav = page.locator("aside nav");
    const links = [
      { text: "Sessions", url: "/sessions" },
      { text: "Commands", url: "/commands" },
      { text: "Usage", url: "/usage" },
      { text: "Artifacts", url: "/artifacts" },
      { text: "Scan", url: "/scan" },
      { text: "Compare", url: "/compare" },
      { text: "Search", url: "/search" },
      { text: "Overview", url: "/" },
    ];

    for (const { text, url } of links) {
      await nav.getByRole("link", { name: text, exact: true }).click();
      await expect(page).toHaveURL(url);
    }
  });

  test("client routes survive a full page load", async ({ page }) => {
    // History-routing fallback: a deep link must serve the SPA shell, not
    // 404 — and /commands, /scan, and /search must not ping-pong with
    // their legacy trailing-slash redirects.
    await page.goto("/usage");
    await expect(page.getByRole("heading", { name: "Usage" })).toBeVisible();

    await page.goto("/commands");
    await expect(page.getByRole("heading", { name: "Commands" })).toBeVisible();

    await page.goto("/scan");
    await expect(
      page.getByRole("heading", { name: "Secret scan" }),
    ).toBeVisible();

    await page.goto("/search");
    await expect(page.getByRole("heading", { name: "Search" })).toBeVisible();
  });
});

// The v2.0 cutover keeps every v1 bookmark working via 301 redirects
// (docs/v2-plan.md §8.2).
test.describe("legacy v1 redirects", () => {
  const cases = [
    { from: "/projects/", to: "/" },
    { from: "/projects/test-project/", to: "/" },
    {
      from: "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/",
      to: "/sessions/claude-code/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    },
    {
      from: "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/commands/",
      to: "/sessions/claude-code/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    },
    { from: "/plans/", to: "/artifacts" },
    { from: "/shell-snapshots/", to: "/artifacts" },
    { from: "/todos/", to: "/artifacts" },
    { from: "/tasks/", to: "/artifacts" },
    { from: "/paste-cache/", to: "/artifacts" },
    { from: "/usage-data/", to: "/artifacts" },
    { from: "/memories/", to: "/artifacts" },
    { from: "/file-history/", to: "/artifacts" },
    { from: "/commands/", to: "/commands" },
    { from: "/scan/", to: "/scan" },
    { from: "/search/?q=hello", to: "/search?q=hello" },
    { from: "/v2/", to: "/" },
    { from: "/v2/usage", to: "/usage" },
  ];

  for (const { from, to } of cases) {
    test(`${from} redirects to ${to}`, async ({ request }) => {
      const res = await request.get(from, { maxRedirects: 0 });
      expect(res.status()).toBe(301);
      expect(res.headers()["location"]).toBe(to);
    });
  }

  test("a legacy conversation bookmark lands on the session page", async ({
    page,
  }) => {
    await page.goto(
      "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/",
    );
    await expect(page).toHaveURL(
      "/sessions/claude-code/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    );
    await expect(page.getByRole("tab", { name: /transcript/ })).toBeVisible();
  });
});
