// The Jobs view as a user drives it (DESIGN.md 6 and 12), against `crowsnest --demo`: give a job
// from the text box (with and without the options) and watch it land on the board and move; open
// a running job, message its agent, answer its question inline; a stuck job says why and offers
// the one action that fits; nudge; cancel behind a confirmation; a finished job's result with
// what was and was not verified; the board's order; the empty state.
//
// Demo job ids, in seed order (internal/jobs/demo.go): j_1 running (worktree), j_2 nudged, j_3
// stuck on a limit, j_4 needs you (question q_8), j_5 needs you (a dialog), j_6 unresponsive,
// j_7 blocked, j_8 done, j_9 failed (older), j_10 queued for the orchestrator (it picks it up); then the landing demo (internal/jobs/demo_landing.go): j_11 done and ready to land, j_12 in review (reviewers j_13, j_14), j_15 waiting for a slot. A job a test gives is j_16.
import type { Page } from "@playwright/test";
import { detailRegion, expect, isPhone, test } from "../support/fixtures";

async function openBoard(page: Page, search = "") {
  await page.goto(`/${search}`);
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
}

/** The titles of a board section, top to bottom. */
async function sectionTitles(page: Page, section: string): Promise<string[]> {
  const items = page.getByRole("region", { name: section }).getByRole("listitem");
  const titles: string[] = [];
  for (const item of await items.all()) titles.push((await item.locator("span.font-medium").first().innerText()).trim());
  return titles;
}

test("giving a job with a directory starts an agent, and the card moves from queued to running", async ({ page, app }, testInfo) => {
  await openBoard(page);
  await page.getByLabel("What needs doing?").fill("Add a health check to the webhook sender\n\nReport the last delivery and the queue depth.");
  await page.getByRole("button", { name: /To the orchestrator/ }).click();
  // The instance's own scratch directory: it exists, and nothing else uses it.
  await page.getByLabel(/^Where/).fill(app.root);
  await page.getByLabel("Role").selectOption("tester");
  await page.getByLabel("Done when").fill("the endpoint answers and the README says how");
  await expect(page.getByRole("button", { name: new RegExp(`To ${app.root.split("/").pop()} · tester`) })).toBeVisible();
  await page.getByRole("button", { name: "Give job" }).click();

  await expect(page.getByText("Job j_16 given")).toBeVisible();
  await expect(page).toHaveURL(/job=j_16/);
  const detail = detailRegion(page, testInfo, "Selected job");
  await expect(detail.getByRole("heading", { name: "Add a health check to the webhook sender" })).toBeVisible();
  await expect(detail.getByText("Done when: the endpoint answers and the README says how")).toBeVisible();
  // The launcher opens a pane, sends the brief and the job is running, all on the timeline.
  const timeline = detail.getByRole("list", { name: "Timeline" });
  await expect(timeline).toContainText("Brief sent to job-16", { timeout: 15_000 });
  await expect(timeline).toContainText("queued → running");
  await expect(detail.getByText("Running", { exact: true }).first()).toBeVisible();

  // On the board it sits in progress, with its agent.
  if (isPhone(testInfo)) await page.getByRole("button", { name: "Close" }).click();
  const card = page.getByRole("region", { name: "In progress" }).getByRole("button", { name: /Add a health check/ });
  await expect(card).toContainText("job-16");
  // The text box was cleared for the next job.
  await expect(page.getByLabel("What needs doing?")).toHaveValue("");
});

test("a directory that does not exist is refused with a message, and the brief is kept", async ({ page }) => {
  await openBoard(page);
  await page.getByLabel("What needs doing?").fill("Build the thing");
  await page.getByRole("button", { name: /To the orchestrator/ }).click();
  await page.getByLabel(/^Where/).fill("/nonexistent/e2e/dir");
  await page.getByRole("button", { name: "Give job" }).click();
  await expect(page.getByText("The job was not created")).toBeVisible();
  await expect(page.getByText(/does not exist or is not a directory/)).toBeVisible();
  await expect(page.getByLabel("What needs doing?")).toHaveValue("Build the thing");
  await expect(page).not.toHaveURL(/job=/);
});

test("giving a job without options sends it to the orchestrator; Cmd/Ctrl+Enter gives it", async ({ page }, testInfo) => {
  await openBoard(page);
  const box = page.getByLabel("What needs doing?");
  await box.fill("Summarise yesterday's merged PRs");
  await box.press("ControlOrMeta+Enter");
  await expect(page.getByText("Sent to the orchestrator")).toBeVisible();
  const detail = detailRegion(page, testInfo, "Selected job");
  await expect(detail.getByRole("heading", { name: "Summarise yesterday's merged PRs" })).toBeVisible();
  await expect(detail.getByText("orchestrator", { exact: true }).first()).toBeVisible();
  await expect(detail.getByText("lead", { exact: true })).toBeVisible();
  // The orchestrator holds j_10 already, so this one waits its turn.
  await expect(detail.getByText("Queued", { exact: true }).first()).toBeVisible();
  // An empty brief cannot be given.
  if (isPhone(testInfo)) await page.getByRole("button", { name: "Close" }).click();
  await expect(page.getByRole("button", { name: "Give job" })).toBeDisabled();
});

