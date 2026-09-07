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

for (const failure of ["network", "http"] as const) {
  test(`first health ${failure} failure recovers without focus or reload`, async ({
    page,
  }) => {
    let available = false;
    let attempts = 0;
    let navigations = 0;
    page.on("request", (request) => {
      if (request.isNavigationRequest() && request.frame() === page.mainFrame())
        navigations++;
    });
    // Recovery must come from health polling, not a delayed SSE invalidate.
    await page.route("**/api/v1/events", (route) =>
      route.fulfill({ status: 501, body: "disabled for this test" }),
    );
    await page.route("**/api/v1/health", async (route) => {
      attempts++;
      if (available) return route.continue();
      if (failure === "network") return route.abort();
      return route.fulfill({
        status: 503,
        json: { error: "synthetic unavailable" },
      });
    });
    await page.goto("/sessions");
    await expect(page.getByRole("alert")).toContainText(
      "Retrying automatically",
    );
    await expect(page.getByRole("button", { name: "Retry now" })).toBeVisible();
    const beforeRecovery = attempts;
    available = true;
    await expect(
      page.getByRole("heading", { name: "Sessions", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("alert")).not.toBeVisible();
    expect(attempts).toBeGreaterThan(beforeRecovery);
    expect(navigations).toBe(1);
  });
}

test("unfinished archive runs warn and recover without local indexing or SSE", async ({
  page,
}) => {
  let status = "running";
  let healthRequests = 0;
  let navigations = 0;
  page.on("request", (request) => {
    if (request.isNavigationRequest() && request.frame() === page.mainFrame())
      navigations++;
  });
  await page.route("**/api/v1/events", (route) =>
    route.fulfill({ status: 501, body: "disabled for this test" }),
  );
  await page.route("**/api/v1/health", (route) => {
    healthRequests++;
    return route.fulfill({
      json: {
        schema: "ccpeek/v1",
        data: {
          status: "ok",
          indexing: false,
          archive: { lastRun: { status } },
        },
      },
    });
  });
  await page.goto("/sessions");
  const warning = page
    .getByRole("status")
    .filter({ hasText: "An index pass is running or was interrupted." });
  await expect(warning).toBeVisible();
  await expect(warning).toContainText("ccpeek ingest");
  await expect(
    page.getByRole("heading", { name: "Sessions", exact: true }),
  ).toBeVisible();
  await expect.poll(() => healthRequests).toBeGreaterThanOrEqual(2);
  status = "ok";
  await expect(warning).not.toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Sessions", exact: true }),
  ).toBeVisible();
  expect(navigations).toBe(1);
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
