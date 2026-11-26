// The command palette against `crowsnest --demo` at 375 and 1440 px, light and dark: open with no
// query (recent, needs you, views, live jobs, actions), with a query (highlights and the Give job
// row), a job's actions, and the keyboard shortcuts sheet. Run with `mise run screenshots:palette`.
import type { Page } from "@playwright/test";
import { expect, test } from "../support/fixtures";

const sizes = [
  { name: "375x812", width: 375, height: 812 },
  { name: "1440x900", width: 1440, height: 900 },
] as const;

const shot = (page: Page, name: string) => page.screenshot({ path: `screenshots/${name}.png`, animations: "disabled", caret: "hide" });

async function openPalette(page: Page, theme: "light" | "dark", width: number, height: number) {
  await page.setViewportSize({ width, height });
  await page.addInitScript((value) => {
    localStorage.setItem("crowsnest-theme", value);
    // A recent item or two, as after a little use.
    localStorage.setItem("crowsnest-palette-recent", JSON.stringify(["nav:/fleet", "job:j_1"]));
  }, theme);
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  await page.getByRole("button", { name: "Search or run…" }).click();
  await expect(page.getByRole("dialog", { name: "Command palette" }).getByRole("group", { name: "Needs you" })).toBeVisible();
}

for (const theme of ["light", "dark"] as const) {
  for (const size of sizes) {
    test(`palette at ${size.name} (${theme})`, async ({ page }) => {
      await openPalette(page, theme, size.width, size.height);
      await expect(page.getByRole("group", { name: "Recent" })).toBeVisible();
      await shot(page, `palette-${size.name}-${theme}`);

      await page.getByRole("combobox", { name: "Search or run a command" }).fill("fleet cache");
      await expect(page.locator("[role=option] mark").first()).toBeVisible();
      await shot(page, `palette-query-${size.name}-${theme}`);

      await page.keyboard.press("ArrowRight");
      await expect(page.getByRole("listbox", { name: /Actions for/ })).toBeVisible();
      await shot(page, `palette-actions-${size.name}-${theme}`);
    });
  }
}

for (const size of sizes) {
  test(`shortcuts sheet at ${size.name}`, async ({ page }) => {
    await openPalette(page, "light", size.width, size.height);
    await page.getByRole("combobox", { name: "Search or run a command" }).fill("keyboard shortcuts");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeVisible();
    await shot(page, `palette-shortcuts-${size.name}-light`);
  });
}
