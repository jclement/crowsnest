// Acceptance, budgets, wake-ups, the fleet's usage limit and answer receipt as a user sees them
// (DESIGN.md 6.15 to 6.19), against `crowsnest --demo`. The demo (internal/jobs/demo.go,
// demo_accept.go): j_11 is accepted (a passing check pinned to its worktree's HEAD), j_16 failed
// its check three rounds and needs you, j_8 is finished but unverified, j_2 is at 84% of its
// budget, j_7 is blocked with a recheck time, j_6 is stuck with its last words; with
// CROWSNEST_DEMO_LIMIT=1 the fleet is under claude's usage limit and j_3 is held by it.
//
// Screenshots at 375 and 1440 px (Chromium) go to web/screenshots/accept-*.png.
import type { Page, TestInfo } from "@playwright/test";
import { detailRegion, expect, expectNoSidewaysScroll, isPhone, test } from "../support/fixtures";

async function shot(page: Page, testInfo: TestInfo, name: string) {
  if (testInfo.project.name.includes("webkit")) return;
  await page.screenshot({ path: `screenshots/accept-${name}-${isPhone(testInfo) ? 375 : 1440}.png`, animations: "disabled", caret: "hide" });
}

function card(page: Page, title: string) {
  return page.getByRole("listitem").filter({ has: page.getByText(title, { exact: true }) });
}

test("the board tells accepted, unverified and failed-check apart, and shows waits and last words", async ({ page }, testInfo) => {
  await page.goto("/?project=all");
  await expect(card(page, "Retry widget fetches with back-off")).toContainText("Accepted");
  await expect(card(page, "Render widgets at three sizes")).toContainText("Check failed");
  await expect(card(page, "Render widgets at three sizes")).toContainText("Should I disable that lint rule?");
  await expect(card(page, "Upgrade the Postgres driver")).toContainText(/rechecks at \d\d:\d\d/);
  await expect(card(page, "Profile the fleet cache")).toContainText("not sure how to proceed");
  await expect(card(page, "Tidy the README install section")).toContainText(/Budget \d+%/);
  await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "board");
  // A finished job with no check command is done but unverified (it may sit in "Older").
  await page.goto("/?job=j_8");
  const detail = detailRegion(page, testInfo, "Selected job");
  await expect(detail.getByRole("heading", { name: "Fix the flaky SSE reconnect test" })).toBeVisible();
  await expect(detail.getByText("Unverified", { exact: true }).first()).toBeVisible();
  await expect(detail.getByRole("heading", { name: "Verified (reported by the agent)", exact: true })).toBeVisible();
});

test("a failed check shows crowsnest's evidence and runs it again", async ({ page }, testInfo) => {
  await page.goto("/?job=j_16");
  const detail = detailRegion(page, testInfo, "Selected job");
  await expect(detail.getByRole("heading", { name: "Render widgets at three sizes" })).toBeVisible();
  const evidence = detail.getByRole("region", { name: "crowsnest's check" });
  await expect(evidence).toContainText("mise run check");
  await expect(evidence).toContainText(/failed · exit 1/);
  await expect(evidence).toContainText("exported type Size should have comment");
  await expect(detail.getByRole("note", { name: "What the agent last said" })).toContainText("disable that lint rule");
  await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "failed-check");
  await evidence.getByRole("button", { name: "Run the check again" }).click();
  // The demo checkout has no mise: the re-run ends at once, and the job says what happened.
  await expect(page.getByText(/Running the check again|Not done/).first()).toBeVisible();
});

test("an accepted job's evidence is pinned to its commit, and Land offers no override", async ({ page }, testInfo) => {
  await page.goto("/?job=j_11");
  const detail = detailRegion(page, testInfo, "Selected job");
  const evidence = detail.getByRole("region", { name: "crowsnest's check" });
  await expect(evidence).toContainText("Accepted");
  await expect(evidence).toContainText(/passed · exit 0/);
  await detail.getByRole("button", { name: "Review and land" }).click();
  await expect(detail.getByText(/evidence does not support/)).toHaveCount(0);
  await detail.getByRole("button", { name: "Merge", exact: true }).click();
  await expect(detail.getByRole("alertdialog", { name: "Confirm Merge" }).getByRole("checkbox")).toHaveCount(0);
});

test("a job near its budget shows the limits, and the budget can be raised", async ({ page }, testInfo) => {
  await page.goto("/?job=j_2");
  const detail = detailRegion(page, testInfo, "Selected job");
  const budget = detail.getByRole("region", { name: "Budget" });
  await expect(budget).toContainText("Dollars");
  await expect(budget).toContainText("of $1.00");
  await budget.getByRole("button", { name: "Raise the budget" }).click();
  await budget.getByLabel("Budget in dollars").fill("5");
  await budget.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Budget changed")).toBeVisible();
  await expect(budget).toContainText("of $5.00");
  await shot(page, testInfo, "budget");
});

test("a blocked job says who owns the wait and when it is woken", async ({ page }, testInfo) => {
  await page.goto("/?job=j_7");
  const detail = detailRegion(page, testInfo, "Selected job");
  const wait = detail.getByLabel("Who it waits on");
  await expect(wait).toContainText(/rechecks at \d\d:\d\d/);
  await expect(wait.getByRole("button", { name: "Wake it" })).toBeVisible();
});

test("the System page lists waits without an owner", async ({ page }) => {
  await page.goto("/system");
  await expect(page.getByRole("region", { name: "Waits without an owner" })).toContainText("Every live job has an owner and a wake-up");
});

test.describe("under the fleet's usage limit", () => {
  test.use({ appOptions: { demo: true, env: { CROWSNEST_DEMO_LIMIT: "1" } } });

  test("one banner with the countdown, and the limited job reads Held", async ({ page }, testInfo) => {
    await page.goto("/?project=all");
    const banner = page.getByRole("status", { name: "claude usage limit" });
    await expect(banner).toContainText(/resets in (49|50) min/);
    await expect(banner).toContainText("1 job waiting");
    const limited = card(page, "Migrate the settings page to the new form kit");
    await expect(limited).toContainText("Held");
    await expect(limited).not.toContainText("Stuck");
    await expectNoSidewaysScroll(page);
    await shot(page, testInfo, "limit");
  });
});
