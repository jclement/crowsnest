// Projects, leads, instruction layers and app schedules (DESIGN.md 21) against `crowsnest --demo`,
// which seeds five projects (crowsnest, website and mobile with their leads running and answering
// with canned replies, infra and handbook with their leads off), jobs filed under them, the
// schedules evening-review (to the crowsnest lead) and link-check (a job for website), and edited
// instruction layers.
//
// Journeys, at 1440 and 375 px: create a project from a directory the test makes; start a lead
// and give it a job "to the lead", held by lead-<slug>; chat with a lead and get its reply;
// filter the board by project (remembered across a reload); edit a role's instructions and see
// the diff against the built-in; make a schedule and Run it now. The desktop and phone Chromium
// runs also photograph the views into web/screenshots (projects-*.png), light and dark.
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Page, TestInfo } from "@playwright/test";
import { expect, expectNoSidewaysScroll, isPhone, test } from "../support/fixtures";

/** Photographs from the two Chromium projects only (WebKit would write the same files). */
async function shot(page: Page, testInfo: TestInfo, name: string, dark = false) {
  if (testInfo.project.name === "mobile-webkit") return;
  const size = isPhone(testInfo) ? "375x812" : "1440x900";
  if (dark) {
    await page.evaluate(() => {
      localStorage.setItem("crowsnest-theme", "dark");
      document.documentElement.classList.add("dark");
    });
  }
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: `screenshots/projects-${name}-${size}${dark ? "-dark" : ""}.png`, animations: "disabled", caret: "hide" });
  if (dark) {
    await page.evaluate(() => {
      localStorage.setItem("crowsnest-theme", "light");
      document.documentElement.classList.remove("dark");
    });
  }
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    if (!localStorage.getItem("crowsnest-theme")) localStorage.setItem("crowsnest-theme", "light");
  });
});

test("the projects list shows each lead's state and counts; a project page has its lead, jobs, schedules and instructions", async ({ page }, testInfo) => {
  await page.goto("/projects");
  const list = page.getByRole("list", { name: "Projects" });
  await expect(list.getByRole("link", { name: /Crowsnest/ })).toBeVisible();
  await expect(list.getByRole("link")).toHaveCount(5);
  await expect(list.getByRole("link", { name: /Infra/ })).toContainText("Lead off");
  await expect(list.getByRole("link", { name: /Handbook/ })).toContainText("Lead off");
  await expect(list.getByRole("link", { name: /Website/ })).not.toContainText("Lead off");
  await expect(list.getByRole("link", { name: /Mobile/ })).not.toContainText("Lead off");
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "list");

  await list.getByRole("link", { name: /Crowsnest/ }).click();
  await expect(page).toHaveURL(/\/projects\/crowsnest$/);
  const project = page.getByRole("article", { name: "Crowsnest" });
  await expect(project.getByTestId("lead-state")).not.toHaveText("Lead off");
  await expect(project.getByRole("region", { name: "Project schedules" })).toContainText("evening-review");
  await expect(project.getByRole("region", { name: "Project instructions" }).getByRole("textbox")).toHaveValue(/mise run check/);
  await expect(project.getByRole("region", { name: "Instructions" }).getByRole("list", { name: "Layers at start" })).toBeVisible();
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "detail");
  await shot(page, testInfo, "detail", true);
});

