import { readFile } from "node:fs/promises";
import { test, expect } from "@playwright/test";

// Regression cover for the UI/UX pass. Each test pins a defect that
// shipped and was found by looking at the running app rather than by
// reading it — the kind type checks and unit tests cannot see.

const parseUSD = (s: string) => {
  const m = s.match(/\$([0-9.]+)/);
  return m ? Number(m[1]) : 0;
};

test.describe("layout", () => {
  // The Overview's session titles sat in a flex row with `truncate` but no
  // `min-w-0`, so they kept their full intrinsic width and pushed the
  // document's min-content out to ~1222px. Every viewport narrower than
  // `xl` got a horizontal scrollbar: 428px of overflow at 1024, 854px at
  // 390. It was the landing page.
  for (const [width, height, name] of [
    [390, 844, "phone"],
    [768, 900, "tablet"],
    [1024, 800, "laptop"],
    [1440, 900, "desktop"],
  ] as const) {
    test(`no horizontal overflow at ${name} (${width}px)`, async ({ page }) => {
      await page.setViewportSize({ width, height });
      for (const route of ["/", "/sessions", "/usage", "/commands"]) {
        await page.goto(route);
        // Let the queries land — overflow only appears once rows render.
        await expect(page.locator("h1").first()).toBeVisible();
        await page.waitForTimeout(500);
        const overflow = await page.evaluate(() => {
          const de = document.documentElement;
          return de.scrollWidth - de.clientWidth;
        });
        expect(
          overflow,
          `${route} overflows at ${width}px`,
        ).toBeLessThanOrEqual(2);
      }
    });
  }
});

test.describe("figures tell the truth", () => {
  // fmtCost printed four decimals below $1, so a zero-cost session — the
  // overwhelming majority in any corpus — rendered "$0.0000": false
  // precision, repeated down a whole column.
  test("zero cost reads as absent, not as $0.0000", async ({ page }) => {
    await page.goto("/sessions");
    await expect(page.locator("ul li a").first()).toBeVisible();
    expect(await page.locator("body").innerText()).not.toContain("$0.0000");
  });

  // The usage header total came from the rollup query, which is disabled
  // on the blocks view but keeps its placeholder data — so the headline
  // showed the previous grouping's sum above a table that added up to
  // something else entirely.
  test("the usage total matches the rows on screen", async ({ page }) => {
    await page.goto("/usage");
    await page.getByRole("radio", { name: "blocks" }).click();
    await expect(
      page
        .locator("table tbody tr")
        .first()
        .or(page.getByText("No usage recorded yet.")),
    ).toBeVisible();

    const rows = page.locator("table tbody tr");
    const n = await rows.count();
    test.skip(n === 0, "fixture corpus has no rolling-window usage");

    // Sub-cent rows render as "<$0.01", which is a bound and not a value,
    // so the row total is an interval. That is wide enough to keep the
    // test honest and still far narrower than the bug it pins: the stale
    // header was $0.074 against a $0.044 table.
    let low = 0;
    let high = 0;
    for (let i = 0; i < n; i++) {
      // Columns: window | sessions | tokens | cost | token share
      const cell = await rows.nth(i).locator("td").nth(3).innerText();
      if (cell.includes("<$")) high += 0.01;
      else {
        low += parseUSD(cell);
        high += parseUSD(cell);
      }
    }
    // The headline figure lives beside the page heading.
    const header = await page
      .locator("h1")
      .first()
      .locator("xpath=..")
      .innerText();
    const shown = parseUSD(header);
    expect(shown).toBeGreaterThanOrEqual(low - 0.005);
    expect(shown).toBeLessThanOrEqual(high + 0.005);
  });
});

