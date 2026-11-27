// The Inbox (DESIGN.md 8 and 12) against `crowsnest --demo`: every question kind answered (confirm,
// choice, multi, manual, ack, notice) with a note; a mermaid body renders to an SVG; a question
// body carrying HTML and script payloads renders as text and runs nothing; the diff view; a
// terminal dialog answered from its card, including the 409 path when the screen changed under
// the press; History; and the badge and tab title following new questions live.
//
// Demo inbox (internal/questions/demo.go), each asked from a demo job's pane: q_1 confirm,
// blocking, with a diff and a mermaid diagram (j_11, job-11); q_2 multi-select (j_12); q_3 choice
// (the orchestrator's j_4); q_4 manual, blocking (j_15); q_5 ack (j_1); q_6 notice, low (j_2); q_7
// answered (j_1); q_8 the release question j_4 waits on. The Handbook job j_5 (agent job-5, pane
// w6:p1) sits at a terminal dialog.
import type { Page, TestInfo } from "@playwright/test";
import { askAsAgent } from "../support/agent";
import { detailRegion, expect, isPhone, test } from "../support/fixtures";

async function openItem(page: Page, testInfo: TestInfo, id: string) {
  await page.goto(`/inbox?q=${encodeURIComponent(id)}`);
  return detailRegion(page, testInfo, "Selected item");
}

/**
 * Waits for an answered question's outcome. On a phone the sheet closes after an answer (M9 bug:
 * it stayed open now and then, because the close was dropped with the unmounted form), so the
 * question is opened again to read its outcome.
 */
async function expectAnswered(page: Page, testInfo: TestInfo, id: string, summary: string | RegExp) {
  if (isPhone(testInfo)) {
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await page.goto(`/inbox?q=${id}`);
  }
  await expect(detailRegion(page, testInfo, "Selected item").getByRole("region", { name: "Outcome" })).toContainText(summary, { timeout: 10_000 });
}

test("confirm (blocking, with a diff and a mermaid diagram) answered with a note", async ({ page }, testInfo) => {
  const detail = await openItem(page, testInfo, "q_1");
  await expect(detail.getByRole("heading", { name: "Apply the fleet cache refactor?" })).toBeVisible();
  await expect(detail.getByText("Blocking", { exact: true })).toBeVisible();
  // Mermaid ran (lazily, strict) and drew an SVG.
  await expect(detail.locator("svg[aria-roledescription], .mermaid svg, svg[id^=mermaid]").first()).toBeVisible({ timeout: 15_000 });
  // The diff: unified on a phone, split from lg.
  const diff = detail.getByRole("region", { name: "Diff of internal/fleet/cache.go" });
  await expect(diff).toBeVisible();
  await expect(diff.locator(`table[data-layout=${isPhone(testInfo) ? "unified" : "split"}]`)).toBeVisible();
  // The recommended option is marked, never preselected.
  const apply = detail.getByRole("button", { name: /Apply it/ });
  await expect(apply).toContainText("Recommended");
  await detail.getByRole("textbox", { name: /Note/ }).fill("Go, but keep the old cache behind a flag for a week");
  await apply.click();
  await expectAnswered(page, testInfo, "q_1", "Apply it");
  await expect(detailRegion(page, testInfo, "Selected item").getByRole("region", { name: "Outcome" })).toContainText("Note: Go, but keep the old cache");
});

