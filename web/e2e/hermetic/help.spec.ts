// The built-in manual (DESIGN.md 12.10) against `crowsnest --demo`, at 390x844 (Chromium and
// WebKit) and 1440: Help from the nav, a search, a deep link to a heading, an "open the thing"
// button, the palette answering a question with the manual, the contextual "?" on a stuck job
// (the section in a sheet over the job, and Escape closing only that sheet), a first-visit hint,
// tap targets, and axe on the Help view and the help sheet.
import type { Page, TestInfo } from "@playwright/test";
import { expect, expectAccessible, expectNoSidewaysScroll, isPhone, smallTapTargets, test } from "../support/fixtures";

async function sizeFor(page: Page, testInfo: TestInfo) {
  if (isPhone(testInfo)) await page.setViewportSize({ width: 390, height: 844 });
}

/** The Help view's search box: in the sidebar on a desktop, in the Contents sheet on a phone. */
async function searchBox(page: Page, testInfo: TestInfo) {
  if (isPhone(testInfo)) {
    await page.getByRole("button", { name: "Contents and search" }).click();
    return page.getByRole("dialog", { name: "Manual" }).getByRole("searchbox", { name: "Search the manual" });
  }
  return page.getByRole("complementary", { name: "Manual contents" }).getByRole("searchbox", { name: "Search the manual" });
}

test("Help from the nav: contents, search, a deep link and an open button", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const nav = page.getByRole("navigation", { name: "Views" }).filter({ visible: true });
  if (isPhone(testInfo)) {
    await nav.getByRole("button", { name: "More", exact: true }).click();
    await page.getByRole("dialog").getByRole("link", { name: /Help/ }).click();
  } else {
    await nav.getByRole("link", { name: /Help/ }).click();
  }
  await expect(page).toHaveURL(/\/help$/);
  await expect(page.getByRole("heading", { name: "The crowsnest manual", level: 1 })).toBeVisible();

  // Search across the manual, then follow the best match.
  const search = await searchBox(page, testInfo);
  await search.fill("how do I land a job");
  const results = page.getByRole("region", { name: "Search results" }).filter({ visible: true });
  await expect(results.getByRole("link").first()).toContainText("Review and land a job");
  if (isPhone(testInfo)) expect(await smallTapTargets(page, '[role="dialog"]')).toEqual([]);
  await results.getByRole("link").first().click();
  await expect(page).toHaveURL(/\/help\/how-to\/review-and-land/);
  await expect(page.getByRole("heading", { name: "Review and land a job", level: 1 })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Previous and next" })).toBeVisible();

  // A deep link lands on its heading.
  await page.goto("/help/concepts/supervisor#stuck-causes");
  await expect(page.getByRole("heading", { name: "Stuck causes" })).toBeInViewport();
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
  await expectAccessible(page, testInfo, "help");

  // "Open the thing": a button in the page goes to the real view.
  await page.goto("/help/how-to/give-a-job");
  await page.locator('[data-open-target="/jobs"]').first().click();
  await expect(page).toHaveURL(/\/jobs/);
});

test("the palette answers a question with the manual", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  await page.getByRole("button", { name: /Search or run/ }).click();
  const palette = page.getByRole("dialog");
  await palette.getByRole("combobox").fill("how do I land a job");
  const manual = palette.getByRole("group", { name: "Manual" });
  await expect(manual.getByRole("option").first()).toContainText(/land/i);
  // A question puts the manual first, so Enter opens the answer.
  await expect(palette.getByRole("group").first()).toHaveAccessibleName("Manual");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/help\/how-to\/review-and-land/);
  await expect(page.getByRole("heading", { name: "Review and land a job", level: 1 })).toBeVisible();
});

test("the ? on a stuck job opens its section over the job", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/jobs?job=j_6");
  const job = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("region", { name: "Selected job" });
  await expect(job.getByRole("heading", { name: "Profile the fleet cache" })).toBeVisible();
  await job.getByRole("button", { name: /^Help: why: Stopped three times/ }).click();
  const help = page.getByRole("dialog", { name: "Stuck causes" });
  await expect(help).toBeVisible();
  await expect(help.getByRole("table")).toContainText("Stopped three times without saying why");
  await expect(help.getByRole("link", { name: "Read the whole page" })).toBeVisible();
  await expect(page).toHaveURL(/\/jobs\?job=j_6/);
  await expectAccessible(page, testInfo, "help-sheet");
  // Escape closes the help sheet alone: the job (a sheet of its own on a phone) stays open.
  await page.keyboard.press("Escape");
  await expect(help).toBeHidden();
  await expect(page.getByRole("heading", { name: "Profile the fleet cache" })).toBeVisible();

  // The approval's risk level has its own "?" too.
  await page.goto("/decide?item=approval:ap_3");
  await expect(page.getByRole("heading", { name: /git push --force/, level: 1 })).toBeVisible();
});

test("a first-visit hint is dismissed for good", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const hint = page.getByRole("complementary", { name: "Hint" });
  await expect(hint).toContainText("New here?");
  await hint.getByRole("button", { name: "Read more" }).click();
  await expect(page.getByRole("dialog", { name: "Start here" })).toBeVisible();
  await page.getByRole("dialog", { name: "Start here" }).getByRole("button", { name: "Close" }).click();
  await hint.getByRole("button", { name: /Got it/ }).click();
  await expect(hint).toBeHidden();
  await page.reload();
  await expect(page.getByRole("heading", { name: "Now", level: 1 })).toBeVisible();
  await expect(page.getByRole("complementary", { name: "Hint" })).toHaveCount(0);
});
