// The approval policy (DESIGN.md 8.7, Policy) against `crowsnest --demo`: a project's rules on its
// page (the demo's own rule with what it matched, one added with a live preview and removed),
// "Allow and remember for this job" from an approval, a batch allow of one job's low-risk calls,
// and the auto-allowed call on its job's timeline.
//
// The demo seeds Crowsnest's rule r_1 (Bash `go test *`) and the call it allowed, ap_4 from the
// reviewer j_13 (`go test ./internal/ratelimit/... -run TestLimiter`). ap_1 is Website's j_14
// (`npm run test:links`, low: a benign shape since R5). With CROWSNEST_DEMO_PARALLEL, j_14 also asks ap_5 and
// ap_6 (both low), for the batch.
import type { Locator, Page, TestInfo } from "@playwright/test";
import { detailRegion, dockedTray, expect, expectAccessible, expectNoSidewaysScroll, isPhone, smallTapTargets, test } from "../support/fixtures";

test.use({ appOptions: { demo: true, env: { CROWSNEST_DEMO_PARALLEL: "1" } } });

async function approval(page: Page, id: string): Promise<{ status: string; decided_via?: string; status_reason?: string }> {
  const response = await page.request.get(`/api/v1/approvals/${id}`);
  return ((await response.json()) as { data: { status: string; decided_via?: string; status_reason?: string } }).data;
}

/** The tray's list, opened: the desktop panel, or the phone's sheet. */
async function openTray(page: Page, testInfo: TestInfo): Promise<Locator> {
  if (isPhone(testInfo)) {
    await page.getByRole("button", { name: /need you: open the tray/ }).click();
    return page.getByRole("dialog");
  }
  const panel = await dockedTray(page);
  const showAll = panel.getByRole("button", { name: /^Show all/ });
  if (await showAll.isVisible()) await showAll.click();
  return panel;
}

test("a project's rules: the demo's rule with its recent match, one added with a preview, then removed", async ({ page }, testInfo) => {
  await page.goto("/projects/crowsnest");
  const card = page.getByRole("region", { name: "Approval rules" });
  await expect(card).toBeVisible({ timeout: 15_000 });
  const demoRule = card.locator('[data-rule="r_1"]');
  await expect(demoRule).toContainText("Allow Bash commands matching go test *");
  await expect(demoRule.getByTestId("rule-preview")).toContainText("Would have matched 1 recent call: go test ./internal/ratelimit/... -run TestLimiter");

  // A preview before saving: the reviewer's force push is matched, and flagged as never allowed.
  const form = card.getByRole("form", { name: "Add a rule" });
  await form.getByRole("textbox", { name: "Pattern" }).fill("git push *");
  await expect(form.getByTestId("rule-preview")).toContainText("never auto-allowed");
  await form.getByLabel("Kind").selectOption("tool");
  await form.getByRole("textbox", { name: "Pattern" }).fill("WebSearch");
  await expect(form.getByTestId("rule-preview")).toContainText("Would have matched none");
  await form.getByRole("button", { name: "Add rule" }).click();
  const added = card.locator('[data-rule="r_2"]');
  await expect(added).toContainText("Allow the tool WebSearch");

  await expectNoSidewaysScroll(page);
  if (isPhone(testInfo)) expect(await smallTapTargets(page, '[aria-label="Approval rules"]')).toEqual([]);
  await expectAccessible(page, testInfo, "approval-rules");

  await added.getByRole("button", { name: /Remove r_2/ }).click();
  await expect(card.locator('[data-rule="r_2"]')).toHaveCount(0);
  const rules = (await (await page.request.get("/api/v1/projects/crowsnest/approval-rules")).json()) as { data: Array<{ id: string }> };
  expect(rules.data.map((rule) => rule.id)).toEqual(["r_1"]);
});

test("allow and remember for this job, from an approval in the tray", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible({ timeout: 15_000 });
  const tray = await openTray(page, testInfo);
  await tray.getByRole("button", { name: /npm run test:links(?! --)/, expanded: false }).first().click();
  const card = tray.locator('[data-approval="ap_1"]');
  await card.getByRole("button", { name: "Allow and remember for this job" }).click();
  const pattern = card.getByRole("textbox", { name: /Pattern/ });
  await expect(pattern).toHaveValue("npm run *");
  await expect(card.getByTestId("remember-sentence")).toContainText("Until j_14 ends, crowsnest will allow Bash commands matching `npm run *`");
  await card.getByRole("button", { name: "Allow and remember" }).click();

  await expect.poll(async () => (await approval(page, "ap_1")).status).toBe("allowed");
  // The new rule settles the job's other matching call (ap_5) by policy; ap_6 (ls) stays.
  await expect.poll(async () => (await approval(page, "ap_5")).decided_via).toBe("policy");
  expect((await approval(page, "ap_6")).status).toBe("pending");
  const rules = (await (await page.request.get("/api/v1/jobs/j_14/approval-rules")).json()) as { data: Array<{ pattern: string; scope: string }> };
  expect(rules.data).toEqual([expect.objectContaining({ pattern: "npm run *", scope: "job" })]);
});

test("batch allow: one job's low-risk calls with one confirming tap", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible({ timeout: 15_000 });
  const tray = await openTray(page, testInfo);
  await tray.getByRole("button", { name: /ls dist\/changelog/, expanded: false }).first().click();
  await tray.getByRole("button", { name: "Allow 3 low-risk calls from job-14" }).click();
  const group = tray.getByRole("group", { name: /Allow 3 calls from job-14/ });
  await expect(group.getByRole("list", { name: "Calls in the batch" })).toContainText("npm run test:links -- dist/blog");
  await group.getByRole("button", { name: "Allow all 3" }).click();
  for (const id of ["ap_1", "ap_5", "ap_6"]) await expect.poll(async () => (await approval(page, id)).status).toBe("allowed");
  // Nothing else was touched: the dangerous force push still waits.
  expect((await approval(page, "ap_3")).status).toBe("pending");
});

test("the auto-allowed call says why, and is on its job timeline", async ({ page }, testInfo) => {
  const why = await approval(page, "ap_4");
  expect(why.decided_via).toBe("policy");
  expect(why.status_reason).toBe("Allowed by rule r_1 (project Crowsnest): Bash commands matching `go test *`");
  await page.goto("/jobs?job=j_13");
  const detail = detailRegion(page, testInfo, "Selected job");
  await expect(detail.getByRole("list", { name: "Timeline" })).toContainText("Auto-allowed by rule r_1: Bash: go test ./internal/ratelimit/... -run TestLimiter", { timeout: 15_000 });
});