test("single choice, multi-select, manual, ack and a notice are each answered", async ({ page }, testInfo) => {
  // Single choice: one press answers.
  let detail = await openItem(page, testInfo, "q_3");
  await detail.getByRole("button", { name: /Both, generated from commits/ }).click();
  await expectAnswered(page, testInfo, "q_3", "Both, generated from commits");

  // Multi: nothing until Submit, which counts the picks.
  detail = await openItem(page, testInfo, "q_2");
  const submit = detail.getByRole("button", { name: /^Submit/ });
  await expect(submit).toBeDisabled();
  await detail.getByRole("checkbox", { name: /Data race/ }).check();
  await detail.getByRole("checkbox", { name: /Document the 150 ms window/ }).check();
  await expect(submit).toHaveText(/Submit \(2\)/);
  await submit.click();
  await expectAnswered(page, testInfo, "q_2", /Data race.*, .*Document the 150 ms window/);

  // Manual: the note is the answer, and it is required.
  detail = await openItem(page, testInfo, "q_4");
  const send = detail.getByRole("button", { name: "Send answer" });
  await expect(send).toBeDisabled();
  await detail.getByRole("textbox", { name: "Your answer" }).fill("Crowsnest runs your agents from your phone.");
  await send.click();
  await expectAnswered(page, testInfo, "q_4", "Crowsnest runs your agents from your phone.");

  // Ack.
  detail = await openItem(page, testInfo, "q_5");
  await detail.getByRole("button", { name: "Acknowledge" }).click();
  await expectAnswered(page, testInfo, "q_5", "Acknowledged");

  // A notice is dismissed, and has no Cancel.
  detail = await openItem(page, testInfo, "q_6");
  await expect(detail.getByRole("button", { name: "Cancel question" })).toHaveCount(0);
  await detail.getByRole("button", { name: "Dismiss" }).click();
  await expectAnswered(page, testInfo, "q_6", "Dismissed");

  // All of them are in History now, and none is open.
  await page.goto("/inbox");
  const open = page.getByRole("list", { name: "Needs you" });
  for (const title of ["Where should the release notes live?", "Which findings should the builder fix before merge?", "What should the homepage hero say?"]) {
    await expect(open.getByText(title)).toHaveCount(0);
  }
  await page.getByRole("tab", { name: "History" }).click();
  const history = page.getByRole("list", { name: "History" });
  await expect(history.getByText("Where should the release notes live?")).toBeVisible();
  await expect(history.getByText("Preview build finished: 214 pages, 14 s")).toBeVisible();
  await expect(history.getByText("Keep the retry schedule in SQLite?")).toBeVisible();
});

test("cancelling a question records it as cancelled", async ({ page }, testInfo) => {
  const detail = await openItem(page, testInfo, "q_3");
  await detail.getByRole("button", { name: "Cancel question" }).click();
  await expectAnswered(page, testInfo, "q_3", "Cancelled");
});

test("HTML and script in a question body render as text and run nothing", async ({ page, app }, testInfo) => {
  await page.addInitScript(() => {
    (window as unknown as { __xss: string[] }).__xss = [];
  });
  const id = await askAsAgent(app, {
    title: "Is this body safe?",
    kind: "ack",
    body_md: [
      "Raw tags: <script>window.__xss.push('script')</script>",
      "",
      '<img src="x" onerror="window.__xss.push(\'img\')">',
      "",
      '<a href="javascript:window.__xss.push(\'a\')">html link</a> and [md link](javascript:window.__xss.push("md"))',
      "",
      '<iframe src="javascript:window.__xss.push(\'iframe\')"></iframe>',
      "",
      "```mermaid",
      'graph TD; A["<img src=x onerror=window.__xss.push(\'mermaid\')>"] --> B',
      'click A call window.__xss.push("click")',
      "```",
    ].join("\n"),
  });
  const detail = await openItem(page, testInfo, id);
  await expect(detail.getByRole("heading", { name: "Is this body safe?" })).toBeVisible();
  // Markdown without raw HTML shows the tags as text.
  await expect(detail.getByText(/Raw tags:/)).toBeVisible();
  // Mermaid (strict) had its chance to render.
  await expect(detail.locator("svg").first()).toBeAttached({ timeout: 15_000 });
  await page.waitForTimeout(500);
  // Click every link and the diagram node: none of them runs anything.
  for (const link of await detail.getByRole("link").all()) await link.click({ trial: false, noWaitAfter: true }).catch(() => {});
  expect(await page.evaluate(() => (window as unknown as { __xss: string[] }).__xss)).toEqual([]);
  expect(await detail.locator("script, iframe, img[onerror]").count()).toBe(0);
  for (const href of await detail.getByRole("link").evaluateAll((links) => links.map((link) => link.getAttribute("href")))) {
    expect(href ?? "").not.toMatch(/^javascript:/i);
  }
});

