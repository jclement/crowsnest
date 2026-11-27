// Real Claude through the browser (`mise run e2e:claude`): the supervisor and the human's side of
// it, with tester jobs in a scratch repository (DESIGN.md 6.3, 6.13, 6.19, 23.1).
//
//   - a question answered from its decision packet on a phone (390x844): the job asks with the
//     ask tool, Now's queue opens the packet, the answer goes in from the answer bar, and the
//     answer's receipt reaches "Received" on the packet without a reload; the job applies it;
//   - an undeclared stop: the Stop hook's nudge shows on the job's timeline;
//   - a job that ends its turn asking in prose is refused, and asks a real question instead,
//     which reaches the Inbox;
//   - pause from the header: a job given while paused is held and says why; Resume, and it runs
//     to done.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import type { Instance } from "../support/instance";
import { apiGet, attention, cheapJobs, getJob, logSpend, makeScratch, realClaudeOnly, removeScratch, startReal, until, waitForJob, type Scratch } from "./rig";

let scratch: Scratch;
let instance: Instance;

/** Gives a tester job in the scratch repository from the Jobs board; returns its id. */
async function giveJob(page: Page, brief: string): Promise<string> {
  await cheapJobs(page);
  await page.goto(`${instance.baseURL}/jobs`);
  await page.getByLabel("What needs doing?").fill(brief);
  await page.getByRole("button", { name: /^To / }).click();
  await page.getByLabel(/^Where/).fill(scratch.repo);
  await page.getByLabel("Role").selectOption("tester");
  const created = page.waitForResponse((response) => response.url().endsWith("/api/v1/jobs") && response.request().method() === "POST");
  await page.getByRole("button", { name: "Give job" }).click();
  const response = await created;
  expect(response.status()).toBe(201);
  return ((await response.json()) as { data: { id: string } }).data.id;
}

async function cancelJob(page: Page, id: string) {
  const job = await getJob(page, instance, id);
  if (["done", "failed", "cancelled"].includes(job.state)) return;
  await page.request.post(`${instance.baseURL}/api/v1/jobs/${id}/cancel`, { headers: { Origin: instance.baseURL, "X-Crowsnest": "1" } });
}

async function openQuestion(page: Page, jobId: string, timeout: number): Promise<{ id: string; title: string; body_md: string }> {
  return until(
    page,
    instance,
    jobId,
    async () => (await apiGet<Array<{ id: string; title: string; body_md: string; job_id: string }>>(page, instance, "/questions?status=open")).find((question) => question.job_id === jobId),
    timeout,
    "the job asks a question",
  );
}