test("the board puts needs-you first, then stuck, then running, then finished today, older behind a toggle", async ({ page }) => {
  await openBoard(page);
  const attention = await sectionTitles(page, "Needs you or stuck");
  // Needs-you (the question and the dialog) before stuck (the limit and the unresponsive job).
  expect(attention.slice(0, 2).sort()).toEqual(["Decide the release date for v0.2", "Review the auth change"]);
  expect(attention.slice(2).sort()).toEqual(["Migrate the settings page to the new form kit", "Profile the fleet cache"]);
  const running = await sectionTitles(page, "In progress");
  expect(running).toEqual(expect.arrayContaining(["Add retry back-off to the webhook sender", "Tidy the README install section", "Upgrade the Postgres driver"]));
  // The demo's finished jobs settled 2 h and 5 h before it started, so which side of midnight they
  // fall on depends on the time of day (and the timezone) the test runs in. Each one must be in
  // exactly one of the two places, today's list or behind the toggle, never lost. Asserting an
  // exact "Finished today" list made this test fail whenever it ran more than 5 h after midnight.
  const finished = ["Fix the flaky SSE reconnect test", "Port the TUI to Bubble Tea v2"];
  const today = (await page.getByRole("region", { name: "Finished today" }).count()) > 0 ? await sectionTitles(page, "Finished today") : [];
  expect(today.every((title) => finished.includes(title))).toBe(true);
  const behindToggle = finished.filter((title) => !today.includes(title));
  const older = page.getByRole("button", { name: /Show \d+ older/ });
  expect(await older.isVisible()).toBe(behindToggle.length > 0);
  if (behindToggle.length > 0) {
    await older.click();
    for (const title of behindToggle) await expect(page.getByRole("region", { name: "Older" })).toContainText(title);
    await page.getByRole("button", { name: "Hide older" }).click();
    await expect(page.getByRole("region", { name: "Older" })).toHaveCount(0);
  }
  // Cards carry the state, the progress line and the counts.
  const limited = page.getByRole("button", { name: /Migrate the settings page/ });
  await expect(limited).toContainText("Retry in");
  await expect(page.getByRole("button", { name: /Tidy the README/ })).toContainText(/\d+ nudges/);
  await expect(page.getByRole("button", { name: /Decide the release date/ })).toContainText("1 question");
});

test("a running job: message its agent and watch the timeline", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_1");
  const detail = detailRegion(page, testInfo, "Selected job");
  await expect(detail.getByRole("heading", { name: "Add retry back-off to the webhook sender" })).toBeVisible();
  await expect(detail.getByText("Doing now:")).toBeVisible();
  await expect(detail.getByText("~/.herdr/worktrees/crowsnest/job-1 · job/1-add-retry-back-off")).toBeVisible();
  // A job names the agent its pane holds (R3, ExpectAgent): the demo's builder pane.
  const box = detail.getByRole("textbox", { name: "Message to builder" });
  await box.fill("Use full jitter, not equal jitter");
  await detail.getByRole("button", { name: "Send" }).click();
  // The builder is working, so Claude queues it.
  await expect(page.getByText(/Queued for the agent|Sent to the agent/)).toBeVisible();
  await expect(box).toHaveValue("");
  await expect(detail.getByRole("list", { name: "Timeline" })).toContainText("Use full jitter", { timeout: 10_000 });
});

test("a job waiting on a question is answered inline and goes back to running", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_4");
  const detail = detailRegion(page, testInfo, "Selected job");
  const banner = detail.getByRole("alert");
  await expect(banner).toContainText("1 open question");
  await expect(banner.getByRole("button", { name: "Answer in Inbox" })).toBeVisible();
  const question = detail.getByRole("article", { name: "Ship v0.2 on Friday?" });
  await question.getByRole("textbox", { name: /Note/ }).fill("Friday, after the docs pass");
  await question.getByRole("button", { name: /Friday/ }).click();
  // The confirmation survives the form being replaced by the answer (M9 bug: it was dropped).
  await expect(page.getByText("Answered q_8")).toBeVisible();
  await expect(detail.getByText("Running", { exact: true }).first()).toBeVisible({ timeout: 10_000 });
  await expect(detail.getByRole("alert")).toHaveCount(0);
  await expect(detail.getByText(/q_8 Ship v0\.2 on Friday\? → Friday/)).toBeVisible();
});

test("'Answer in Inbox' opens the job's question in the Inbox", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_4");
  const detail = detailRegion(page, testInfo, "Selected job");
  await detail.getByRole("button", { name: "Answer in Inbox" }).click();
  await expect(page).toHaveURL(/\/inbox\?q=q_8/);
  await expect(detailRegion(page, testInfo, "Selected item").getByRole("heading", { name: "Ship v0.2 on Friday?" })).toBeVisible();
});