test("a terminal dialog is answered from its card; a press against a changed screen is refused and the card refreshes", async ({ page }, testInfo) => {
  // First press: the screen the card shows is stale (its hash is not the screen's any more),
  // as when the agent moved on between render and press. The server refuses with 409 and
  // sends nothing; the card says so and shows the current screen.
  let stale = true;
  await page.route("**/api/v1/agents/*/screen", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { screen_hash: string } };
    if (stale) body.data.screen_hash = "0".repeat(32);
    await route.fulfill({ response, json: body });
  });
  const keys: Array<{ status: number }> = [];
  page.on("response", (response) => {
    if (response.url().endsWith("/keys")) keys.push({ status: response.status() });
  });
  const detail = await openItem(page, testInfo, "dialog:w6:p1");
  const card = detail.getByLabel("Dialog from job-5");
  await expect(card.getByRole("region", { name: "Screen text" }).or(card.getByLabel("Screen text"))).toContainText("rm -rf dist");
  // The risky option is badged.
  await expect(card.getByRole("button", { name: /don't ask again/ })).toContainText("Risky");
  await card.getByRole("list", { name: "Options" }).getByRole("button").first().click();
  await expect(card.getByText("The screen changed before your answer arrived, so nothing was sent.")).toBeVisible();
  expect(keys).toEqual([{ status: 409 }]);

  // The refreshed card carries the real hash; the press goes through.
  stale = false;
  await page.unroute("**/api/v1/agents/*/screen");
  await page.reload();
  const fresh = detailRegion(page, testInfo, "Selected item").getByLabel("Dialog from job-5");
  await fresh.getByRole("list", { name: "Options" }).getByRole("button").first().click();
  await expect(page.getByText(/^Sent “1” to job-5/)).toBeVisible();
  expect(keys.at(-1)).toEqual({ status: 200 });
  // The Handbook job goes to work, so the dialog leaves the Inbox.
  await page.goto("/inbox");
  await expect(page.getByRole("list", { name: "Needs you" }).getByText("job-5 is waiting at a dialog")).toHaveCount(0, { timeout: 15_000 });
});

test("the badge and the tab title count what needs you, and follow new questions live", async ({ page, app }) => {
  await page.goto("/");
  // The one attention queue (internal/attention): five open demo questions (the low notice q_6
  // is not counted), the question j_4 waits on, the three demo tool-call approvals (DESIGN.md
  // 8.7), the Handbook job's dialog, and four jobs: j_6 unresponsive, j_3 at the usage limit,
  // j_9 failed, and the landing demo's j_18 ready to land.
  await expect(page).toHaveTitle(/^\(14\) crowsnest/);
  const badge = page.getByRole("navigation", { name: "Views" }).getByLabel(/need you/).first();
  await expect(badge).toHaveAttribute("aria-label", "14 need you");

  await askAsAgent(app, { title: "A new question from an agent", kind: "ack" });
  await expect(page).toHaveTitle(/^\(15\) crowsnest/, { timeout: 10_000 });
  await expect(page.getByRole("navigation", { name: "Views" }).getByLabel("15 need you").first()).toBeAttached();

  // Answering one from another tab brings it down here, without a reload.
  const other = await page.context().newPage();
  await other.goto("/inbox?q=q_5");
  await other.getByRole("button", { name: "Acknowledge", exact: true }).click();
  await expect(page).toHaveTitle(/^\(14\) crowsnest/, { timeout: 10_000 });
  await other.close();
});

test("an empty Inbox says nothing needs you", async ({ page }) => {
  // The Open list is the attention queue: an empty one, and no event stream to refill it.
  await page.route("**/api/v1/attention", (route) =>
    route.fulfill({ json: { data: { items: [], counts: { total: 0, blocking: 0, can_wait: 0, snoozed: 0, by_project: {} }, revision: 1, at: new Date().toISOString() } } }),
  );
  await page.route("**/api/v1/events", (route) => route.abort());
  await page.route("**/api/v1/questions?status=open", (route) => route.fulfill({ json: { data: [] } }));
  await page.route("**/api/v1/approvals?status=pending", (route) => route.fulfill({ json: { data: [] } }));
  await page.route("**/api/v1/jobs", (route) => route.fulfill({ json: { data: [] } }));
  await page.route("**/api/v1/fleet", async (route) => {
    // The app can abort a fleet request it no longer needs (a newer one superseded it) while
    // this handler is still reading the real response; Playwright then throws "Response has been
    // disposed" and failed the test about once in 25 runs. An aborted request needs no answer.
    try {
      const response = await route.fetch();
      const body = (await response.json()) as { data: { agents: Array<{ needs_you: boolean }> } };
      for (const agent of body.data.agents) agent.needs_you = false;
      await route.fulfill({ response, json: body });
    } catch {
      await route.abort().catch(() => undefined);
    }
  });
  await page.goto("/inbox");
  await expect(page.getByRole("heading", { name: "Nothing needs you" })).toBeVisible();
});
