import { test, expect } from "@playwright/test";

test("archive initialization shows the shell before loading data pages", async ({
  page,
}) => {
  let initializing = true;
  let healthRequests = 0;
  let dataRequests = 0;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/health") {
      healthRequests++;
      await route.fulfill({
        json: {
          schema: "ccpeek/v1",
          data: { status: "ok", indexing: initializing, initializing },
        },
      });
    } else {
      if (path !== "/api/v1/events") dataRequests++;
      await route.continue();
    }
  });
  await page.goto("/sessions");
  await expect(page.locator("aside nav")).toBeVisible();
  await expect(
    page.getByText("Opening your archive.", { exact: false }),
  ).toBeVisible();
  await expect.poll(() => healthRequests).toBeGreaterThanOrEqual(2);
  expect(dataRequests).toBe(0);

  initializing = false;
  await expect(
    page.getByRole("heading", { name: "Sessions", exact: true }),
  ).toBeVisible();
  await expect.poll(() => dataRequests).toBeGreaterThan(0);
  await expect(
    page.getByText("Opening your archive.", { exact: false }),
  ).not.toBeVisible();
});

test("failed initialization keeps the shell and reports the failure", async ({
  page,
}) => {
  await page.route("**/api/v1/health", (route) =>
    route.fulfill({
      json: {
        schema: "ccpeek/v1",
        data: {
          status: "ok",
          indexing: true,
          initializing: true,
          bootstrap: {
            state: "failed",
            error: "Archive initialization failed.",
          },
        },
      },
    }),
  );
  await page.goto("/");
  await expect(page.locator("aside nav")).toBeVisible();
  await expect(
    page.getByText(
      "Archive initialization failed. Check the terminal and restart ccpeek to retry.",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByText("Opening your archive.", { exact: false }),
  ).not.toBeVisible();
});
