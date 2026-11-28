// The phone, end to end against `crowsnest --demo` (R4: Fable 3.2 to 3.6, 4.1; Codex P1 to P4,
// V1), at 390x844 in Chromium and WebKit (touch) and at 1440x900:
//
//   - a decision packet opened from a push's deep link: where it belongs, the ask, what each
//     choice does, the evidence, and the answer bar on screen at the bottom; the note survives a
//     reload, a rotation and the page going to the background; after the answer the same link says
//     it is already answered instead of failing;
//   - an approval from its packet: Allow once for a low one, the second confirmation for a
//     dangerous one;
//   - a long diff: collapsed behind a file list, a file opened from it;
//   - rescue without a terminal: the stuck job's Rescue panel, Interrupt and Nudge;
//   - the phone terminal's key bar: every key on screen at 390 px;
//   - (giving a job by voice is phone-voice.spec.ts: it needs a fake ElevenLabs and a fake mic);
//   - axe on the packet and the rescue panel; screenshots into web/screenshots/phone-*.png.
//
// Lock-screen buttons and a real iPhone are out of reach here: docs/PHONE-CHECK.md is that pass.
import type { Page, TestInfo } from "@playwright/test";
import { expect, expectAccessible, isPhone, test } from "../support/fixtures";

async function sizeFor(page: Page, testInfo: TestInfo) {
  if (isPhone(testInfo)) await page.setViewportSize({ width: 390, height: 844 });
}

function width(testInfo: TestInfo): string {
  return isPhone(testInfo) ? `390${testInfo.project.name.includes("webkit") ? "-webkit" : ""}` : "1440";
}

interface Item {
  id: string;
  kind: string;
  urgency: string;
  project: { name: string } | null;
  approval?: { risk: string };
  job_id?: string;
  job?: { stuck: boolean; cause: string };
  question?: { kind: string; multi: boolean };
}

async function items(page: Page): Promise<Item[]> {
  const response = await page.request.get("/api/v1/attention");
  return ((await response.json()) as { data: { items: Item[] } }).data.items;
}

/** The answer bar's top edge is inside the viewport: the answer is within thumb reach. */
async function expectBarOnScreen(page: Page) {
  const bar = page.getByTestId("answer-bar");
  await expect(bar).toBeVisible();
  const box = await bar.boundingBox();
  const height = page.viewportSize()?.height ?? 0;
  expect(box && box.y < height && box.y + box.height <= height + 1, `answer bar at ${JSON.stringify(box)} in a ${height}px viewport`).toBe(true);
}

test("a question's decision packet from a push link: brief first, the bar in reach, the note kept, then stale", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const question = (await items(page)).find((item) => item.kind === "question" && item.question && !item.question.multi && (item.question.kind === "confirm" || item.question.kind === "choice"));
  expect(question, "the demo has a single-choice question").toBeDefined();
  await page.goto(`/decide?item=${encodeURIComponent(question!.id)}`);

  const packet = page.getByTestId("decision-packet");
  await expect(packet).toBeVisible();
  await expect(packet.getByRole("navigation", { name: "Where this belongs" })).toContainText(question!.project?.name ?? "Unfiled");
  await expect(packet.getByRole("heading", { level: 1 })).not.toBeEmpty();
  await expect(packet.getByRole("region", { name: "What each choice does" })).toBeVisible();
  await expectBarOnScreen(page);
  await page.screenshot({ path: `screenshots/phone-decide-question-${width(testInfo)}.png` });
  await expectAccessible(page, testInfo, "decision packet");

  // The note survives a reload, a rotation and a trip to the background. It is folded behind
  // "Add a note" until wanted (R10 Fable F6), and stays open once it has text.
  await page.getByTestId("answer-bar").getByRole("button", { name: "Add a note" }).click();
  const note = page.getByTestId("answer-bar").getByLabel(/Note/);
  await note.fill("only after the backup");
  await page.reload();
  await expect(page.getByTestId("answer-bar").getByLabel(/Note/)).toHaveValue("only after the backup");
  if (isPhone(testInfo)) {
    await page.setViewportSize({ width: 844, height: 390 });
    await expect(page.getByTestId("answer-bar").getByLabel(/Note/)).toHaveValue("only after the backup");
    await page.setViewportSize({ width: 390, height: 844 });
  }
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect(page.getByTestId("answer-bar").getByLabel(/Note/)).toHaveValue("only after the backup");

  // Answer: the packet moves on, and the same link afterwards says it was answered.
  const choices = page.getByTestId("answer-bar").getByRole("group", { name: "Choices" }).getByRole("button");
  await choices.first().click();
  const confirm = page.getByRole("button", { name: /^Yes, / });
  if (await confirm.isVisible().catch(() => false)) await confirm.click();
  await expect(page).not.toHaveURL(new RegExp(encodeURIComponent(question!.id)));
  await page.goto(`/decide?item=${encodeURIComponent(question!.id)}`);
  await expect(page.getByRole("status").filter({ hasText: "Already answered." })).toBeVisible();
  // What was answered, and how far it got (stored, held, received: main's answer receipt).
  await expect(page.getByRole("region", { name: "Outcome" })).toBeVisible();
  await expect(page.getByTestId("answer-bar")).toHaveCount(0);
});

