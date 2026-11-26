// The Fleet view with fixtures, photographed at phone and desktop sizes in both themes, plus the
// mobile layout rules (DESIGN.md section 12): no horizontal page scroll at 375 px and 44 px tap
// targets. Screenshots land in web/screenshots/ and are committed, so a change in the look shows
// up in review. Run with `mise run screenshots`.
import { expect, test, type Page } from "@playwright/test";

const themes = ["light", "dark"] as const;
const MIN_TARGET_PX = 44;

async function openFleet(page: Page, theme: (typeof themes)[number], search = "") {
  await page.addInitScript((value) => localStorage.setItem("crowsnest-theme", value), theme);
  await page.goto(`/fleet?fixtures=1${search}`);
  await expect(page.getByRole("list", { name: "Workspaces" })).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

async function screenshot(page: Page, name: string) {
  await page.screenshot({ path: `screenshots/${name}.png`, animations: "disabled", caret: "hide" });
}

/** Fails with the offending elements when the page scrolls sideways. */
async function expectNoHorizontalScroll(page: Page) {
  const overflow = await page.evaluate(() => {
    const width = document.documentElement.clientWidth;
    const wide = [...document.querySelectorAll<HTMLElement>("body *")]
      .filter((element) => element.getBoundingClientRect().right > width + 1 && element.getClientRects().length > 0)
      .filter((element) => !element.closest("pre, [data-allow-overflow]"))
      .slice(0, 5)
      .map((element) => `${element.tagName.toLowerCase()}.${element.className}`.slice(0, 120));
    return { scrollWidth: document.documentElement.scrollWidth, width, wide };
  });
  expect(overflow.wide, "elements wider than the viewport").toEqual([]);
  expect(overflow.scrollWidth).toBeLessThanOrEqual(overflow.width);
}

/** Every visible button, link and text input is at least 44 px in both directions. */
async function expectTapTargets(page: Page) {
  const small = await page.evaluate((min) => {
    return [...document.querySelectorAll<HTMLElement>("button, a[href], input, select, textarea, [role=tab]")]
      .filter((element) => {
        const box = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        return box.width > 0 && box.height > 0 && style.visibility !== "hidden";
      })
      .filter((element) => {
        const box = element.getBoundingClientRect();
        return box.height < min - 0.5 || box.width < min - 0.5;
      })
      .map((element) => {
        const box = element.getBoundingClientRect();
        const name = element.getAttribute("aria-label") ?? element.textContent?.trim().slice(0, 30) ?? "";
        return `${element.tagName.toLowerCase()} "${name}" ${Math.round(box.width)}x${Math.round(box.height)}`;
      });
  }, MIN_TARGET_PX);
  expect(small, "tap targets under 44 px").toEqual([]);
}

for (const theme of themes) {
  test.describe(`${theme} theme`, () => {
    test(`fleet at 375x812 (${theme})`, async ({ page }) => {
      await page.setViewportSize({ width: 375, height: 812 });
      await openFleet(page, theme);
      await expectNoHorizontalScroll(page);
      await expectTapTargets(page);
      // The footer follows the list rather than sitting on top of it.
      const list = await page.getByRole("list", { name: "Workspaces" }).boundingBox();
      const footer = await page.locator("main footer").boundingBox();
      expect(footer!.y).toBeGreaterThanOrEqual(list!.y + list!.height - 1);
      await screenshot(page, `fleet-375-${theme}`);
    });

    test(`agent sheet at 375x812 (${theme})`, async ({ page }) => {
      await page.setViewportSize({ width: 375, height: 812 });
      await openFleet(page, theme);
      await page.locator("button[data-needs-you]").first().click(); // head-loss, sorted first
      const sheet = page.getByRole("dialog");
      await expect(sheet.getByRole("list", { name: "Options" })).toBeVisible();
      await expectNoHorizontalScroll(page);
      await expectTapTargets(page);
      await screenshot(page, `fleet-375-agent-${theme}`);
    });

    test(`fleet at 1440x900 (${theme})`, async ({ page }) => {
      await page.setViewportSize({ width: 1440, height: 900 });
      await openFleet(page, theme, "&agent=w3%3Ap1");
      await expect(page.getByRole("list", { name: "Options" })).toBeVisible();
      await expectNoHorizontalScroll(page);
      await screenshot(page, `fleet-1440-${theme}`);
    });
  });
}

test("pressing a dialog option answers it (fixtures)", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openFleet(page, "light", "&agent=w3%3Ap1");
  await page.getByRole("button", { name: /^1\s*Yes$/ }).click();
  await expect(page.getByText("Sent “1” to head-loss")).toBeVisible();
  await expect(page.getByRole("list", { name: "Options" })).toBeHidden();
});

test("every view fits 375 px with 44 px targets", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await openFleet(page, "light");
  for (const label of ["Jobs", "Chat", "Inbox", "Fleet", "Terminal"]) {
    await page.getByRole("navigation", { name: "Views" }).getByRole("link", { name: label }).last().click();
    await expectNoHorizontalScroll(page);
    await expectTapTargets(page);
  }
});
