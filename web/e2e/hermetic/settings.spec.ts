// The Settings view (R8: one door to configuration) against `crowsnest --demo`: the account menu
// is System, Settings, the Pause and Do not disturb switches; the view has its four groups with
// every section; each old door (the palette's Settings, Appearance, Notifications, Instructions and
// Limits, the manual's app:sheet buttons, the Jobs board's limits button) lands on its section;
// Do not disturb turns on with a time and off with one tap; no sideways scroll; axe.
import type { Page } from "@playwright/test";
import { expect, expectAccessible, expectNoSidewaysScroll, isPhone, test } from "../support/fixtures";

const sections = [
  "Appearance",
  "Notifications",
  "Voice",
  "Account and name",
  "Limits",
  "Budgets, upkeep and notices",
  "Backups and storage",
  "Tailnet",
  "System",
  "Agent settings",
  "Instructions and roles",
  "Schedules",
  "Approval rules",
  "Protected hosts and branches",
  "Pause and do not disturb",
];

async function palette(page: Page, query: string, option: RegExp) {
  await page.getByRole("button", { name: "Search or run…" }).click();
  await page.getByRole("combobox", { name: "Search or run a command" }).fill(query);
  // Visible only: the Settings view's phone section picker has options of its own.
  await page.getByRole("option", { name: option }).filter({ visible: true }).first().click();
}

test("the account menu is System, Settings and two switches; Settings has every section", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Account" }).click();
  const menu = page.getByRole("menu");
  await expect(menu.getByRole("menuitem")).toHaveText([/System/, /Settings/]);
  await expect(menu.getByRole("menuitemcheckbox")).toHaveText([/Pause automation/, /Do not disturb/]);
  await menu.getByRole("menuitem", { name: "Settings" }).click();
  await expect(page).toHaveURL(/\/settings$/);
  for (const group of ["This device", "This instance", "Agents", "Safety"]) await expect(page.getByRole("heading", { name: group, level: 2 })).toBeVisible();
  for (const name of sections) await expect(page.getByRole("region", { name, exact: true })).toBeAttached();
  await expect(page.getByRole("region", { name: "Agent settings" }).getByLabel("Job model")).toBeVisible();
  await expect(page.getByRole("region", { name: "Protected hosts and branches" }).getByText("Protected branches", { exact: true })).toBeVisible();
  await expectNoSidewaysScroll(page);
  await expectAccessible(page, testInfo, "settings");
  await page.screenshot({ path: `screenshots/settings-view-${isPhone(testInfo) ? 375 : 1440}.png`, animations: "disabled", caret: "hide" });
});

test("every old door lands on its section", async ({ page }) => {
  await page.goto("/");
  // The palette's Settings is the view itself now (R10 F-F8: g s, More and the rail too).
  await palette(page, "settings", /^Settings$/);
  await expect(page).toHaveURL(/\/settings$/);
  for (const [query, option, hash] of [
    ["appearance", /^Appearance/, "appearance"],
    ["notifications", /^Notifications/, "notifications"],
    ["instructions", /^Instructions/, "instructions"],
    ["limits", /^Limits/, "limits"],
  ] as const) {
    await palette(page, query, option);
    await expect(page, query).toHaveURL(new RegExp(`/settings#${hash}$`));
    await expect(page.locator(`#settings-${hash}`)).toBeInViewport();
  }
  // The board's running count opens Limits.
  await page.goto("/jobs");
  await page.getByRole("button", { name: /running/ }).first().click();
  await expect(page).toHaveURL(/\/settings#limits$/);
  await expect(page.getByRole("region", { name: "Limits" }).getByText("Running jobs at once")).toBeVisible();
  // A manual "open the thing" button for a sheet that moved.
  await page.goto("/help/safety");
  await page.getByRole("link", { name: /Open Settings → Protected hosts/ }).click();
  await expect(page).toHaveURL(/\/settings#guard$/);
  await expect(page.locator("#settings-guard")).toBeInViewport();
});

test("Do not disturb is a switch: on with a time, off with one tap", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Account" }).click();
  const dnd = page.getByRole("menuitemcheckbox", { name: "Do not disturb" });
  await expect(dnd).toHaveAttribute("aria-checked", "false");
  await dnd.click();
  const sheet = page.getByRole("dialog", { name: "Presence and do not disturb" });
  await expect(sheet).toBeVisible();
  const until = await page.evaluate(() => {
    const at = new Date(Date.now() + 2 * 3600_000);
    return `${String(at.getHours()).padStart(2, "0")}:${String(at.getMinutes()).padStart(2, "0")}`;
  });
  await sheet.getByLabel(/until/i).fill(until);
  await sheet.getByRole("button", { name: "Hold notifications" }).click();
  await expect(page.getByText("Do not disturb is on")).toBeVisible();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Account" }).click();
  await expect(page.getByRole("menuitemcheckbox", { name: "Do not disturb" })).toHaveAttribute("aria-checked", "true");
  await page.getByRole("menuitemcheckbox", { name: "Do not disturb" }).click();
  await expect(page.getByText("Do not disturb is off")).toBeVisible();
});

// R10 F-B1 / O-B5: a section link scrolled the document as well as the view, so the header
// (search, account) was cut off and a blank band showed under the tab bar.
test("a section link scrolls the view, never the app's frame", async ({ page }) => {
  await page.goto("/settings#tailnet");
  await expect(page.locator("#settings-tailnet")).toBeInViewport();
  await page.waitForTimeout(1300); // the view scrolls again as the sections above it load
  expect(await page.evaluate(() => document.scrollingElement?.scrollTop)).toBe(0);
  expect(await page.locator("header").first().evaluate((header) => header.getBoundingClientRect().top)).toBe(0);
  await expect(page.locator("#settings-tailnet")).toBeInViewport();
});

test("Settings is reachable from More or the rail and g s; Voice says how to turn it on", async ({ page }, testInfo) => {
  await page.goto("/");
  if (isPhone(testInfo)) {
    await page.getByRole("button", { name: "More", exact: true }).click();
    await page.getByRole("dialog", { name: "More" }).getByRole("link", { name: "Settings" }).click();
  } else {
    await page.getByRole("navigation", { name: "Views" }).getByRole("link", { name: "Settings" }).click();
  }
  await expect(page).toHaveURL(/\/settings$/);
  if (!isPhone(testInfo)) {
    await page.goto("/");
    await page.keyboard.press("g");
    await page.keyboard.press("s");
    await expect(page).toHaveURL(/\/settings$/);
  }
  // The demo runs without an ElevenLabs key: the section is there and says how to turn voice on.
  await page.goto("/settings#voice");
  const voice = page.getByRole("region", { name: "Voice", exact: true });
  await expect(voice).toBeInViewport();
  await expect(voice.getByText(/Talk mode is off/)).toBeVisible();
  if (isPhone(testInfo)) {
    await page.getByLabel("Go to a section").selectOption("tailnet");
    await expect(page).toHaveURL(/\/settings#tailnet$/);
    await expect(page.locator("#settings-tailnet")).toBeInViewport();
  }
  // A switch says what it is set to, not only where the value came from (R10 O-F11).
  await expect(page.getByRole("region", { name: "Tailnet" }).getByText("Off (default)")).toBeVisible();
});