test("each stuck cause shows its reason and the one action that fits", async ({ page }, testInfo) => {
  // A usage limit: the reason, the countdown, Retry now.
  await openBoard(page, "?job=j_3");
  let detail = detailRegion(page, testInfo, "Selected job");
  let banner = detail.getByRole("alert");
  await expect(banner).toContainText("Hit a usage or rate limit");
  await expect(banner).toContainText("Usage limit reached; resets at 14:00");
  await expect(banner).toContainText(/Retry in .* \(attempt 2\)/);
  await banner.getByRole("button", { name: "Retry now" }).click();
  await expect(page.getByText("Retry now: done")).toBeVisible();

  // A dialog: Open terminal switches to the job's terminal.
  await openBoard(page, "?job=j_5");
  detail = detailRegion(page, testInfo, "Selected job");
  banner = detail.getByRole("alert");
  await expect(banner).toContainText("Claude wants to run: rm -rf node_modules");
  await banner.getByRole("button", { name: "Open terminal" }).click();
  await expect(detail.getByRole("tab", { name: "Terminal" })).toHaveAttribute("aria-selected", "true");
  await expect(detail.locator("[data-terminal]")).toBeVisible();

  // Unresponsive after three nudges: Nudge.
  await openBoard(page, "?job=j_6");
  detail = detailRegion(page, testInfo, "Selected job");
  banner = detail.getByRole("alert");
  await expect(banner).toContainText("Ended its turn 4 times in a row");
  await expect(banner.getByRole("button", { name: "Nudge" })).toBeVisible();
});

test("nudging an unresponsive job sends it back to work and clears the flag", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_6");
  const detail = detailRegion(page, testInfo, "Selected job");
  await detail.getByRole("alert").getByRole("button", { name: "Nudge" }).click();
  await expect(page.getByText("Nudge: done")).toBeVisible();
  await expect(detail.getByRole("alert")).toHaveCount(0);
  await expect(detail.getByRole("list", { name: "Timeline" })).toContainText("Nudged by you");
  await expect(detail.getByText("4 nudges")).toBeVisible();
});

test("cancelling asks first, then the job is cancelled and leaves the live sections", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_1");
  const detail = detailRegion(page, testInfo, "Selected job");
  await detail.getByRole("button", { name: "Cancel job" }).click();
  await expect(detail.getByText("Cancel j_1? Esc is sent to the agent; its pane stays open.")).toBeVisible();
  await detail.getByRole("button", { name: "Keep it" }).click();
  await expect(detail.getByText(/^Cancel j_1\?/)).toHaveCount(0);
  await detail.getByRole("button", { name: "Cancel job" }).click();
  await detail.getByRole("button", { name: "Cancel the job" }).click();
  await expect(detail.getByText("Cancelled", { exact: true }).first()).toBeVisible();
  // A finished job has no message box and no cancel.
  await expect(detail.getByRole("button", { name: "Cancel job" })).toHaveCount(0);
  await expect(detail.getByRole("form", { name: "Message the agent" })).toHaveCount(0);
  if (isPhone(testInfo)) await page.getByRole("button", { name: "Close" }).click();
  await expect(page.getByRole("region", { name: "In progress" })).not.toContainText("Add retry back-off");
  await expect(page.getByRole("region", { name: "Finished today" })).toContainText("Add retry back-off");
});

test("a finished job shows its result, what was verified and what was not", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_8");
  const detail = detailRegion(page, testInfo, "Selected job");
  const result = detail.getByText("Fixed the race in sse.test.tsx: the fake timer advanced");
  await expect(result).toBeVisible();
  await expect(detail.getByRole("region", { name: "Verified", exact: true })).toContainText("mise run check: green");
  await expect(detail.getByRole("region", { name: "Verified", exact: true })).toContainText("50 runs, 0 failures");
  await expect(detail.getByRole("region", { name: "Not verified" })).toContainText("Safari");
  await expect(detail.getByLabel("Done")).toBeVisible();
  // A failed job says why instead.
  await openBoard(page, "?job=j_9");
  await expect(detailRegion(page, testInfo, "Selected job").getByText("Failed: Bubble Tea v2 is not released yet; the beta API changed twice this week.")).toBeVisible();
});

test("a blocked job says what it is waiting for", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_7");
  await expect(detailRegion(page, testInfo, "Selected job").getByText("Blocked: Waiting for the staging database to come back").first()).toBeVisible();
});

test("an empty board invites the first job", async ({ page }) => {
  await page.route("**/api/v1/jobs", (route) => (route.request().method() === "GET" ? route.fulfill({ json: { data: [] } }) : route.continue()));
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "No jobs yet" })).toBeVisible();
  await expect(page.getByText("Say what needs doing above.")).toBeVisible();
});

test("a job that does not exist says so", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_999");
  await expect(detailRegion(page, testInfo, "Selected job").getByRole("alert")).toContainText("Could not load j_999");
});