test("create a project from a directory: the name follows the directory, and the project page opens", async ({ page }, testInfo) => {
  const dir = mkdtempSync(join(tmpdir(), "cn-e2e-garden-"));
  try {
    await page.goto("/projects");
    await page.getByRole("button", { name: "New project" }).first().click();
    const form = page.getByRole("form", { name: "New project" });
    await form.getByLabel(/Directory/).fill(dir);
    await expect(form.getByLabel("Name", { exact: true })).toHaveValue(/^Cn e2e garden/);
    await form.getByLabel("Name", { exact: true }).fill("Garden");
    await form.getByRole("button", { name: /Defaults for its jobs/ }).click();
    await form.getByLabel("Start the lead").selectOption("manual");
    if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
    await shot(page, testInfo, "new");
    await form.getByRole("button", { name: "Create project" }).click();
    await expect(page).toHaveURL(/\/projects\/garden$/);
    const project = page.getByRole("article", { name: "Garden" });
    await expect(project.getByTestId("lead-state")).toHaveText("Lead off");
    await expect(project).toContainText(dir.split("/").pop() ?? "");

    // The server's refusal comes back in words: a second project on the same directory overlaps.
    await page.goto("/projects");
    await page.getByRole("button", { name: "New project" }).first().click();
    await page.getByRole("form", { name: "New project" }).getByLabel(/Directory/).fill(dir);
    await page.getByRole("form", { name: "New project" }).getByLabel("Name", { exact: true }).fill("Garden again");
    await page.getByRole("button", { name: "Create project" }).click();
    await expect(page.getByRole("form", { name: "New project" }).getByRole("alert")).toBeVisible();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("start a lead and give it a job to the lead: filed in the project, for its lead, with no directory", async ({ page }) => {
  await page.goto("/projects/infra");
  const project = page.getByRole("article", { name: "Infra" });
  await expect(project.getByTestId("lead-state")).toHaveText("Lead off");
  await project.getByRole("button", { name: "Start lead" }).click();
  await expect(project.getByTestId("lead-state")).not.toHaveText("Lead off", { timeout: 20_000 });

  await page.goto("/?project=infra");
  await expect(page.getByRole("radio", { name: /^Infra/ })).toHaveAttribute("aria-checked", "true");
  // The board has no composer; New job opens the form, given to the board's project.
  await page.getByRole("button", { name: "New job" }).click();
  await expect(page.getByRole("dialog", { name: "New job for Infra" })).toBeVisible();
  await expect(page.getByRole("radio", { name: "To the lead" })).toHaveAttribute("aria-checked", "true");
  await page.getByLabel("What needs doing?").fill("Rotate the devbox image's base to the new release");
  await page.getByRole("button", { name: "Give job" }).click();
  await expect(page.getByText("Given to Infra's lead")).toBeVisible();
  const card = page.getByRole("button", { name: /Rotate the devbox image/ }).first();
  await expect(card).toBeVisible();
  const job = await page.evaluate(() =>
    fetch("/api/v1/jobs")
      .then((r) => r.json())
      .then((body: { data: Array<{ title: string; project_id: number; dir: string; role: string; agent_name: string }> }) => body.data.find((j) => j.title.startsWith("Rotate the devbox"))),
  );
  const infra = await page.evaluate(() => fetch("/api/v1/projects/infra").then((r) => r.json()).then((body: { data: { id: number } }) => body.data.id));
  expect(job).toMatchObject({ project_id: infra, dir: "", role: "lead" });
  // The lead takes it once it is ready: a lead's status turning idle dispatches its waiting job.
  await expect
    .poll(
      () =>
        page.evaluate(() =>
          fetch("/api/v1/jobs")
            .then((r) => r.json())
            .then((body: { data: Array<{ title: string; agent_name: string }> }) => body.data.find((j) => j.title.startsWith("Rotate the devbox"))?.agent_name ?? ""),
        ),
      { timeout: 20_000 },
    )
    .toBe("lead-infra");
});

test("chat with a project's lead: the target is in the URL and the lead replies", async ({ page }, testInfo) => {
  await page.goto("/chat");
  await page.getByTestId("chat-target").selectOption("lead-crowsnest");
  await expect(page).toHaveURL(/target=lead-crowsnest/);
  const box = page.getByRole("textbox", { name: "Message to Crowsnest's lead" });
  await expect(box).toBeVisible({ timeout: 15_000 });
  const replies = page.getByRole("article", { name: "Reply" });
  const before = await replies.count();
  await box.fill("What is open in this project?");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByRole("list", { name: "Conversation" }).getByText("What is open in this project?", { exact: true })).toBeVisible();
  // More than before, not exactly one more: the demo lead can still be finishing an earlier
  // scripted reply when `before` is counted, and under a loaded machine that late reply made an
  // exact count fail about once in a few hundred runs. What matters is that the message was answered.
  await expect.poll(() => replies.count(), { timeout: 30_000 }).toBeGreaterThan(before);
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "chat-lead");

  // The menu acts on the lead: no compaction, but its instructions.
  await page.getByRole("button", { name: "Conversation menu" }).click();
  await expect(page.getByRole("menuitem", { name: "Compact now" })).toHaveCount(0);
  await page.getByRole("menuitem", { name: "Instructions" }).click();
  await expect(page.getByRole("dialog").getByRole("list", { name: "Layers at start" })).toBeVisible();
});

test("filter the board by project; the choice is remembered", async ({ page }) => {
  await page.goto("/jobs");
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
  await page.getByRole("radio", { name: /^Website/ }).click();
  await expect(page).toHaveURL(/project=website/);
  const cards = page.locator('ul > li > button[class*="min-h-14"]');
  await expect(cards.first()).toBeVisible();
  const allCount = await page.evaluate(() => fetch("/api/v1/jobs").then((r) => r.json()).then((body: { data: unknown[] }) => body.data.length));
  expect(await cards.count()).toBeLessThan(allCount);
  await page.goto("/jobs");
  await expect(page.getByRole("radio", { name: /^Website/ })).toHaveAttribute("aria-checked", "true");
  await page.getByRole("radio", { name: /^All/ }).click();
  await expect(page.getByRole("button", { name: /Crowsnest/ }).first()).toBeVisible();
});

test("edit a role's instructions and see the diff against the built-in", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Instructions" }).click();
  const sheet = page.getByRole("dialog", { name: "Instructions" });
  await sheet.getByRole("region", { name: "Roles" }).getByRole("button", { name: /tester/ }).click();
  const editor = sheet.getByRole("textbox", { name: /tester instructions/ });
  await expect(editor).not.toHaveValue("");
  await expect(sheet.getByTestId("layer-source")).toHaveText("built-in");
  await editor.fill(`${await editor.inputValue()}\n\nAlways attach the failing test's output.`);
  await sheet.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("Instructions saved")).toBeVisible();
  await expect(sheet.getByTestId("layer-source")).toHaveText("edited");
  await sheet.getByRole("tab", { name: "Diff vs built-in" }).click();
  await expect(sheet.getByText("Always attach the failing test's output.")).toBeVisible();
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "instructions");
  await sheet.getByRole("tab", { name: /History/ }).click();
  await expect(sheet.getByRole("list", { name: "Revisions" }).getByRole("listitem")).toHaveCount(1);
});

