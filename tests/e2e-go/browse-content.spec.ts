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
    // Detail header shows an agent chip and a human-readable size.
    await expect(page.getByText(/\d+(?:\.\d+)? (?:B|KB|MB)/)).toBeVisible();
  });

  test("kind filter narrows the list to plans", async ({ page }) => {
    await page.goto("/artifacts");
    await page.getByLabel("Filter by kind").selectOption("plan");

    const rows = page.locator("ul li");
    await expect(rows.first()).toBeVisible();
    // Every visible row carries the plan badge.
    await expect(rows.first().getByText("plan", { exact: true })).toBeVisible();
  });

  test("memory cross-links resolve to sibling artifacts", async ({ page }) => {
    // Memory files link to siblings with relative markdown links; the
    // artifact name carries a directory prefix the bare href loses, so
    // the app must resolve clicks against the current artifact's prefix.
    await page.goto(
      "/artifacts/claude-code/memory/" +
        encodeURIComponent("test-project/MEMORY.md"),
    );
    await page.getByRole("link", { name: "conventions" }).click();
    await expect(page).toHaveURL(/memory\/test-project%2Fconventions\.md/);
    await expect(
      page.getByRole("heading", { name: "Coding Conventions" }),
    ).toBeVisible();
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
    await expect(page.getByRole("tab", { name: /transcript/ })).toBeVisible();
  });
});

test.describe("transcript deep-link paging", () => {
  const bigSession = "deadbeef-1111-2222-3333-444444444444";

  test("backward pages tile without overlapping the anchor", async ({
    page,
  }) => {
    const transcriptRequests: string[] = [];
    page.on("request", (r) => {
      if (r.url().includes(`/${bigSession}/transcript`)) {
        transcriptRequests.push(r.url());
      }
    });

    // Deep link into the middle: the anchor sits 100 before the target.
    await page.goto(`/sessions/claude-code/${bigSession}?seq=500`);
    await expect(page.getByText("deep link message 500")).toBeVisible();

    // The anchored request covers from=400 with a full page limit.
    expect(
      transcriptRequests.some(
        (u) => u.includes("from=400") && u.includes("limit=1000"),
      ),
      `anchored request missing in ${transcriptRequests.join(", ")}`,
    ).toBeTruthy();

    // Scrolling to the top triggers the backward load. The page must be
    // bounded to the uncovered gap (0..399 → limit=400), NOT a fixed
    // 1000 that would re-cover 400..999 and duplicate seq keys.
    await page.evaluate(() => window.scrollTo(0, 0));
    await expect
      .poll(
        () =>
          transcriptRequests.some(
            (u) => u.includes("from=0") && u.includes("limit=400"),
          ),
        {
          message: `gap-bounded backward request missing in ${transcriptRequests.join(", ")}`,
        },
      )
      .toBeTruthy();
    expect(
      transcriptRequests.some(
        (u) => u.includes("from=0") && u.includes("limit=1000"),
      ),
      "backward request used a full page limit (overlap)",
    ).toBeFalsy();

    // No duplicate mounted rows: each rendered permalink seq is unique.
    const seqs = await page
      .locator("button[title='Copy link to this message']")
      .allTextContents();
    const unique = new Set(seqs);
    expect(unique.size, `duplicate mounted seqs among ${seqs.join(",")}`).toBe(
      seqs.length,
    );
  });
});

test.describe("lazy tool payloads", () => {
  const editSession = "11111111-aaaa-bbbb-cccc-111111111111";

  test("transcript loads chips only; excerpts arrive on expansion", async ({
    page,
  }) => {
    const toolRequests: string[] = [];
    page.on("request", (r) => {
      const u = r.url();
      if (u.includes(`/${editSession}/tools`)) toolRequests.push(u);
    });

    await page.goto(`/sessions/claude-code/${editSession}`);
    await expect(
      page.locator("button[title='Copy link to this message']").first(),
    ).toBeVisible();
    // Give any eager fetch a beat to fire before asserting it did not.
    await page.waitForTimeout(300);

    // The transcript triggers ONLY the compact chip projection — no full
    // tool list, no per-call detail, no eager page loop.
    expect(toolRequests.length).toBeGreaterThan(0);
    for (const u of toolRequests) {
      expect(
        u,
        `non-compact tools request before any tab/expansion: ${u}`,
      ).toContain("compact=1");
    }

    // Expanding the Edit chip fetches exactly that call's detail and
    // renders the diff.
    await page.getByRole("button", { name: /Edit/ }).first().click();
    await expect
      .poll(() => toolRequests.some((u) => /\/tools\/\d+(\?|$)/.test(u)))
      .toBeTruthy();

    // Opening the Tools tab is what starts the full (excerpt-free) list.
    const before = toolRequests.length;
    await page.getByRole("tab", { name: /^tools/ }).click();
    await expect
      .poll(() =>
        toolRequests
          .slice(before)
          .some((u) => u.includes("limit=500") && !u.includes("compact")),
      )
      .toBeTruthy();
  });
});

// The Tools and Files tabs are DOM-windowed: a session's tool list pages
// to completion (the tabs show counts and group by file, so they need
// every row), and mounting all of them is what the windowing avoids. The
// rows still have to render, stay expandable, and keep their table
// semantics — a windowed grid is not a <table>, so the roles carry it.
test.describe("session tool tabs", () => {
  const editSession = "11111111-aaaa-bbbb-cccc-111111111111";

  test("tools tab renders windowed rows that still expand", async ({
    page,
  }) => {
    await page.goto(`/sessions/claude-code/${editSession}?tab=tools`);

    const grid = page.getByRole("table", { name: "Tool calls" });
    await expect(grid).toBeVisible();
    await expect(
      grid.getByRole("columnheader", { name: "tool" }),
    ).toBeVisible();

    const rows = grid.getByRole("row");
    // Header plus at least one call.
    await expect.poll(() => rows.count()).toBeGreaterThan(1);

    // The Edit call's row opens its diff in place.
    const editRow = rows.filter({ hasText: "Edit" }).first();
    await expect(editRow).toBeVisible();
    await editRow.click();
    await expect(page.getByText(/diff too large|^[-+]/).first()).toBeVisible();
  });

  test("files tab lists touched files and opens their changes", async ({
    page,
  }) => {
    await page.goto(`/sessions/claude-code/${editSession}?tab=files`);
    // Rows show an edit/write tally; opening one reveals its changes. The
    // tally is pluralised, so a single change reads "1 edit".
    const row = page
      .locator("li")
      .filter({ hasText: /\d+ (edit|write)s?/ })
      .first();
    await expect(row).toBeVisible();
    await row.click();
    await expect(
      page.getByRole("button", { name: /↗ #/ }).first(),
    ).toBeVisible();
  });

  test("commands tab renders its windowed rows", async ({ page }) => {
    await page.goto(
      "/sessions/claude-code/22222222-aaaa-bbbb-cccc-222222222222?tab=commands",
    );
    // Highlighting splits the command into spans, so match the row rather
    // than a contiguous text node.
    await expect(
      page.locator("li").filter({ hasText: "go test" }).first(),
    ).toBeVisible();
  });
});
