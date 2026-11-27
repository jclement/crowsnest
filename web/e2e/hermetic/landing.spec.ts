// Landing, throttling, the pause and the digest as a user drives them (DESIGN.md 6.11 to 6.13,
// 7.13), against `crowsnest --demo`. The landing demo (internal/jobs/demo_landing.go) is a real,
// throwaway git repository in the instance's temporary directory: j_18 is done and ready to land,
// j_19 is in review round 2 with round 1's findings, j_22 waits for a slot.
//
// Screenshots at 375 and 1440 px (Chromium) go to web/screenshots/landing-*.png, as the tray's do,
// and are committed after being looked at.
import type { Page, TestInfo } from "@playwright/test";
import { detailRegion, expect, expectNoSidewaysScroll, isPhone, test } from "../support/fixtures";

async function shot(page: Page, testInfo: TestInfo, name: string) {
  if (testInfo.project.name.includes("webkit")) return; // one set: Chromium at 375 and 1440
  await page.screenshot({ path: `screenshots/landing-${name}-${isPhone(testInfo) ? 375 : 1440}.png`, animations: "disabled", caret: "hide" });
}

async function openJob(page: Page, id: string) {
  await page.goto(`/?job=${id}`);
}

test("the review tab shows what the job produced, and Merge lands it after a confirmation", async ({ page }, testInfo) => {
  await openJob(page, "j_18");
  const detail = detailRegion(page, testInfo, "Selected job");
  await expect(detail.getByRole("heading", { name: "Retry widget fetches with back-off" })).toBeVisible();
  await expect(detail.getByText(/Finished in its worktree, review approved/)).toBeVisible();
  await detail.getByRole("button", { name: "Review and land" }).click();

  await expect(detail.getByText("1 commit ahead of main")).toBeVisible();
  const files = detail.getByRole("list", { name: "Changed files" });
  // Reading order: the walkthrough names retry.go first, then the test, then the docs.
  await expect(files.getByRole("button").nth(0)).toContainText("retry.go");
  await expect(files.getByRole("button").nth(1)).toContainText("retry_test.go");
  await files.getByRole("button", { name: /retry\.go/ }).first().click();
  await expect(detail.getByText("for attempt := 0; attempt < 4; attempt++ {")).toBeVisible();
  await expect(detail.getByRole("list", { name: "Review rounds" })).toContainText("Approved");
  await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "review");

  await detail.getByRole("button", { name: "Merge", exact: true }).click();
  const confirm = detail.getByRole("alertdialog", { name: "Confirm Merge" });
  await expect(confirm).toContainText("Merge job/91-retry-with-back-off into main with a merge commit");
  await shot(page, testInfo, "confirm");
  await confirm.getByRole("button", { name: "Yes, merge" }).click();
  await expect(page.getByText("Merge: done").first()).toBeVisible({ timeout: 20_000 });
  await expect(detail.getByText(/Merged: Merged into main/)).toBeVisible();
  await detail.getByRole("tab", { name: "Overview" }).click();
  await expect(detail.getByRole("list", { name: "Timeline" })).toContainText("Teardown finished");
});

test("a job in review shows the reviewer's findings round by round", async ({ page }, testInfo) => {
  await openJob(page, "j_19");
  const detail = detailRegion(page, testInfo, "Selected job");
  await expect(detail.getByText(/In review, round 2 of 2/)).toBeVisible();
  await detail.getByRole("button", { name: "Open the review" }).click();
  const rounds = detail.getByRole("list", { name: "Review rounds" });
  await expect(rounds).toContainText("Round 1");
  await expect(rounds).toContainText("Changes requested");
  await expect(rounds).toContainText("data race under load");
  await expect(rounds).toContainText("Reviewing");
  await shot(page, testInfo, "in-review");
});

test("a job waiting for a slot says why, and Start anyway starts it", async ({ page }, testInfo) => {
  await openJob(page, "j_22");
  const detail = detailRegion(page, testInfo, "Selected job");
  const banner = detail.getByRole("alert");
  await expect(banner).toContainText("Waiting to start");
  await expect(banner).toContainText("waiting for a slot: 4 of 4 running");
  await shot(page, testInfo, "queued");
  await banner.getByRole("button", { name: "Start anyway" }).click();
  await expect(page.getByText("Starting it now, past the limits")).toBeVisible();
  await expect(detail.getByRole("list", { name: "Timeline" })).toContainText("You started it anyway", { timeout: 15_000 });
});

test("pausing shows a banner everywhere until resumed", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Pause automation…" }).click();
  const sheet = page.getByRole("dialog", { name: "Pause automation" });
  await sheet.getByLabel(/Reason/).fill("deploy window");
  await sheet.getByRole("button", { name: "Pause", exact: true }).click();
  const banner = page.getByRole("status", { name: "Automation paused" });
  await expect(banner).toContainText("Automation paused by you");
  await expect(banner).toContainText("deploy window");
  await page.goto("/inbox");
  await expect(banner).toBeVisible();
  await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "paused");
  await banner.getByRole("button", { name: "Resume" }).click();
  await expect(banner).toBeHidden();
});

test("the daily digest: a switch and a time in Schedule, and past digests", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  await page.getByRole("button", { name: "Schedule", exact: true }).first().click();
  const schedule = page.getByRole("dialog", { name: "Schedule" });
  const digest = schedule.getByRole("switch", { name: "Daily digest" });
  await expect(digest).toHaveAttribute("aria-checked", "false");
  await digest.click();
  await expect(digest).toHaveAttribute("aria-checked", "true");
  await schedule.getByRole("button", { name: "Past digests" }).click();
  const list = page.getByRole("dialog", { name: "Digests" });
  await expect(list).toContainText("Digest for");
  await expect(list).toContainText("by crowsnest");
  await shot(page, testInfo, "digests");
});

test("the worktree sweep lists what is still around and discards after a confirmation", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  await page.getByRole("button", { name: /to land/ }).click();
  const sweep = page.getByRole("dialog", { name: "Worktrees still around" });
  await expect(sweep.getByRole("list", { name: "Finished jobs with worktrees" })).toContainText("Retry widget fetches with back-off");
  await shot(page, testInfo, "sweep");
  await sweep.getByLabel("Choose j_18").check();
  await sweep.getByRole("button", { name: /Discard 1 chosen/ }).click();
  const confirm = sweep.getByRole("alertdialog", { name: "Confirm discard" });
  await expect(confirm).toContainText("cannot be undone");
  await confirm.getByRole("button", { name: "Yes, discard them" }).click();
  await expect(page.getByText("1 discarded")).toBeVisible({ timeout: 20_000 });
});

test("throttling and review are Settings, and the System page shows the heavy slots", async ({ page }, testInfo) => {
  await page.goto("/system");
  await expect(page.getByRole("region", { name: "Heavy commands" })).toContainText("Heavy commands (2 slots");
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Settings" }).click();
  const settings = page.getByRole("dialog", { name: "Settings" });
  await expect(settings.getByText("Throttling and review")).toBeVisible();
  await expect(settings.getByText("Running jobs at once")).toBeVisible();
  await expect(settings.getByText("Daily digest at")).toBeVisible();
  await shot(page, testInfo, "settings");
});