test.describe("cost provenance", () => {
  test("mode survives usage drill-down into a session", async ({ page }) => {
    await page.goto("/usage");
    const usageMode = page.getByRole("radiogroup", {
      name: "Cost provenance",
    });
    await usageMode.getByRole("radio", { name: "calculate" }).click();
    await expect(page).toHaveURL(/\/usage\?.*cost_mode=calculate/);
    await expect(
      usageMode.getByRole("radio", { name: "calculate" }),
    ).toHaveAttribute("aria-checked", "true");

    await page.locator("table tbody tr td").first().getByRole("link").click();
    await expect(page).toHaveURL(/\/sessions\?.*cost_mode=calculate/);
    await expect(
      page
        .getByRole("radiogroup", { name: "Cost provenance" })
        .getByRole("radio", { name: "calculate" }),
    ).toHaveAttribute("aria-checked", "true");

    await page.locator("ul li a").first().click();
    await expect(page).toHaveURL(
      /\/sessions\/[^/]+\/[^?]+\?.*cost_mode=calculate/,
    );
    await expect(
      page
        .getByRole("radiogroup", { name: "Cost provenance" })
        .getByRole("radio", { name: "calculate" }),
    ).toHaveAttribute("aria-checked", "true");
  });

  test("timeline CSV names incomplete-cost columns explicitly", async ({
    page,
  }) => {
    await page.goto("/usage");
    const exportButton = page.getByRole("button", { name: "export CSV" });
    await expect(exportButton).toBeVisible();
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      exportButton.click(),
    ]);
    const path = await download.path();
    expect(path).not.toBeNull();
    const csv = await readFile(path!, "utf8");
    const header = csv.slice(0, csv.indexOf("\n"));
    expect(header).toContain("_has_incomplete_cost");
    expect(header).not.toContain("_has_unpriced");
  });
});

test.describe("accessibility", () => {
  // The session facets were a row of styled <button>s announcing
  // themselves as unrelated buttons; they are one tablist.
  test("session facets are a tablist", async ({ page }) => {
    await page.goto("/sessions");
    await page.locator("ul li a").first().click();
    await expect(page.getByRole("tab").first()).toBeVisible();
    await expect(page.getByRole("tab", { name: /transcript/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  // The heatmap was 364 mouse-only <rect>s: its figures were unreachable
  // by keyboard and invisible to a screen reader. Active days are links to
  // that day's sessions, which is both the fix and the obvious gesture.
  test("active heatmap days are links to that day", async ({ page }) => {
    await page.goto("/");
    const day = page.locator("svg a[href*='/sessions']").first();
    await expect(day).toBeVisible();
    await expect(day).toHaveAttribute("aria-label", /\d{4}-\d{2}-\d{2}:/);
    await day.click();
    await expect(page).toHaveURL(/\/sessions\?.*since=/);
  });

  // There was no focus-visible rule anywhere in the app: keyboard users
  // got whatever the UA happened to draw, off-palette and ignoring each
  // control's radius.
  test("keyboard focus draws the accent ring", async ({ page }) => {
    await page.goto("/sessions");
    await expect(page.locator("h1").first()).toBeVisible();
    await page.keyboard.press("Tab");
    // The ring fades in on elements carrying transition-colors.
    await page.waitForTimeout(400);
    const ring = await page.evaluate(() => {
      const s = getComputedStyle(document.activeElement!);
      return {
        width: s.outlineWidth,
        style: s.outlineStyle,
        color: s.outlineColor,
      };
    });
    expect(ring.style).toBe("solid");
    expect(ring.width).toBe("2px");
    // --color-accent resolves per scheme; the run's scheme is not pinned.
    expect(["rgb(122, 162, 247)", "rgb(48, 96, 201)"]).toContain(ring.color);
  });
});

test.describe("sidebar", () => {
  // The rail collapses to a slim strip of abbreviations. Nothing may be
  // lost in the shrink: every link keeps its full accessible name, the
  // choice survives a reload, and `[` toggles it from the keyboard.
  test("collapses to a rail, persists, and stays operable", async ({
    page,
  }) => {
    await page.goto("/");
    const aside = page.locator("aside");
    const sessions = aside.getByRole("link", { name: "Sessions" });
    await expect(sessions).toHaveText("Sessions");

    await aside.getByRole("button", { name: "Collapse sidebar" }).click();
    await expect(sessions).toHaveText("Se");
    await sessions.click();
    await expect(page).toHaveURL(/\/sessions/);

    await page.reload();
    const expand = aside.getByRole("button", { name: "Expand sidebar" });
    await expect(expand).toBeVisible();
    await expect(expand).toHaveAttribute("aria-expanded", "false");
    await expect(sessions).toHaveText("Se");

    await expand.click();
    await expect(sessions).toHaveText("Sessions");

    await page.keyboard.press("[");
    await expect(sessions).toHaveText("Se");
  });
});