test("approvals from their packets: allow once a low one; a dangerous one asks again", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const all = await items(page);
  const low = all.find((item) => item.kind === "approval" && item.approval?.risk === "low");
  const dangerous = all.find((item) => item.kind === "approval" && item.approval?.risk === "dangerous");
  expect(low && dangerous).toBeTruthy();

  await page.goto(`/decide?item=${encodeURIComponent(dangerous!.id)}`);
  await expect(page.getByRole("region", { name: "The call" })).toBeVisible();
  await expectBarOnScreen(page);
  await page.getByTestId("answer-bar").getByRole("button", { name: /Allow once/ }).click();
  await expect(page.getByRole("group", { name: "Confirm a dangerous allow" })).toBeVisible();
  await page.screenshot({ path: `screenshots/phone-decide-dangerous-${width(testInfo)}.png` });
  await page.getByRole("button", { name: "Cancel" }).click();

  await page.goto(`/decide?item=${encodeURIComponent(low!.id)}`);
  await page.getByTestId("answer-bar").getByRole("button", { name: /Allow once/ }).click();
  await expect.poll(async () => (await items(page)).some((item) => item.id === low!.id)).toBe(false);
});

test("a long diff is navigable: a file list first, a file opened from it", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const response = await page.request.get("/api/v1/questions?status=open");
  const open = ((await response.json()) as { data: Array<{ id: string; file_count: number }> }).data;
  const withFiles = open.find((question) => question.file_count > 0);
  expect(withFiles, "the demo has a question with a diff").toBeDefined();
  await page.goto(`/decide?item=${encodeURIComponent(`question:${withFiles!.id}`)}`);
  const navigator = page.getByTestId("diff-navigator");
  await expect(navigator).toBeVisible();
  const files = navigator.getByRole("navigation", { name: "Changed files" }).getByRole("button");
  await files.first().click();
  await expect(navigator.getByRole("region", { name: /^Diff of / }).first()).toBeVisible();
  await page.screenshot({ path: `screenshots/phone-decide-diff-${width(testInfo)}.png`, fullPage: true });
});

test("rescue a stuck agent with buttons, not a terminal", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const stuck = (await items(page)).find((item) => item.kind === "job" && item.job?.stuck);
  expect(stuck, "the demo has a stuck job").toBeDefined();
  await page.goto(`/jobs?job=${stuck!.job_id}`);
  const panel = page.getByTestId("rescue-panel");
  await expect(panel).toBeVisible();
  // An agent that said something leads with its words and the reply box; the rescue buttons are
  // folded under them as "Other ways to get it moving" (R10 Fable F2, Opus F3).
  const other = panel.getByRole("button", { name: "Other ways to get it moving" });
  if (await other.isVisible()) {
    await expect(page.getByRole("form", { name: "Message the agent" })).toBeVisible();
    await other.click();
  }
  const actions = panel.getByRole("list", { name: "Rescue actions" });
  await expect(actions).toBeVisible();
  // axe first: it waits for the job sheet's slide-in to finish, so the picture is not mid-fade.
  await expectAccessible(page, testInfo, "rescue panel");
  await page.screenshot({ path: `screenshots/phone-rescue-${width(testInfo)}.png` });
  await actions.getByRole("button", { name: /Interrupt \(Esc\)/ }).click();
  await expect(page.getByText("Sent Esc")).toBeVisible();
  await actions.getByRole("button", { name: /^Nudge/ }).click();
  await expect(page.getByText("Nudged", { exact: true })).toBeVisible();
});

