// The Inbox against `crowsnest --demo`: the list (blocking question, the demo's terminal dialog,
// every other kind) and a question sheet with a mermaid diagram and a diff, at phone and desktop
// sizes in both themes; then a real answer round trip through the API. Also checks the 375 px
// rules: no sideways scroll, 44 px targets. Run with `mise run screenshots:inbox`.
import { expect, test, type Page } from "@playwright/test";

const themes = ["light", "dark"] as const;
const sizes = [
  { name: "375x812", width: 375, height: 812 },
  { name: "1440x900", width: 1440, height: 900 },
] as const;

async function openInbox(page: Page, theme: (typeof themes)[number], search = "") {
  await page.addInitScript((value) => localStorage.setItem("crowsnest-theme", value), theme);
  await page.goto(`/inbox${search}`);
  await expect(page.getByRole("list", { name: "Needs you" })).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

async function noSidewaysScroll(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
}

for (const theme of themes) {
  for (const size of sizes) {
    test(`inbox list at ${size.name} (${theme})`, async ({ page }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      await openInbox(page, theme);
      const list = page.getByRole("list", { name: "Needs you" });
      await expect(list.getByRole("button").first()).toContainText("Apply the fleet cache refactor?");
      await expect(list.getByText(/is waiting at a dialog/)).toBeVisible();
      // The badge counts open questions plus the dialog.
      await expect(page.getByRole("navigation", { name: "Views" }).first().getByLabel(/need you/)).toBeVisible();
      if (size.width < 768) await noSidewaysScroll(page);
      await page.screenshot({ path: `screenshots/inbox-list-${size.name}-${theme}.png`, animations: "disabled", caret: "hide" });
    });

    test(`question sheet at ${size.name} (${theme})`, async ({ page }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      await openInbox(page, theme, "?q=q_1");
      const detail = size.width < 768 ? page.getByRole("dialog") : page.getByRole("region", { name: "Selected item" });
      await expect(detail.getByRole("region", { name: "Diff of internal/fleet/cache.go" })).toBeVisible();
      await expect(detail.locator("svg[id^=mermaid], .mermaid svg, svg[aria-roledescription]").first()).toBeVisible({ timeout: 15_000 });
      await expect(detail.getByRole("button", { name: /Apply it/ })).toContainText("Recommended");
      const layout = size.width >= 1024 ? "split" : "unified";
      await expect(detail.locator(`table[data-layout=${layout}]`)).toBeVisible();
      if (size.width < 768) await noSidewaysScroll(page);
      await page.screenshot({ path: `screenshots/inbox-question-${size.name}-${theme}.png`, animations: "disabled", caret: "hide" });
    });
  }
}

test("multi-select question at 375 px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await openInbox(page, "light", "?q=q_2");
  const sheet = page.getByRole("dialog");
  await sheet.getByRole("checkbox", { name: /Data race/ }).check();
  await sheet.getByRole("checkbox", { name: /Document the 150 ms window/ }).check();
  await expect(sheet.getByRole("button", { name: /Submit \(2\)/ })).toBeEnabled();
  await page.screenshot({ path: "screenshots/inbox-multi-375x812-light.png", animations: "disabled", caret: "hide" });
});

test("tap targets in the Inbox are at least 44 px on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await openInbox(page, "light", "?q=q_1");
  const small = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("[role=dialog] button, [role=dialog] textarea, [role=dialog] input")]
      .filter((element) => {
        const box = element.getBoundingClientRect();
        // Gap rows inside a diff table are a full-width strip, not a tap target that needs height.
        return box.width > 0 && box.height > 0 && !element.closest("table") && (box.width < 43.5 || box.height < 43.5);
      })
      .map((element) => element.getAttribute("aria-label") ?? element.textContent?.trim() ?? element.tagName),
  );
  expect(small).toEqual([]);
});

test("answering a question moves it to History", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openInbox(page, "light", "?q=q_3");
  const detail = page.getByRole("region", { name: "Selected item" });
  await detail.getByRole("textbox", { name: /Note/ }).fill("Generated is fine");
  await detail.getByRole("button", { name: /Both, generated from commits/ }).click();
  await expect(detail.getByRole("region", { name: "Outcome" })).toContainText("Both, generated from commits", { timeout: 10_000 });
  await expect(page.getByRole("list", { name: "Needs you" }).getByText("Where should the release notes live?")).toHaveCount(0);
  await page.getByRole("tab", { name: "History" }).click();
  await expect(page.getByRole("list", { name: "History" }).getByText("Where should the release notes live?")).toBeVisible();
});
