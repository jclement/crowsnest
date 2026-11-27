// Real Claude through the browser (`mise run e2e:claude`): structured approvals (DESIGN.md 8.7).
// Job agents run in Claude's `manual` (formerly `default`) permission mode, so a shell command that writes asks for
// permission; crowsnest's PermissionRequest hook turns that into an approval card:
//
//   - allowed: the card appears in the needs-you tray with the command, Allow once, the command
//     runs and the job finishes (any follow-up request, such as a git commit, is allowed from its
//     decision packet as a person would);
//   - denied with a note: Deny with "Note to the agent" from the tray; the agent receives the note
//     (it quotes it in its result) and the command never runs.
//
// The terminal is never touched: the decision travels through the browser alone.
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Locator, type Page } from "@playwright/test";
import type { Instance } from "../support/instance";
import { apiGet, cheapJobs, getJob, makeScratch, realClaudeOnly, removeScratch, startReal, until, type Scratch } from "./rig";

let scratch: Scratch;
let instance: Instance;

interface Approval {
  id: string;
  job_id: string;
  status: string;
  summary: string;
  tool_name: string;
  note?: string;
}

async function pending(page: Page, jobId: string): Promise<Approval[]> {
  return (await apiGet<Approval[]>(page, instance, "/approvals?status=pending")).filter((approval) => approval.job_id === jobId);
}

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

async function firstApproval(page: Page, jobId: string): Promise<Approval> {
  return until(page, instance, jobId, async () => (await pending(page, jobId))[0], 4 * 60_000, "the agent asks for permission");
}

/** The desktop tray, every row shown. */
async function openTray(page: Page): Promise<Locator> {
  const panel = page.getByRole("complementary", { name: "Needs you" });
  await expect(panel).toBeVisible();
  const showAll = panel.getByRole("button", { name: /^Show all/ });
  if (await showAll.isVisible()) await showAll.click();
  return panel;
}

/** Answers every further request of the job from its decision packet; returns how it settles. */
async function settle(page: Page, jobId: string, decision: "Allow once" | "Deny", note = ""): Promise<string> {
  return until(
    page,
    instance,
    jobId,
    async () => {
      const job = await getJob(page, instance, jobId);
      if (["done", "failed", "cancelled"].includes(job.state)) return job.state;
      for (const approval of await pending(page, jobId)) {
        test.info().annotations.push({ type: `follow-up approval (${decision})`, description: approval.summary });
        await page.goto(`${instance.baseURL}/decide?item=${encodeURIComponent(`approval:${approval.id}`)}`);
        const bar = page.getByTestId("answer-bar");
        if (note) await bar.getByLabel(/Note/).fill(note);
        await bar.getByRole("button", { name: decision, exact: true }).click();
        await expect.poll(async () => (await apiGet<Approval>(page, instance, `/approvals/${approval.id}`)).status).not.toBe("pending");
      }
      return undefined;
    },
    5 * 60_000,
    "the job finishes",
  );
}

test.describe.serial("structured approvals with real Claude", () => {
  realClaudeOnly();

  test.beforeAll(async () => {
    // *.txt is ignored, so the agent's output files leave the tree clean and done needs no commit.
    scratch = makeScratch("cn-approve", { ".gitignore": "*.txt\n" });
    instance = await startReal(scratch, { permissionMode: "manual" });
  });

  test.afterAll(async () => {
    await instance?.stop();
    removeScratch(scratch);
  });

  test("a Bash call asks in the tray; Allow once runs it and the job finishes", async ({ page }) => {
    test.setTimeout(12 * 60_000);
    const id = await giveJob(
      page,
      "Use your Bash tool to run exactly this shell command: echo approved-run > d.txt\nDo not write the file any other way. When the command has run, call your crowsnest done tool with a one-line result.",
    );
    const approval = await firstApproval(page, id);
    expect(approval.tool_name).toBe("Bash");
    // While it waits, the job says it needs you for a tool approval.
    await expect.poll(async () => (await getJob(page, instance, id)).cause).toBe("approval-tool");

    await page.goto(`${instance.baseURL}/jobs?job=${id}`);
    const tray = await openTray(page);
    await tray.getByRole("button", { name: /echo approved-run/, expanded: false }).click();
    const card = tray.locator(`[data-approval="${approval.id}"]`);
    await expect(card.getByLabel("Command")).toContainText("echo approved-run > d.txt");
    await page.screenshot({ path: "screenshots/e2e-claude-approval-1440x900.png" });
    await card.getByRole("button", { name: "Allow once" }).click();
    await expect.poll(async () => (await apiGet<Approval>(page, instance, `/approvals/${approval.id}`)).status).toBe("allowed");
    await expect(tray.locator(`[data-approval="${approval.id}"]`)).toHaveCount(0);

    expect(await settle(page, id, "Allow once")).toBe("done");
    expect(readFileSync(join(scratch.repo, "d.txt"), "utf8")).toContain("approved-run");
    await page.goto(`${instance.baseURL}/jobs?job=${id}`);
    await expect(page.getByRole("region", { name: "Selected job" }).getByText("Done", { exact: true }).first()).toBeVisible();
  });

  test("a Bash call denied from the tray with a note: the agent gets the note, nothing runs", async ({ page }) => {
    test.setTimeout(12 * 60_000);
    const note = "Not now: the word for today is PURPLE-42";
    const id = await giveJob(
      page,
      "Use your Bash tool to run exactly this shell command: echo second > e.txt\nIf it is refused, do not retry it and do not write the file another way: call your crowsnest done tool with result_md set to the exact refusal message you received, word for word.",
    );
    const approval = await firstApproval(page, id);
    await page.goto(`${instance.baseURL}/jobs?job=${id}`);
    const tray = await openTray(page);
    await tray.getByRole("button", { name: /echo second/, expanded: false }).click();
    const card = tray.locator(`[data-approval="${approval.id}"]`);
    await card.getByLabel(/Note to the agent/).fill(note);
    await card.getByRole("button", { name: "Deny" }).click();
    await expect.poll(async () => (await apiGet<Approval>(page, instance, `/approvals/${approval.id}`)).status).toBe("denied");
    expect((await apiGet<Approval>(page, instance, `/approvals/${approval.id}`)).note).toBe(note);

    // Anything else it asks for is denied too, so nothing is written another way.
    const state = await settle(page, id, "Deny", note);
    expect(["done", "failed"]).toContain(state);
    const job = await getJob(page, instance, id);
    expect(job.result_md, "the agent quotes the note").toContain("PURPLE-42");
    expect(existsSync(join(scratch.repo, "e.txt"))).toBe(false);
    await page.goto(`${instance.baseURL}/jobs?job=${id}`);
    await expect(page.getByRole("region", { name: "Selected job" })).toContainText("PURPLE-42");
  });
});
