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
