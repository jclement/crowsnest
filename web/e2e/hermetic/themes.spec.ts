// Themes in the browser (DESIGN.md "Design language"), against `crowsnest --demo`:
//
// - every theme, in both modes, passes axe (contrast included) on every view and on the
//   Appearance gallery itself; checked on desktop, since contrast does not depend on the width;
// - a reload never flashes the wrong theme: data-theme and the mode are on <html> at the first
//   frame, before React runs, for a browser's own choice and for the server's default (--theme);
// - the T key and the palette switch the theme, the Appearance sheet picks one and the choice
//   survives a reload; xterm follows (its background is the theme's terminal background).
import type { Page } from "@playwright/test";
import { expect, expectAccessible, isPhone, test } from "../support/fixtures";

const themes = ["crowsnest", "phosphor", "deep-sea", "ember", "contrast"] as const;

const views = [
  { name: "now", path: "/", ready: (page: Page) => page.getByRole("list", { name: "Projects" }) },
  { name: "jobs", path: "/jobs", ready: (page: Page) => page.getByRole("region", { name: "Needs you or stuck" }) },
  { name: "job", path: "/jobs?job=j_3", ready: (page: Page) => page.getByRole("heading", { name: "Migrate the settings page to the new form kit" }) },
  { name: "chat", path: "/chat", ready: (page: Page) => page.getByRole("list", { name: "Conversation" }) },
  { name: "inbox", path: "/inbox", ready: (page: Page) => page.getByRole("list", { name: "Needs you" }) },
  { name: "question", path: "/inbox?q=q_1", ready: (page: Page) => page.getByRole("region", { name: "Diff of internal/fleet/cache.go" }) },
  { name: "projects", path: "/projects", ready: (page: Page) => page.getByRole("heading", { name: "Projects" }).first() },
  { name: "fleet", path: "/agents", ready: (page: Page) => page.getByRole("list", { name: "Workspaces" }) },
  { name: "terminal", path: "/agents?session=true", ready: (page: Page) => page.getByRole("status").filter({ hasText: "Connected" }) },
  { name: "system", path: "/system", ready: (page: Page) => page.getByRole("heading", { name: "System" }).first() },
] as const;

/** Records, at the first animation frame and at DOMContentLoaded, the theme and mode on <html>. */
async function recordFirstFrames(page: Page) {
  await page.addInitScript(() => {
    const seen: string[] = [];
    (window as unknown as { themeSeen: string[] }).themeSeen = seen;
    const record = () => seen.push(`${document.documentElement.dataset.theme}/${document.documentElement.classList.contains("dark") ? "dark" : "light"}`);
    requestAnimationFrame(record);
    document.addEventListener("DOMContentLoaded", record);
  });
}

/**
 * Starts the test as a browser that never chose a theme, even when the suite runs under
 * E2E_THEME (which chooses one in every test's page): cleared once, on the first load only.
 */
async function freshBrowser(page: Page) {
  await page.addInitScript(() => {
    if (sessionStorage.getItem("theme-test-fresh")) return;
    sessionStorage.setItem("theme-test-fresh", "1");
    localStorage.removeItem("crowsnest-theme-name");
  });
}

async function firstFrames(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { themeSeen: string[] }).themeSeen);
}

test.describe("accessibility in every theme", () => {
  for (const theme of themes) {
    for (const mode of ["light", "dark"] as const) {
      test(`${theme} (${mode}): no serious or critical axe violations on any view`, async ({ page }, testInfo) => {
        test.skip(isPhone(testInfo), "checked once, on desktop: contrast does not depend on the width");
        test.slow();
        await page.addInitScript(([id, value]) => {
          localStorage.setItem("crowsnest-theme-name", id);
          localStorage.setItem("crowsnest-theme", value);
        }, [theme, mode] as const);
        for (const view of views) {
          await page.goto(view.path);
          await expect(view.ready(page).first()).toBeVisible({ timeout: 15_000 });
          await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
          await page.evaluate(() => document.fonts.ready);
          await expectAccessible(page, testInfo, `${theme}-${mode}-${view.name}`);
        }
      });
    }
  }

  test("the Appearance gallery: every theme in both modes, accessible", async ({ page }, testInfo) => {
    await page.goto("/");
    await page.getByRole("button", { name: "Account" }).click();
    await page.getByRole("menuitem", { name: "Appearance" }).click();
    const sheet = page.getByRole("dialog", { name: "Appearance" });
    await expect(sheet.getByRole("radio", { name: /theme$/ })).toHaveCount(themes.length);
    for (const theme of themes) await expect(sheet.locator(`[data-theme="${theme}"]`)).toHaveCount(2);
    await expectAccessible(page, testInfo, "appearance");
  });
});

test("picking a theme applies it at once, survives a reload with no flash, and the terminal follows", async ({ page }) => {
  await freshBrowser(page);
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/agents?session=true");
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible({ timeout: 15_000 });
  const html = page.locator("html");
  await expect(html).toHaveAttribute("data-theme", "crowsnest");
  const terminalBackground = () => page.locator(".xterm-scrollable-element").first().evaluate((element) => getComputedStyle(element).backgroundColor);
  const before = await terminalBackground();

  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Appearance" }).click();
  await page.getByRole("radio", { name: "Phosphor theme" }).click();
  await expect(html).toHaveAttribute("data-theme", "phosphor");
  await expect(page.getByRole("radio", { name: "Phosphor theme" })).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("Escape");
  await expect.poll(terminalBackground).not.toBe(before);
  // The phone's status bar and the installed app's title bar follow the chrome.
  const chrome = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--color-chrome").trim());
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute("content", chrome);

  await recordFirstFrames(page);
  await page.reload();
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible({ timeout: 15_000 });
  const seen = await firstFrames(page);
  expect(seen.length).toBeGreaterThan(0);
  expect(seen.every((frame) => frame === "phosphor/dark"), `phosphor and dark at every early frame: ${seen}`).toBe(true);

});

test("T moves on to the next theme, and the palette names one directly", async ({ page }, testInfo) => {
  test.skip(isPhone(testInfo), "keyboard shortcuts are a desktop path");
  await freshBrowser(page);
  const html = page.locator("html");
  await page.goto("/jobs");
  await expect(html).toHaveAttribute("data-theme", "crowsnest");
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
  await page.keyboard.press("Shift+T");
  await expect(html).toHaveAttribute("data-theme", "phosphor");
  await page.getByRole("button", { name: "Search or run…" }).click();
  await page.getByRole("combobox").fill("theme ember");
  await page.keyboard.press("Enter");
  await expect(html).toHaveAttribute("data-theme", "ember");
});

test.describe("the server's default theme", () => {
  test.use({ appOptions: { demo: true, args: ["--theme", "ember"] } });

  test("is on <html> from the first frame for a browser that never chose, and a choice overrides it", async ({ page }) => {
    await freshBrowser(page);
    await recordFirstFrames(page);
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/jobs");
    await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
    expect(await firstFrames(page)).toEqual(expect.arrayContaining(["ember/light"]));
    expect((await firstFrames(page)).every((frame) => frame === "ember/light")).toBe(true);
    await expect(page.locator("html")).toHaveAttribute("data-theme-default", "ember");

    await page.evaluate(() => localStorage.setItem("crowsnest-theme-name", "contrast"));
    await page.reload();
    await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
    expect((await firstFrames(page)).every((frame) => frame === "contrast/light")).toBe(true);
  });
});
