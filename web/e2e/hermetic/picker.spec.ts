// The folder picker (DESIGN.md 4.23) against `crowsnest --demo`, whose home for browsing is a
// small real tree in its throwaway state directory: ~/Developer/acme-api (a GitHub repo with a
// check task and AGENTS.md), ~/Developer/billing (Azure DevOps, CLAUDE.md), ~/Developer/scratch,
// ~/Work, a hidden folder and a file.
//
// Journeys at 1440 and 390 px: open New project, browse to the repo, read its facts, select it,
// find the form prefilled (name, check command, the remote's note) and create the project; the
// palette's "New project in…" with a pasted path, a refused one and an Azure DevOps repo; the job
// form's Browse. axe on the open picker; photographs into web/screenshots (picker-*.png).
import type { Page, TestInfo } from "@playwright/test";
import { expect, expectAccessible, expectNoSidewaysScroll, isPhone, smallTapTargets, test } from "../support/fixtures";

/** Photographs from the two Chromium projects only (WebKit would write the same files). */
async function shot(page: Page, testInfo: TestInfo, name: string) {
  if (testInfo.project.name === "mobile-webkit") return;
  const size = isPhone(testInfo) ? "390x844" : "1440x900";
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: `screenshots/picker-${name}-${size}.png`, animations: "disabled", caret: "hide" });
}

test.beforeEach(async ({ page }, testInfo) => {
  if (isPhone(testInfo)) await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => {
    if (!localStorage.getItem("crowsnest-theme")) localStorage.setItem("crowsnest-theme", "light");
  });
});

const picker = (page: Page) => page.getByRole("dialog", { name: "Choose the project's folder" });
const folders = (page: Page, label: string) => page.getByRole("list", { name: `Folders in ${label}` });

test("New project from the picker: browse to a repo, its facts, select it, the form is prefilled, create it", async ({ page }, testInfo) => {
  await page.goto("/projects");
  await page.getByRole("button", { name: "New project" }).first().click();
  const form = page.getByRole("form", { name: "New project" });
  await form.getByRole("button", { name: "Browse…" }).click();

  const dialog = picker(page);
  await expect(folders(page, "~")).toBeVisible();
  await expect(folders(page, "~").getByRole("button", { name: /^Developer/ })).toBeVisible();
  await expect(dialog.getByRole("navigation", { name: "Places" }).filter({ visible: true }).getByRole("button", { name: /~\/Work/ })).toBeVisible();
  // Home itself is refused as a project, and the picker says so before anything is chosen.
  await expect(dialog.getByText(/your home folder: too wide/)).toBeVisible();
  await shot(page, testInfo, "home");

  await dialog.getByRole("button", { name: "Open Developer" }).click();
  const developer = folders(page, "~/Developer");
  await expect(developer).toBeVisible();
  // The repositories float to the top, badged.
  await expect(developer.getByRole("button").first()).toHaveText(/^acme-apigit/);
  await developer.getByRole("button", { name: /^acme-api/ }).click();
  await expect(dialog.getByText("git repo on GitHub, branch main, has a check task, AGENTS.md present")).toBeVisible();
  await expect(dialog.getByText("GitHub: Land can open pull requests here.")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Select this folder" })).toBeInViewport();
  await expectAccessible(page, testInfo, "the folder picker");
  if (isPhone(testInfo)) {
    await expectNoSidewaysScroll(page);
    expect(await smallTapTargets(page, '[role="dialog"]:last-of-type')).toEqual([]);
  }
  await shot(page, testInfo, "repo");

  await dialog.getByRole("button", { name: "Select this folder" }).click();
  await expect(dialog).toBeHidden();
  await expect(form.getByLabel(/Directory/)).toHaveValue(/\/demo-home\/Developer\/acme-api$/);
  await expect(form.getByLabel("Name", { exact: true })).toHaveValue("Acme api");
  await expect(form.getByLabel(/Check command/)).toHaveValue("mise run check");
  await expect(form.getByText("GitHub: Land can open pull requests here.")).toBeVisible();
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "prefilled");

  await form.getByRole("button", { name: "Create project" }).click();
  await expect(page).toHaveURL(/\/projects\/acme-api$/);
  await expect(page.getByRole("article", { name: "Acme api" })).toBeVisible();
});

test("the palette's New project in…: a pasted path, a refused one, and an Azure DevOps repo", async ({ page }, testInfo) => {
  await page.goto("/projects");
  await page.getByRole("button", { name: "Search or run…" }).click();
  await page.getByRole("combobox", { name: "Search or run a command" }).fill("New project in");
  await page.keyboard.press("Enter");

  const dialog = picker(page);
  await expect(folders(page, "~")).toBeVisible();
  const box = dialog.getByLabel("Filter folders, or paste a path");
  await expect(box).toBeFocused();
  await box.fill("/etc");
  await box.press("Enter");
  await expect(dialog.getByRole("alert")).toContainText("outside what the picker may browse");
  await expect(folders(page, "~")).toBeVisible();

  await box.fill("~/Developer/billing");
  await box.press("Enter");
  await expect(folders(page, "~/Developer/billing")).toBeAttached(); // no sub-folders: an empty list
  await expect(dialog.getByText(/No folders here/)).toBeVisible();
  await expect(dialog.getByText("git repo on Azure DevOps, branch develop, CLAUDE.md present")).toBeVisible();
  await box.press("Control+Enter");
  await expect(dialog).toBeHidden();
  const form = page.getByRole("form", { name: "New project" });
  await expect(form.getByLabel("Name", { exact: true })).toHaveValue("Billing");
  await expect(form.getByText("Azure DevOps: Land will not open pull requests here; it merges locally.")).toBeVisible();
  // Escape closes the picker alone; the form stays.
  await form.getByRole("button", { name: "Browse…" }).click();
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(form).toBeVisible();
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
});

test("the job form's where: Browse picks the folder, a folder that is not a repo is flagged", async ({ page }, testInfo) => {
  await page.goto("/jobs?new=1"); // the board's New job sheet: the same form as Now's job box
  await page.getByRole("button", { name: /To the orchestrator/ }).click();
  await page.getByRole("button", { name: "Browse for a folder" }).click();
  const dialog = page.getByRole("dialog", { name: "Where should the job run?" });
  await expect(folders(page, "~")).toBeVisible();
  const box = dialog.getByLabel("Filter folders, or paste a path");
  await box.fill("~/Developer");
  await box.press("Enter");
  await expect(folders(page, "~/Developer")).toBeVisible();
  await box.fill("scr");
  await box.press("ArrowDown");
  await expect(dialog.getByText("Not a git repository: jobs here cannot be isolated in worktrees.")).toBeVisible();
  await dialog.getByRole("button", { name: "Select this folder" }).click();
  await expect(page.getByLabel(/^Where/)).toHaveValue(/\/demo-home\/Developer\/scratch$/);
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
});
