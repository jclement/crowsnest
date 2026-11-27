// The built-in manual's pictures (docs/manual/img), photographed from `crowsnest --demo` in the
// light theme at 1x, so they stay small and regenerate with one command: `mise run
// screenshots:manual`. Hints are marked seen first, so the pictures show the views as they are
// after a first visit. src/manual/manual.test.ts checks every picture a page names exists, every
// file here is used, and each is under 250 KB.
import { type Page } from "@playwright/test";
import { expect, test } from "../support/fixtures";

// JPEG at quality 80: a 1440 px view as PNG is 200 to 300 KB; as JPEG it is 50 to 210 KB.
const out = (name: string) => `../docs/manual/img/${name}.jpg`;

async function open(page: Page, path: string, width: number, height: number) {
  await page.setViewportSize({ width, height });
  await page.addInitScript(() => {
    localStorage.setItem("crowsnest-theme", "light");
    for (const id of ["now", "jobs", "projects"]) localStorage.setItem(`crowsnest-hint:${id}`, "1");
  });
  await page.goto(path);
  await page.evaluate(() => document.fonts.ready);
}

const shot = (page: Page, name: string) => page.screenshot({ path: out(name), type: "jpeg", quality: 80, animations: "disabled", caret: "hide" });

test("now at 1440", async ({ page }) => {
  await open(page, "/", 1440, 900);
  await expect(page.getByRole("region", { name: "Needs you" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Projects" })).toBeVisible();
  await shot(page, "now-1440");
});

test("now at 390", async ({ page }) => {
  await open(page, "/", 390, 844);
  await expect(page.getByRole("region", { name: "Needs you" })).toBeVisible();
  await shot(page, "now-390");
});

test("jobs board at 1440", async ({ page }) => {
  await open(page, "/jobs", 1440, 900);
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
  await shot(page, "jobs-1440");
});

test("a project at 1440", async ({ page }) => {
  await open(page, "/projects/crowsnest", 1440, 900);
  await expect(page.getByRole("heading", { name: "Crowsnest", level: 2 })).toBeVisible();
  await shot(page, "project-1440");
});

test("a decision on a phone", async ({ page }) => {
  await open(page, "/decide?item=approval:ap_3", 390, 844);
  await expect(page.getByRole("region", { name: "Answer" })).toBeVisible();
  await shot(page, "decide-390");
});

test("rescue on a phone", async ({ page }) => {
  await open(page, "/jobs?job=j_6", 390, 844);
  const rescue = page.getByRole("dialog").getByRole("region", { name: "Rescue" });
  await expect(rescue).toBeVisible();
  await shot(page, "rescue-390");
});

test("the land tab at 1440", async ({ page }) => {
  await open(page, "/jobs?job=j_18", 1440, 900);
  await page.getByRole("tab", { name: "Land" }).click();
  await expect(page.getByRole("group", { name: "Land actions" })).toBeVisible();
  await shot(page, "land-1440");
});

test("the schedule sheet at 1440", async ({ page }) => {
  await open(page, "/jobs", 1440, 900);
  await page.getByRole("button", { name: "Schedule", exact: true }).click();
  await expect(page.getByRole("table", { name: "Scheduled tasks" })).toBeVisible();
  await shot(page, "schedule-1440");
});

test("chat at 1440", async ({ page }) => {
  await open(page, "/chat", 1440, 900);
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await shot(page, "chat-1440");
});

// The folder picker (New project → Browse…), with the names the manual quotes asserted, so the
// how-to page cannot describe a button that is not there.
test("the folder picker at 1440", async ({ page }) => {
  await open(page, "/projects", 1440, 900);
  await page.getByRole("button", { name: "New project" }).first().click();
  const form = page.getByRole("form", { name: "New project" });
  await form.getByRole("button", { name: "Browse…" }).click();
  const dialog = page.getByRole("dialog", { name: "Choose the project's folder" });
  await expect(dialog.getByRole("navigation", { name: "Places" }).filter({ visible: true })).toBeVisible();
  await expect(dialog.getByPlaceholder("Filter, or paste a path (/… or ~/…)")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Hidden", exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Files", exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "Open Developer" }).click();
  await dialog.getByRole("button", { name: /^acme-api/ }).click();
  await expect(dialog.getByText("GitHub: Land can open pull requests here.")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Select this folder" })).toBeVisible();
  await shot(page, "picker-1440");
  await dialog.getByRole("button", { name: "Select this folder" }).click();
  await expect(form.getByLabel(/Check command/)).toHaveValue("mise run check");
});

// The diff viewer on a running worktree job (the demo's j_24, Website), with the labels the manual
// quotes asserted, then its full-screen route.
test("the diff viewer at 1440", async ({ page }) => {
  await open(page, "/jobs?job=j_24", 1440, 900);
  const detail = page.getByRole("region", { name: "Selected job" });
  await detail.getByRole("tab", { name: "Review" }).click();
  const viewer = detail.getByRole("region", { name: /^Changes: / });
  await expect(viewer.getByText("Hide viewed")).toBeVisible();
  const scopes = viewer.getByRole("radiogroup", { name: "Which changes" });
  for (const scope of ["All changes", "Commits on this branch", "Uncommitted", "Since you viewed"]) await expect(scopes.getByRole("radio", { name: new RegExp(`^${scope} \\d+$`) })).toBeVisible();
  await expect(viewer.getByRole("button", { name: "Hide whitespace" })).toBeVisible();
  await expect(viewer.getByRole("button", { name: "Full screen (f)" })).toBeVisible();
  await expect(viewer.getByRole("button", { name: "Open in a new tab" })).toBeVisible();
  await expect(viewer.getByRole("button", { name: "Refresh" })).toBeVisible();
  await page.goto("/diff?job=j_24&file=web/src/cart.ts");
  const full = page.getByRole("region", { name: /^Changes: / });
  await expect(full.getByRole("table", { name: "Diff of web/src/cart.ts" })).toBeVisible({ timeout: 20_000 });
  await expect(full.getByRole("checkbox", { name: "Viewed: web/src/cart.ts" }).first()).toBeVisible();
  await expect(full.getByRole("button", { name: "Leave full screen (Esc)" })).toBeVisible();
  await expect(full.getByText(/\d+ of \d+ viewed/).first()).toBeVisible();
  await expect(full.getByRole("button", { name: "Split view" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Views" })).toHaveCount(0);
  await shot(page, "diff-1440");
});

// Settings → Appearance, with the labels the manual quotes asserted.
test("the appearance section at 1440", async ({ page }) => {
  await open(page, "/settings#appearance", 1440, 900);
  const sheet = page.getByRole("region", { name: "Appearance" });
  for (const mode of ["System", "Light", "Dark"]) await expect(sheet.getByRole("radio", { name: mode, exact: true })).toBeVisible();
  for (const theme of ["Crowsnest", "Phosphor", "Deep sea", "Ember", "High contrast"]) await expect(sheet.getByRole("radio", { name: `${theme} theme` })).toBeAttached();
  await expect(sheet.getByRole("button", { name: /Use for new browsers/ }).first()).toBeAttached();
  await shot(page, "appearance-1440");
});