test.describe.serial("the supervisor and the human, with real Claude", () => {
  realClaudeOnly();

  test.beforeAll(async () => {
    // *.txt is ignored, so the agents' output files leave the tree clean and done needs no commit.
    scratch = makeScratch("cn-supervise", { ".gitignore": "*.txt\n" });
    instance = await startReal(scratch);
  });

  test.afterAll(async () => {
    await logSpend(instance, "supervise");
    await instance?.stop();
    removeScratch(scratch);
  });

  test.describe("on a phone", () => {
    test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });

    test("a question answered from its decision packet; the receipt reaches Received", async ({ page }) => {
      test.setTimeout(12 * 60_000);
      const id = await giveJob(
        page,
        "Use the crowsnest ask tool (not a terminal question) to ask me which colour to write, kind choice, options red and blue, recommend blue. Do not wait on it: while you wait, do nothing else. When the answer arrives as a message starting [crowsnest answer, write that colour (one word) to colour.txt in the current directory, check it, and call done.",
      );
      const itemId = await until(
        page,
        instance,
        id,
        async () => (await attention(page, instance)).find((item) => item.kind === "question" && item.job_id === id)?.id,
        4 * 60_000,
        "the question reaches the attention queue",
      );

      // From Now's queue, as on the phone: the row, then its decision packet.
      await page.goto(`${instance.baseURL}/`);
      const row = page.getByRole("region", { name: "Needs you" }).locator(`[data-tray-item="${itemId}"]`);
      await expect(row).toBeVisible();
      await row.getByRole("button", { expanded: false }).first().click();
      await row.getByRole("button", { name: /Review/ }).first().click();
      await expect(page).toHaveURL(/\/decide\?item=/);
      const packet = page.getByTestId("decision-packet");
      await expect(packet.getByRole("heading", { level: 1 })).toContainText(/colou?r/i);
      await expect(packet.getByRole("region", { name: "What each choice does" })).toBeVisible();
      const bar = page.getByTestId("answer-bar");
      await expect(bar.getByRole("button", { name: /blue/i })).toBeVisible();
      await page.screenshot({ path: "screenshots/e2e-claude-decide-390x844.png" });
      await bar.getByLabel(/Note/).fill("red, for the e2e run");
      await bar.getByRole("group", { name: "Choices" }).getByRole("button", { name: /^red/i }).click();
      const confirm = page.getByRole("button", { name: /^Yes, / });
      if (await confirm.isVisible().catch(() => false)) await confirm.click();
      await expect(page).not.toHaveURL(new RegExp(encodeURIComponent(itemId)));

      // The same link now shows the outcome; the receipt follows the answer into the agent's
      // conversation and turns Received on this page, live.
      await page.goto(`${instance.baseURL}/decide?item=${encodeURIComponent(itemId)}`);
      await expect(page.getByRole("status").filter({ hasText: "Already answered." })).toBeVisible();
      const outcome = page.getByRole("region", { name: "Outcome" });
      await expect(outcome).toContainText(/red/i);
      // exact: the manual's "Help: answer receipts" button sits beside it.
      await expect(outcome.getByLabel("Answer receipt", { exact: true })).toContainText("Received", { timeout: 2 * 60_000 });
      await page.screenshot({ path: "screenshots/e2e-claude-decide-received-390x844.png" });

      const done = await waitForJob(page, instance, id, (job) => ["done", "failed", "cancelled"].includes(job.state), 5 * 60_000, "the job applies the answer");
      expect(done.state).toBe("done");
      expect(readFileSync(join(scratch.repo, "colour.txt"), "utf8").trim().toLowerCase()).toBe("red");
    });
  });

  test("a job told to stop without declaring is nudged, and the nudge shows on its timeline", async ({ page }) => {
    test.setTimeout(8 * 60_000);
    const id = await giveJob(page, "Reply with the single word ok and end your turn right away. Do not call any tool, not even a crowsnest one.");
    await waitForJob(page, instance, id, (job) => job.timeline.some((event) => event.kind === "nudge") || ["done", "failed", "cancelled"].includes(job.state), 4 * 60_000, "the stop is nudged");
    await page.goto(`${instance.baseURL}/jobs?job=${id}`);
    const detail = page.getByRole("region", { name: "Selected job" });
    await expect(detail.getByRole("list", { name: "Timeline" })).toContainText(/Nudged/);
    // Stop spending: cancel it from the UI.
    await detail.getByRole("button", { name: "Cancel job" }).click();
    await detail.getByRole("button", { name: "Cancel the job" }).click();
    await expect(detail.getByText("Cancelled", { exact: true }).first()).toBeVisible();
  });

  test("a job that asks in prose is refused, and asks a real question that reaches the Inbox", async ({ page }) => {
    test.setTimeout(14 * 60_000);
    const brief =
      "This is a test of what crowsnest does with a question asked in plain text, so asking it any other way would spoil it. " +
      "Your first reply must be exactly this one sentence, written as plain text, with no tool call before it (not the ask tool, not any other), " +
      "and then end your turn: Should I write hello or hi in the greeting file? " +
      "After that, if crowsnest sends you a message about it, do what that message says.";
    // The subject is a turn that ends with a question in prose. Haiku sometimes goes straight to
    // the ask tool instead; then the situation never arose, and a fresh job tries once more (the
    // assertion is never relaxed: a prose question must be refused and asked for real).
    for (let attempt = 1; ; attempt++) {
      const id = await giveJob(page, brief);
      const question = await openQuestion(page, id, 5 * 60_000);
      const job = await getJob(page, instance, id);
      const refused = job.timeline.some((event) => event.kind === "nudge" && event.detail?.rule === "prose_question");
      const story = job.timeline.map((event) => `${event.kind}: ${event.summary}`).join(" | ");
      if (!refused && attempt < 2 && !job.timeline.some((event) => event.kind === "nudge")) {
        test.info().annotations.push({ type: "no prose turn (asked with the tool at once); trying a fresh job", description: story });
        console.log(`prose journey: ${id} asked with the tool at once (no prose turn); trying a fresh job`);
        await cancelJob(page, id);
        await page.request.delete(`${instance.baseURL}/api/v1/questions/${question.id}`, { headers: { Origin: instance.baseURL, "X-Crowsnest": "1" } });
        continue;
      }
      expect(refused, `the stop was refused as a prose question: ${story}`).toBe(true);
      expect(`${question.title} ${question.body_md}`.toLowerCase()).toContain("hello");
      await page.goto(`${instance.baseURL}/jobs?job=${id}`);
      const detail = page.getByRole("region", { name: "Selected job" });
      await expect(detail.getByRole("list", { name: "Timeline" })).toContainText(/prose/i);
      await page.goto(`${instance.baseURL}/inbox?q=${question.id}`);
      await expect(page.getByRole("region", { name: "Selected item" })).toContainText(/hello/i);
      await cancelJob(page, id);
      return;
    }
  });

  test("pause from the header holds a new job; Resume, and it runs to done", async ({ page }) => {
    test.setTimeout(10 * 60_000);
    await page.goto(`${instance.baseURL}/jobs`);
    await page.getByRole("button", { name: "Account" }).click();
    await page.getByRole("menuitem", { name: "Pause automation…" }).click();
    const sheet = page.getByRole("dialog", { name: "Pause automation" });
    await sheet.getByLabel(/Reason/).fill("e2e hold");
    await sheet.getByRole("button", { name: "Pause", exact: true }).click();
    const banner = page.getByRole("status", { name: "Automation paused" });
    await expect(banner).toContainText("e2e hold");

    const id = await giveJob(page, "Write the word resumed to resumed.txt in the current directory, check it with cat, and call your crowsnest done tool with a one-line result.");
    await expect(page).toHaveURL(new RegExp(`job=${id}`));
    const detail = page.getByRole("region", { name: "Selected job" });
    await expect(detail.getByRole("alert")).toContainText("Waiting to start");
    await expect(detail.getByRole("alert")).toContainText(/paused/);
    // Held: still queued a while later, no agent.
    await page.waitForTimeout(8_000);
    const held = await getJob(page, instance, id);
    expect(held.state).toBe("queued");
    expect(held.pane_id).toBe("");

    await banner.getByRole("button", { name: "Resume" }).click();
    await expect(banner).toBeHidden();
    // It is launched on its own (the hold is gone at once; the launch is crowsnest's wait) and
    // runs to done; the open job follows it there without a reload.
    await expect(detail.getByRole("alert").filter({ hasText: "Waiting to start" })).toHaveCount(0, { timeout: 60_000 });
    const done = await waitForJob(page, instance, id, (job) => ["done", "failed", "cancelled"].includes(job.state), 5 * 60_000, "the resumed job finishes");
    expect(done.state).toBe("done");
    expect(done.timeline.some((event) => /queued → running/.test(event.summary))).toBe(true);
    expect(readFileSync(join(scratch.repo, "resumed.txt"), "utf8")).toContain("resumed");
    if (!page.url().includes(`job=${id}`)) await page.goto(`${instance.baseURL}/jobs?job=${id}`); // a trust prompt answered on the way moved the page
    await expect(detail.getByText("Done", { exact: true }).first()).toBeVisible({ timeout: 30_000 });
  });
});
