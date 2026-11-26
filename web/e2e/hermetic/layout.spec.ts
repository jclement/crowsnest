// What every view owes a phone and every user (DESIGN.md 12, house style), against `crowsnest
// --demo`: at 375 px no sideways scroll, every tap target at least 44 px, and the bottom nav never
// covering the composer or the last row of a list; the theme toggle cycles system → light → dark
// and a reload never flashes the wrong theme; the offline banner when the server dies and its
// recovery when it returns; and axe finds no serious or critical violation on any view, in
// either theme.
import type { Page } from "@playwright/test";
import { expect, expectAccessible, expectNoSidewaysScroll, isPhone, smallTapTargets, test, TEST_LOGIN } from "../support/fixtures";

/** Every view, with something selected where the view has a detail, and what proves it loaded. */
const views = [
  { name: "jobs", path: "/", ready: (page: Page) => page.getByRole("region", { name: "Needs you or stuck" }) },
  { name: "job", path: "/?job=j_3", ready: (page: Page) => page.getByRole("heading", { name: "Migrate the settings page to the new form kit" }) },
  { name: "chat", path: "/chat", ready: (page: Page) => page.getByRole("list", { name: "Conversation" }) },
  { name: "inbox", path: "/inbox", ready: (page: Page) => page.getByRole("list", { name: "Needs you" }) },
  { name: "question", path: "/inbox?q=q_1", ready: (page: Page) => page.getByRole("region", { name: "Diff of internal/fleet/cache.go" }) },
  { name: "dialog", path: "/inbox?q=dialog%3Aw3%3Ap1", ready: (page: Page) => page.getByLabel("Dialog from docs-writer") },
  { name: "fleet", path: "/fleet", ready: (page: Page) => page.getByRole("list", { name: "Workspaces" }) },
  { name: "agent", path: "/fleet?agent=w2%3Ap1", ready: (page: Page) => page.getByRole("tab", { name: "Conversation" }) },
  { name: "terminal", path: "/terminal", ready: (page: Page) => page.getByRole("status").filter({ hasText: "Connected" }) },
] as const;

async function open(page: Page, path: string, ready: (page: Page) => ReturnType<Page["getByRole"]>) {
  await page.goto(path);
  await expect(ready(page).first()).toBeVisible({ timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
}

test.describe("at 375 px", () => {
  test.skip(({ isMobile }) => !isMobile, "the phone layout");

  for (const view of views) {
    test(`${view.name}: no sideways scroll and 44 px tap targets`, async ({ page }) => {
      await open(page, view.path, view.ready);
      await expectNoSidewaysScroll(page);
      expect(await smallTapTargets(page)).toEqual([]);
    });
  }

  for (const view of [views[0], views[3], views[6]]) {
    test(`${view.name}: the bottom nav never covers the last row`, async ({ page }) => {
      await open(page, view.path, view.ready);
      const nav = page.getByRole("navigation", { name: "Views" }).last();
      const navTop = (await nav.boundingBox())?.y ?? 0;
      // Scroll the page to its end, as a reader reaching the last row does.
      await page.locator("main").evaluate((main) => (main.scrollTop = main.scrollHeight));
      const last = page.locator("main li").last();
      await last.scrollIntoViewIfNeeded();
      const box = await last.boundingBox();
      expect(box, "a last row").not.toBeNull();
      expect((box?.y ?? 0) + (box?.height ?? 0)).toBeLessThanOrEqual(navTop + 0.5);
    });
  }

  test("chat: the bottom nav never covers the composer", async ({ page }) => {
    await open(page, "/chat", views[2].ready);
    const nav = page.getByRole("navigation", { name: "Views" }).last();
    const composer = page.getByRole("textbox", { name: "Message to the orchestrator" });
    for (const where of ["top", "bottom"] as const) {
      await page.locator("main").evaluate((main, end) => (main.scrollTop = end === "top" ? 0 : main.scrollHeight), where);
      const navBox = await nav.boundingBox();
      const box = await composer.boundingBox();
      expect(box && navBox && box.y + box.height <= navBox.y + 0.5, `composer clear of the nav at the ${where}`).toBe(true);
      await expect(composer).toBeInViewport();
    }
  });
});

test("the theme toggle cycles system → light → dark, and a reload never flashes", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  const html = page.locator("html");
  const toggle = page.getByRole("button", { name: /theme$/ });
  await expect(toggle).toHaveAccessibleName("System theme");
  await expect(html).toHaveClass(/\bdark\b/); // system, and the OS is dark
  await toggle.click();
  await expect(toggle).toHaveAccessibleName("Light theme");
  await expect(html).not.toHaveClass(/\bdark\b/);
  await toggle.click();
  await expect(toggle).toHaveAccessibleName("Dark theme");
  await expect(html).toHaveClass(/\bdark\b/);

  // Record the theme at the first paint of the reload, before React has run: it must already
  // be dark (public/theme-init.js runs blocking in <head>).
  await page.addInitScript(() => {
    const seen: boolean[] = [];
    (window as unknown as { themeSeen: boolean[] }).themeSeen = seen;
    const record = () => seen.push(document.documentElement.classList.contains("dark"));
    requestAnimationFrame(record);
    document.addEventListener("DOMContentLoaded", record);
  });
  await page.emulateMedia({ colorScheme: "light" });
  await page.reload();
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
  const seen = await page.evaluate(() => (window as unknown as { themeSeen: boolean[] }).themeSeen);
  expect(seen.length).toBeGreaterThan(0);
  expect(seen.every(Boolean), `dark at every early frame: ${seen}`).toBe(true);

  // Back to system: follows the OS, now light.
  await toggle.click();
  await expect(toggle).toHaveAccessibleName("System theme");
  await expect(html).not.toHaveClass(/\bdark\b/);
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(html).toHaveClass(/\bdark\b/);
});

test("the offline banner shows when the server dies, and goes when it returns", async ({ page, app }) => {
  test.slow();
  await page.goto("/");
  await expect(page.getByRole("status").filter({ hasText: "Live" })).toBeAttached();
  await app.kill();
  const banner = page.getByRole("status").filter({ hasText: /^Connection lost\./ });
  await expect(banner).toBeVisible({ timeout: 15_000 });
  await expect(banner.getByRole("button", { name: "Retry now" })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Offline" })).toBeAttached();
  await app.start();
  await banner.getByRole("button", { name: "Retry now" }).click().catch(() => {});
  await expect(banner).toHaveCount(0, { timeout: 40_000 });
  await expect(page.getByRole("status").filter({ hasText: "Live" })).toBeAttached();
  // And the data is live again: the restarted demo's board loads.
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
});

test.describe("accessibility", () => {
  for (const theme of ["light", "dark"] as const) {
    for (const view of views) {
      test(`${view.name} (${theme}): no serious or critical axe violations`, async ({ page }, testInfo) => {
        test.skip(theme === "dark" && isPhone(testInfo), "dark is checked once, on desktop: colour contrast does not depend on the width");
        await page.addInitScript((value) => localStorage.setItem("crowsnest-theme", value), theme);
        await open(page, view.path, view.ready);
        await expectAccessible(page, testInfo, `${view.name}-${theme}`);
      });
    }
  }
});

test.describe("the login page", () => {
  test.use({ appOptions: { demo: true, auth: TEST_LOGIN } });
  test("has no sideways scroll, 44 px targets on a phone, and no serious axe violations", async ({ page }, testInfo) => {
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    await expectNoSidewaysScroll(page);
    if (isPhone(testInfo)) expect(await smallTapTargets(page)).toEqual([]);
    await expectAccessible(page, testInfo, "login");
  });
});