test("make a schedule in the app and Run it now", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  await page.getByRole("button", { name: "Schedule", exact: true }).click();
  const sheet = page.getByRole("dialog", { name: "Schedule" });
  await expect(sheet.getByText("link-check").locator("visible=true").first()).toBeVisible();
  await sheet.getByRole("button", { name: "New schedule" }).click();
  const form = sheet.getByRole("form", { name: "New schedule" });
  await form.getByLabel("Name", { exact: true }).fill("weekly-deps");
  await form.getByLabel("Cadence").selectOption("days");
  await form.getByLabel("Days").fill("7");
  await form.getByRole("radio", { name: "A job" }).check();
  await form.getByLabel("Project", { exact: true }).selectOption({ label: "Website" });
  await form.getByRole("textbox", { name: /brief/ }).fill("# Check the dependencies\n\nList outdated packages.");
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "schedule-new");
  await form.getByRole("button", { name: "Create schedule" }).click();
  await expect(page.getByText("Schedule weekly-deps made")).toBeVisible();
  const runNow = sheet.getByRole("button", { name: "Run weekly-deps now" }).locator("visible=true");
  await runNow.click();
  await expect(page.getByText("weekly-deps: job started")).toBeVisible();
  await expect(sheet.getByText(/every 7d/).locator("visible=true").first()).toBeVisible();
  await shot(page, testInfo, "schedule");
});
