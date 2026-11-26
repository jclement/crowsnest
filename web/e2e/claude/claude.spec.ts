// Real Claude through the browser (`mise run e2e:claude`; opt-in, a few cents of haiku): the
// journeys the hermetic suite can only script, against a real orchestrator and real job agents.
//
//   - chat: a prompt from the Chat box gets a rendered reply;
//   - a job given from the Jobs box (directory: a scratch git repo) goes running → done on the
//     board, and its detail shows the result and what was verified;
//   - a job told to ask before it acts: the question appears in the Inbox, is answered there, and
//     the job finishes with the answer applied;
//   - a job told to stop without declaring anything: the Stop hook's nudge shows on its timeline;
//   - `crowsnest ask --wait` run by the orchestrator from its pane blocks until the Inbox answer
//     arrives and prints it (the blocking path, never before proven against real Claude).
//
// Along the way it saves real Claude screens (ANSI, from the screen API) to
// e2e/claude/captured/ for the Go dialog and draft detection fixtures.
//
// Safety: one throwaway crowsnest-test-* herdr session, stopped and deleted at the end; every
// HERDR_* variable removed from the child's environment. Claude's login lives in the real HOME,
// so this suite (alone) keeps it (`realHome`), exactly as `mise run test:claude` does. Everything
// Claude touches is under a scratch directory inside this worktree, which Claude already trusts,
// and is removed at the end. Jobs use the tester role so no new herdr worktree (an untrusted
// folder) is made. The Jobs form has no model field, so the test adds "haiku" to the browser's
// own POST /jobs (cost control; the request is otherwise the page's).
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { BINARY, scrubbedEnv, startInstance, type Instance } from "../support/instance";

const CAPTURED = resolve(import.meta.dirname, "captured");
const MODEL = process.env.CROWSNEST_E2E_MODEL ?? "haiku";

const missing = ["herdr", "claude", "git"].filter((tool) => spawnSync("sh", ["-c", `command -v ${tool}`], { env: scrubbedEnv() }).status !== 0);

let instance: Instance;
let scratch: string;
let repo: string;

interface JobView {
  id: string;
  state: string;
  pane_id: string;
  result_md: string;
  timeline: Array<{ kind: string; summary: string }>;
  questions: Array<{ id: string; status: string }>;
}

async function getJob(page: Page, id: string): Promise<JobView> {
  return ((await (await page.request.get(`${instance.baseURL}/api/v1/jobs/${id}`)).json()) as { data: JobView }).data;
}

/** Saves the ANSI screen of a pane, for the Go fixtures. */
async function capture(page: Page, paneId: string, name: string): Promise<string> {
  const screen = ((await (await page.request.get(`${instance.baseURL}/api/v1/agents/${encodeURIComponent(paneId)}/screen`)).json()) as { data: { ansi: string; text: string } }).data;
  mkdirSync(CAPTURED, { recursive: true });
  writeFileSync(join(CAPTURED, `${name}.ansi`), screen.ansi);
  return screen.text;
}

interface ScreenView {
  text: string;
  draft: { present: boolean; text: string };
  dialog: { present: boolean };
}

async function screenOf(page: Page, paneId: string): Promise<ScreenView> {
  return ((await (await page.request.get(`${instance.baseURL}/api/v1/agents/${encodeURIComponent(paneId)}/screen`)).json()) as { data: ScreenView }).data;
}

/** A herdr command aimed at this instance's throwaway session only. */
function herdrIn(args: string[]) {
  const result = spawnSync("herdr", ["--session", instance.session, ...args], { env: instance.env, encoding: "utf8", timeout: 15_000 });
  if (result.status !== 0) throw new Error(`herdr ${args.join(" ")}: ${result.stderr}`);
}

/** Makes every POST /jobs from the page ask for the cheap model. */
async function cheapJobs(page: Page) {
  await page.route("**/api/v1/jobs", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    const body = { ...(route.request().postDataJSON() as Record<string, unknown>), model: MODEL };
    await route.continue({ postData: JSON.stringify(body) });
  });
}

