// Real Claude through the browser (`mise run e2e:claude`; opt-in, a few cents of haiku): the
// orchestrator's journeys, against a real orchestrator in a throwaway herdr session.
//
//   - chat: a prompt from the Chat box gets a rendered reply; a draft typed at the terminal is
//     seen by the screen API and stops a send (409 DRAFT) until it is cleared;
//   - `crowsnest ask --wait` run by the orchestrator from its pane blocks until the Inbox answer
//     arrives and prints it (the blocking path).
//
// The job journeys live beside it: land.spec.ts (Now → check → review → Land, and a failed check
// fixed), supervise.spec.ts (the decision packet on a phone with the answer receipt, a nudge, a
// prose question, pause), approvals.spec.ts (the tray's Allow and Deny), lead.spec.ts (a project
// with a lead), firstrun.spec.ts (a true first run). Shared helpers: rig.ts.
//
// Along the way it saves real Claude screens (ANSI, from the screen API) to
// e2e/claude/captured/ for the Go dialog and draft detection fixtures.
//
// Safety: one throwaway crowsnest-test-* herdr session, stopped and deleted at the end; every
// HERDR_* variable removed from the child's environment. Claude's login lives in the real HOME,
// so this suite (alone) keeps it (`realHome`), exactly as `mise run test:claude` does. Everything
// Claude touches is under a scratch directory inside this worktree, which Claude already trusts,
// and is removed at the end.
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { BINARY, type Instance } from "../support/instance";
import { makeScratch, realClaudeOnly, removeScratch, startReal, trustFolders, type Scratch } from "./rig";

const CAPTURED = resolve(import.meta.dirname, "captured");

let instance: Instance;
let scratch: Scratch;

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

/** On a failure, what the orchestrator was doing: its newest messages, its screen, the journal. */
async function explainOrchestrator(page: Page) {
  const get = async (path: string) => (await page.request.get(`${instance.baseURL}/api/v1${path}`)).text();
  const messages = await get("/agents/orchestrator/messages?limit=12");
  const status = await get("/orchestrator");
  const orchestrator = (JSON.parse(status) as { data: { pane_id: string } }).data;
  const view = orchestrator.pane_id ? await screenOf(page, orchestrator.pane_id) : null;
  const screen = view ? `${view.text}\n--- draft verdict\n${JSON.stringify(view.draft)}` : "(no pane)";
  if (orchestrator.pane_id) await capture(page, orchestrator.pane_id, "orchestrator-at-failure");
  const composer = await page.getByRole("alert").allInnerTexts().catch(() => []);
  test.info().annotations.push({ type: "alerts on the page", description: composer.join(" | ") });
  const journal = await get("/journal");
  const body = `--- orchestrator\n${status}\n--- messages\n${messages}\n--- screen\n${screen}\n--- journal\n${journal}`;
  await test.info().attach("orchestrator.txt", { body, contentType: "text/plain" });
  console.log(body.slice(-6000));
}

test.describe.serial("the orchestrator, with real Claude", () => {
  realClaudeOnly();

  test.beforeAll(async () => {
    test.setTimeout(3 * 60_000);
    scratch = makeScratch("cn-orch");
    instance = await startReal(scratch, { orchestrator: true });
  });

  test.afterAll(async () => {
    await instance?.stop();
    removeScratch(scratch);
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

  test("`crowsnest ask --wait` from the orchestrator's pane blocks until the Inbox answers", async ({ page }) => {
    const question = join(scratch.dir, "question.json");
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
        if (!questionId) await trustFolders(page, instance);
        const open = ((await (await page.request.get(`${instance.baseURL}/api/v1/questions?status=open`)).json()) as { data: Array<{ id: string; title: string }> }).data;
        questionId = open.find((entry) => entry.title === "Go ahead with the e2e check?")?.id ?? "";
        return questionId;
      }, { timeout: 4 * 60_000, intervals: [3_000] })
      .not.toBe("")
      .catch(async (error: unknown) => {
        await explainOrchestrator(page);
        throw error;
      });
    // The orchestrator is blocked in the command meanwhile.
    await expect(page.getByTestId("chat-state")).toHaveText("Working");
    await page.goto(`${instance.baseURL}/inbox?q=${questionId}`);
    await page.getByRole("region", { name: "Selected item" }).getByRole("textbox", { name: /Note/ }).fill("yes, from the browser");
    await page.getByRole("region", { name: "Selected item" }).getByRole("button", { name: /^Yes/ }).click();
    const outcome = page.getByRole("region", { name: "Outcome" });
    await expect(outcome).toContainText("Yes");
    // A waiting asker gets it through the long-poll, not typed into its pane: its own call
    // returning the answer is the receipt (DESIGN.md 6.19), Received at once.
    await expect(outcome.getByLabel("Answer receipt")).toContainText("Received");
    await expect(outcome).not.toContainText("Typed into the agent");
    await page.goto(`${instance.baseURL}/chat`);
    await expect(page.getByRole("article", { name: "Reply" }).last()).toContainText(/answered/i, { timeout: 180_000 });
    await expect(page.getByRole("article", { name: "Reply" }).last()).toContainText(/yes, from the browser|"yes"/i);
    expect(existsSync(question)).toBe(true);
  });
});