test("the phone terminal: every key bar key on screen", async ({ page }, testInfo) => {
  test.skip(!isPhone(testInfo), "the key bar is the phone's");
  await sizeFor(page, testInfo);
  await page.goto("/agents?session=true");
  const bar = page.getByRole("toolbar", { name: "Terminal keys" });
  await expect(bar).toBeVisible();
  const keys = bar.getByRole("button");
  const count = await keys.count();
  expect(count).toBeGreaterThanOrEqual(12);
  for (let index = 0; index < count; index++) {
    const box = await keys.nth(index).boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= 391 && box.height >= 44, `key ${index} at ${JSON.stringify(box)}`).toBe(true);
  }
  await page.screenshot({ path: `screenshots/phone-terminal-keys-${width(testInfo)}.png` });
});

// R10 Opus B1: after an answer the packet moves to the next decision at once. A reflex double tap
// (or a tap one beat late) must not answer the next item, whose buttons sit where the first were.
test("a double tap answers one decision: the next one's choices are held, and it says what was answered", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const low = (await items(page)).find((item) => item.kind === "approval" && item.approval?.risk === "low");
  expect(low, "the demo has a routine approval").toBeDefined();
  await page.goto(`/decide?item=${encodeURIComponent(low!.id)}`);
  const allow = page.getByTestId("answer-bar").getByRole("button", { name: /^Allow once$/ });
  await expect(allow).toBeVisible();
  const box = (await allow.boundingBox())!;
  const before = (await items(page)).map((item) => item.id);
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  await expect(page).not.toHaveURL(new RegExp(encodeURIComponent(low!.id)));
  // The second tap of the double tap, on the same spot, on the item that just appeared.
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  await expect(page.getByTestId("answered-notice")).toContainText("Allowed");
  await expect(page.getByRole("group", { name: /Confirm/ })).toHaveCount(0);
  await page.waitForTimeout(1_000);
  const after = (await items(page)).map((item) => item.id);
  expect(before.filter((id) => !after.includes(id))).toEqual([low!.id]);
  await expect(page.getByTestId("answer-bar")).toHaveAttribute("data-armed", "true");
});

// R10 Opus B6: a decision that cannot load (the browser offline: TanStack pauses the query, which
// left a skeleton for good) says so and offers Retry.
test("a decision that cannot load says so and offers Retry", async ({ page, context }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const question = (await items(page)).find((item) => item.kind === "question");
  expect(question).toBeDefined();
  const ref = question!.id.split(":")[1];
  await expect(page.locator("[data-now-summary]")).toBeVisible();
  await context.setOffline(true);
  // In-app navigation: a page load would fail outright while offline.
  await page.evaluate((id) => {
    window.history.pushState({}, "", `/decide?item=${encodeURIComponent(id)}`);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, question!.id);
  const alert = page.getByRole("alert").filter({ hasText: ref! });
  await expect(alert).toContainText("offline", { timeout: 10_000 });
  await expect(alert.getByRole("button", { name: "Retry" })).toBeVisible();
  // Back online, the paused fetch resumes by itself and the decision appears.
  await context.setOffline(false);
  await expect(page.getByTestId("answer-bar")).toBeVisible({ timeout: 15_000 });
});

// R10 Fable B2: in the job's own Review tab (the sheet a phone lands on), selecting a line shows
// the Comment bar on screen, not under the file pager or below the fold.
test("a line selected in a job's Review tab shows its Comment bar on screen", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const jobs = ((await (await page.request.get("/api/v1/jobs")).json()) as { data: Array<{ id: string; worktree_branch?: string }> }).data;
  const job = jobs.find((candidate) => candidate.worktree_branch === "job/120-checkout-redesign");
  expect(job, "the demo has the checkout redesign").toBeDefined();
  await page.goto(`/jobs?job=${job!.id}`);
  await page.getByRole("tab", { name: /^Review/ }).first().click();
  const line = page.getByRole("button", { name: /^Line \d+, (old|new) side/ }).first();
  await line.click();
  const bar = page.getByRole("status", { name: "Selected lines" });
  const comment = bar.getByRole("button", { name: /^Comment/ });
  await expect(comment).toBeVisible();
  const box = (await comment.boundingBox())!;
  const height = page.viewportSize()!.height;
  expect(box.y + box.height, `Comment at ${JSON.stringify(box)} in a ${height}px viewport`).toBeLessThanOrEqual(height);
  await comment.click();
  await expect(page.getByRole("textbox").filter({ hasText: "" }).last()).toBeVisible();
});