/** Gives a job from the Jobs box with the scratch repo as its directory; returns its id. */
async function giveJob(page: Page, brief: string, doneWhen?: string): Promise<string> {
  await cheapJobs(page);
  await page.goto(`${instance.baseURL}/`);
  await page.getByLabel("What needs doing?").fill(brief);
  await page.getByRole("button", { name: /^To / }).click();
  await page.getByLabel(/^Where/).fill(repo);
  await page.getByLabel("Role").selectOption("tester");
  if (doneWhen) await page.getByLabel("Done when").fill(doneWhen);
  const created = page.waitForResponse((response) => response.url().endsWith("/api/v1/jobs") && response.request().method() === "POST");
  await page.getByRole("button", { name: "Give job" }).click();
  const response = await created;
  expect(response.status()).toBe(201);
  return ((await response.json()) as { data: { id: string } }).data.id;
}

/**
 * Plays the human at any terminal dialog an agent is held at. Claude 2.1.332's folder-trust prompt
 * is an unnumbered list with "No, exit" preselected, which a dialog card cannot press (by design
 * crowsnest never answers it), so it is answered in the browser terminal as a person would: Down,
 * Enter. A numbered permission dialog is answered from its card in the Inbox, only on a "Yes".
 */
async function clearDialogs(page: Page, name: string) {
  const fleet = ((await (await page.request.get(`${instance.baseURL}/api/v1/fleet`)).json()) as { data: { agents: Array<{ pane_id: string; needs_you: boolean }> } }).data;
  for (const agent of fleet.agents.filter((entry) => entry.needs_you)) {
    const text = await capture(page, agent.pane_id, `${name}-dialog-${agent.pane_id.replace(":", "-")}`);
    test.info().annotations.push({ type: "dialog seen", description: text.split("\n").filter(Boolean).slice(-8).join(" / ") });
    if (text.includes("Yes, I trust this folder")) {
      await page.goto(`${instance.baseURL}/fleet?agent=${encodeURIComponent(agent.pane_id)}`);
      const detail = page.getByRole("article", { name: /^Agent / });
      await detail.getByRole("tab", { name: "Terminal" }).click();
      await expect(detail.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
      await detail.locator("[data-terminal]").click();
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("Enter");
      await page.waitForTimeout(2_000);
      continue;
    }
    await page.goto(`${instance.baseURL}/inbox?q=${encodeURIComponent(`dialog:${agent.pane_id}`)}`);
    const card = page.getByRole("region", { name: "Selected item" }).getByLabel(/^Dialog from /);
    const yes = card.getByRole("list", { name: "Options" }).getByRole("button", { name: /^1\s*Yes/ });
    if (await yes.isVisible().catch(() => false)) await yes.click();
  }
}

test.describe.serial("real Claude through the browser", () => {
  test.skip(process.env.CROWSNEST_E2E_CLAUDE !== "1", "real-Claude suite: set CROWSNEST_E2E_CLAUDE=1 (mise run e2e:claude); it costs a few cents");
  test.skip(missing.length > 0, `not installed: ${missing.join(", ")}`);

  test.beforeAll(async () => {
    test.setTimeout(3 * 60_000);
    scratch = mkdtempSync(join(import.meta.dirname, ".scratch-"));
    repo = join(scratch, "repo");
    mkdirSync(repo);
    execFileSync("git", ["init", "-q", "-b", "main"], { cwd: repo });
    writeFileSync(join(repo, "README.md"), "# e2e scratch repo\n");
    execFileSync("git", ["-c", "user.email=e2e@example.invalid", "-c", "user.name=e2e", "add", "."], { cwd: repo });
    execFileSync("git", ["-c", "user.email=e2e@example.invalid", "-c", "user.name=e2e", "commit", "-q", "-m", "init"], { cwd: repo });
    const orchestratorDir = join(scratch, "orchestrator");
    mkdirSync(orchestratorDir);
    instance = await startInstance({ demo: false, orchestrator: true, realHome: true, args: ["--model", MODEL, "--dir", orchestratorDir, "--poke-interval", "0"] });
  });

  test.afterAll(async () => {
    await instance?.stop();
    if (scratch) rmSync(scratch, { recursive: true, force: true });
  });

  test("chat: the orchestrator answers a prompt and the reply renders", async ({ page }) => {
    await page.goto(`${instance.baseURL}/chat`);
    await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 120_000 });
    await page.getByRole("textbox", { name: "Message to the orchestrator" }).fill("Reply with exactly one word: pong. Do not use any tools.");
    await page.getByRole("button", { name: "Send" }).click();
    const reply = page.getByRole("article", { name: "Reply" }).last();
    await expect(reply).toContainText(/pong/i, { timeout: 120_000 });
    await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 60_000 });
    const orchestrator = ((await (await page.request.get(`${instance.baseURL}/api/v1/orchestrator`)).json()) as { data: { pane_id: string } }).data;
    // An idle Claude prompt, often with ghost-text suggestions: a real fixture for draft detection.
    await capture(page, orchestrator.pane_id, "claude-idle-after-reply");
    await expect.poll(async () => (await screenOf(page, orchestrator.pane_id)).draft.present).toBe(false);

    // A draft typed at the terminal (herdr types it, as a person at the TUI would): the screen API
    // must see it, and a send must stop for it (409 DRAFT) until forced.
    herdrIn(["pane", "send-text", orchestrator.pane_id, "half-typed draft from the terminal"]);
    await expect.poll(async () => (await screenOf(page, orchestrator.pane_id)).draft, { timeout: 15_000 }).toMatchObject({ present: true, text: "half-typed draft from the terminal" });
    await capture(page, orchestrator.pane_id, "claude-typed-draft");
    const refused = await page.request.post(`${instance.baseURL}/api/v1/agents/orchestrator/prompt`, {
      headers: { Origin: instance.baseURL, "X-Crowsnest": "1", "Content-Type": "application/json" },
      data: { text: "this must not be typed after the draft" },
    });
    expect(refused.status()).toBe(409);
    expect(((await refused.json()) as { error: { code: string } }).error.code).toBe("DRAFT");
    // Clear it, as the person would, and the box is empty again.
    herdrIn(["pane", "send-keys", orchestrator.pane_id, ...Array.from({ length: 40 }, () => "backspace")]);
    await expect.poll(async () => (await screenOf(page, orchestrator.pane_id)).draft.present, { timeout: 15_000 }).toBe(false);
  });

  test("a job from the Jobs box runs to done; its result and evidence show", async ({ page }) => {
    const id = await giveJob(
      page,
      "Create a file named hello.txt in the current directory containing exactly the text: hi from crowsnest\nThen check it with cat, and call done with what you did, what you verified and what you did not.",
      "hello.txt exists with that text",
    );
    await expect(page).toHaveURL(new RegExp(`job=${id}`));
    const card = () => page.getByRole("button", { name: /Create a file named hello\.txt/ });
    await expect(card()).toBeVisible();
    await expect.poll(async () => (await getJob(page, id)).state, { timeout: 4 * 60_000, intervals: [3_000] }).toMatch(/running|done|needs_you/);
    const running = await getJob(page, id);
    if (running.pane_id) await capture(page, running.pane_id, "claude-job-working");
    await expect
      .poll(async () => {
        await clearDialogs(page, "job-done");
        return (await getJob(page, id)).state;
      }, { timeout: 5 * 60_000, intervals: [5_000] })
      .toBe("done");
    await page.goto(`${instance.baseURL}/?job=${id}`);
    const detail = page.getByRole("region", { name: "Selected job" });
    await expect(detail.getByText("Done", { exact: true }).first()).toBeVisible();
    await expect(detail.getByRole("region", { name: "Verified", exact: true })).toBeVisible();
    await expect(page.getByRole("region", { name: "Finished today" }).getByRole("button", { name: /Create a file named hello\.txt/ })).toBeVisible();
    expect(readFileSync(join(repo, "hello.txt"), "utf8")).toContain("hi from crowsnest");
  });

  test("a job asks in the Inbox, the answer is applied, and it finishes", async ({ page }) => {
    const id = await giveJob(
      page,
      "Use the crowsnest ask tool (not a terminal question) to ask me which colour to write, kind choice, options red and blue, recommend blue. Do not wait on it: while you wait, do nothing else. When the answer arrives as a message starting [crowsnest answer, write that colour (one word) to colour.txt in the current directory, check it, and call done.",
    );
    let questionId = "";
    await expect
      .poll(async () => {
        await clearDialogs(page, "job-ask");
        const job = await getJob(page, id);
        questionId = job.questions.find((question) => question.status === "open")?.id ?? "";
        return questionId;
      }, { timeout: 4 * 60_000, intervals: [5_000] })
      .not.toBe("");
    await page.goto(`${instance.baseURL}/inbox?q=${questionId}`);
    const detail = page.getByRole("region", { name: "Selected item" });
    await expect(detail.getByRole("button", { name: /blue/i })).toContainText("Recommended");
    await detail.getByRole("button", { name: /^red/i }).click();
    await expect(detail.getByRole("region", { name: "Outcome" })).toContainText(/red/i);
    await expect
      .poll(async () => {
        await clearDialogs(page, "job-ask-after");
        return (await getJob(page, id)).state;
      }, { timeout: 5 * 60_000, intervals: [5_000] })
      .toBe("done");
    expect(readFileSync(join(repo, "colour.txt"), "utf8").trim().toLowerCase()).toBe("red");
    await page.goto(`${instance.baseURL}/?job=${id}`);
    await expect(page.getByRole("region", { name: "Selected job" }).getByRole("listitem").filter({ hasText: /→ red/i })).toBeVisible();
  });

  test("a job told to stop without declaring is nudged, and the nudge shows on its timeline", async ({ page }) => {
    const id = await giveJob(page, "Reply with the single word ok and end your turn right away. Do not call any tool, not even a crowsnest one.");
    await expect
      .poll(async () => {
        await clearDialogs(page, "job-nudge");
        return (await getJob(page, id)).timeline.some((event) => event.kind === "nudge");
      }, { timeout: 4 * 60_000, intervals: [5_000] })
      .toBe(true);
    await page.goto(`${instance.baseURL}/?job=${id}`);
    const detail = page.getByRole("region", { name: "Selected job" });
    await expect(detail.getByRole("list", { name: "Timeline" })).toContainText(/Nudged/);
    // Stop spending: cancel it from the UI.
    await detail.getByRole("button", { name: "Cancel job" }).click();
    await detail.getByRole("button", { name: "Cancel the job" }).click();
    await expect(detail.getByText("Cancelled", { exact: true }).first()).toBeVisible();
  });

  test("`crowsnest ask --wait` from the orchestrator's pane blocks until the Inbox answers", async ({ page }) => {
    const question = join(scratch, "question.json");
    writeFileSync(question, JSON.stringify({ title: "Go ahead with the e2e check?", kind: "confirm", recommend: "yes", body_md: "Asked with `crowsnest ask --wait` by the e2e suite." }));
    await page.goto(`${instance.baseURL}/chat`);
    await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 120_000 });
    await page
      .getByRole("textbox", { name: "Message to the orchestrator" })
      .fill(`Run exactly this shell command with the Bash tool and wait for it to finish (it blocks until I answer; allow it up to 10 minutes): ${BINARY} ask --file ${question} --wait\nThen tell me the exact JSON it printed.`);
    await page.getByRole("button", { name: "Send" }).click();
    let questionId = "";
    await expect
      .poll(async () => {
        await clearDialogs(page, "ask-wait");
        const open = ((await (await page.request.get(`${instance.baseURL}/api/v1/questions?status=open`)).json()) as { data: Array<{ id: string; title: string }> }).data;
        questionId = open.find((entry) => entry.title === "Go ahead with the e2e check?")?.id ?? "";
        return questionId;
      }, { timeout: 4 * 60_000, intervals: [3_000] })
      .not.toBe("");
    // The orchestrator is blocked in the command meanwhile.
    await expect(page.getByTestId("chat-state")).toHaveText("Working");
    await page.goto(`${instance.baseURL}/inbox?q=${questionId}`);
    await page.getByRole("region", { name: "Selected item" }).getByRole("textbox", { name: /Note/ }).fill("yes, from the browser");
    await page.getByRole("region", { name: "Selected item" }).getByRole("button", { name: /^Yes/ }).click();
    const outcome = page.getByRole("region", { name: "Outcome" });
    await expect(outcome).toContainText("Yes");
    // A waiting asker gets it through the long-poll, not typed into its pane.
    await expect(outcome).toContainText("The agent was waiting and got the answer at once.");
    await page.goto(`${instance.baseURL}/chat`);
    await expect(page.getByRole("article", { name: "Reply" }).last()).toContainText(/answered/i, { timeout: 180_000 });
    await expect(page.getByRole("article", { name: "Reply" }).last()).toContainText(/yes, from the browser|"yes"/i);
    expect(existsSync(question)).toBe(true);
  });
});
