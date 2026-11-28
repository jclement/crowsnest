// A true first run, end to end, with real herdr and real Claude (haiku; part of the opt-in
// `mise run e2e:claude`): crowsnest starts with no login and no data, prints a setup
// link; the browser opens it, chooses a password, lands on the first-run checklist (already green
// on a machine with herdr and a signed-in Claude), presses Start; the orchestrator starts in a
// folder Claude has never seen, so Claude asks its one-time folder-trust question, which the
// checklist shows as a card with its buttons; answering it there lets the orchestrator come up,
// the browser goes to Chat, and the orchestrator's greeting arrives.
//
// Safety: a throwaway crowsnest-test-* herdr session (stopped and deleted at the end), a throwaway
// data directory, every HERDR_* variable removed from the child. Claude's login lives in the real
// HOME, so (as in claude.spec.ts) the real HOME is used: answering the trust question records the
// scratch folder in ~/.claude.json, and the test removes exactly that record at the end.
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, realpathSync, rmSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { scrubbedEnv, startInstance, type Instance } from "../support/instance";
import { completeSetup } from "../support/setup";
import { forgetScratch } from "./rig";

const MODEL = process.env.CROWSNEST_E2E_MODEL ?? "haiku";
const missing = ["herdr", "claude"].filter((tool) => spawnSync("sh", ["-c", `command -v ${tool}`], { env: scrubbedEnv() }).status !== 0);

let instance: Instance;
let scratch: string;

test.skip(missing.length > 0, `needs ${missing.join(", ")} on PATH`);

test.beforeAll(async () => {
  scratch = mkdtempSync("/tmp/cnfr-");
  const dir = join(scratch, "orchestrator");
  mkdirSync(dir);
  instance = await startInstance({ demo: false, setup: true, orchestrator: true, realHome: true, args: ["--model", MODEL, "--dir", dir, "--open", "never"] });
});

test.afterAll(async () => {
  await instance?.stop();
  if (scratch) {
    // Claude records the folder as /tmp/… and as /private/tmp/…: both are this run's own.
    forgetScratch(scratch, realpathSync(scratch));
    rmSync(scratch, { recursive: true, force: true });
  }
});

test("first run: setup link → password → checklist → Start → trust answered on the checklist → Chat with a reply", async ({ page }) => {
  await completeSetup(page, instance, "firstrun");
  await expect(page).toHaveURL(/\/welcome$/, { timeout: 60_000 });
  await expect(page.getByRole("heading", { name: "Almost there" })).toBeVisible();
  await expect(page.locator('[data-check="herdr.installed"] [data-status="ok"]')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('[data-check="agent.claude"] [data-status="ok"]')).toBeVisible();
  await page.screenshot({ path: "screenshots/firstrun-checklist-real-1440x900.png" });
  // The orchestrator waits for Start.
  const waiting = await (await page.request.get(`${instance.baseURL}/api/v1/orchestrator`)).json();
  expect(waiting.data.state).toBe("waiting");

  await page.getByRole("button", { name: "Start" }).click();
  await expect(page.getByRole("heading", { name: "Starting the orchestrator…" })).toBeVisible();
  // Claude stops at the folder-trust question; the checklist shows it as a dialog card.
  const trustButton = page.getByRole("button", { name: /^Trust this folder$/ }).first();
  const reachedChat = page.waitForURL(/\/chat$/, { timeout: 180_000 }).then(() => "chat" as const);
  const sawTrust = trustButton.waitFor({ timeout: 120_000 }).then(() => "trust" as const);
  const first = await Promise.race([reachedChat, sawTrust]);
  if (first === "trust") {
    await page.screenshot({ path: "screenshots/firstrun-trust-card-real-1440x900.png" });
    await trustButton.click();
    await reachedChat;
  }
  await expect(page).toHaveURL(/\/chat$/);
  // The greeting: the orchestrator's first reply arrives in Chat.
  await expect
    .poll(
      async () => {
        const messages = (await (await page.request.get(`${instance.baseURL}/api/v1/agents/orchestrator/messages?limit=20`)).json()) as {
          data: { messages: Array<{ kind: string; text?: string }> };
        };
        return messages.data.messages.some((message) => message.kind === "assistant" && (message.text ?? "").length > 0);
      },
      { timeout: 180_000, intervals: [2_000] },
    )
    .toBe(true);
  await page.waitForTimeout(1_500);
  await page.screenshot({ path: "screenshots/firstrun-chat-real-1440x900.png" });
  const status = await (await page.request.get(`${instance.baseURL}/api/v1/orchestrator`)).json();
  expect(status.data.state).toMatch(/idle|working/);
});
