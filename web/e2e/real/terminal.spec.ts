// Terminals end to end, through the real binary and real WebSockets (DESIGN.md section 10).
//
//   - demo: `crowsnest --demo` serves the built-in toy terminal on both routes; the Terminal view
//     and a shell pane in Fleet both echo what is typed.
//   - real herdr: a throwaway crowsnest-test-* session with one workspace. The Terminal view (the
//     herdr TUI in a PTY) runs a command in the focused pane, the Fleet pane view (pane control)
//     runs another, and each command's output must appear in the xterm buffer. The output is
//     computed by the shell ($((n+1))), so the typed command echoing back does not count.
//     Screenshots of the Terminal view at 375x812 and 1440x900 go to web/screenshots/.
//
// Each describe block starts its own instance on a random port and stops it (and, for real
// herdr, stops and deletes the session) when it ends.
import { spawnSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { scrubbedEnv, startInstance, type Instance } from "./instance";

/** The visible text of the terminal's rows (xterm's DOM renderer draws one div per row). */
async function terminalText(page: Page): Promise<string> {
  return page.locator("[data-terminal] .xterm-rows").innerText();
}

async function expectTerminalText(page: Page, text: string | RegExp, timeout = 15_000) {
  await expect.poll(() => terminalText(page), { timeout, message: `terminal shows ${text}` }).toMatch(text);
}

/** Types into the focused terminal. */
async function typeInTerminal(page: Page, text: string) {
  await page.locator("[data-terminal]").click();
  await page.keyboard.type(text, { delay: 15 });
  await page.keyboard.press("Enter");
}

function computedEcho(): { command: string; output: string } {
  const n = Math.floor(Math.random() * 1_000_000);
  return { command: `echo crow-$((${n}+1))`, output: `crow-${n + 1}` };
}

test.describe("demo terminals", () => {
  let instance: Instance;
  test.beforeAll(async () => {
    instance = await startInstance({ demo: true });
  });
  test.afterAll(async () => {
    await instance?.stop();
  });

  test("the Terminal view echoes through the WebSocket", async ({ page }) => {
    await page.goto(`${instance.baseURL}/terminal`);
    await expectTerminalText(page, /demo terminal/);
    await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
    await typeInTerminal(page, "hello crow");
    await expectTerminalText(page, /you typed: hello crow/);
  });

  test("a shell pane in Fleet opens the pane terminal", async ({ page }) => {
    await page.goto(`${instance.baseURL}/fleet`);
    await page.getByRole("button", { name: /^Shell w\d+:p\d+/ }).first().click();
    await expectTerminalText(page, /standing in for pane w\d+:p\d+/);
    await typeInTerminal(page, "size");
    await expectTerminalText(page, /\d+ columns x \d+ rows/);
  });
});

test.describe("real herdr terminals", () => {
  let instance: Instance;
  test.beforeAll(async () => {
    test.skip(spawnSync("herdr", ["--version"], { env: scrubbedEnv() }).status !== 0, "herdr is not installed");
    instance = await startInstance({ demo: false });
    const cwd = mkdtempSync(join(tmpdir(), "e2e-"));
    const created = spawnSync("herdr", ["--session", instance.session, "workspace", "create", "--cwd", cwd, "--label", "e2e"], {
      env: instance.env,
      encoding: "utf8",
      timeout: 15_000,
    });
    if (created.status !== 0) throw new Error(`creating a workspace: ${created.stderr}`);
    await new Promise((resolve) => setTimeout(resolve, 1500)); // the shell's first prompt
  });
  test.afterAll(async () => {
    await instance?.stop();
  });

  test("the Terminal view drives the herdr TUI", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`${instance.baseURL}/terminal`);
    await expectTerminalText(page, /e2e/); // the workspace in herdr's sidebar
    const { command, output } = computedEcho();
    await typeInTerminal(page, command);
    await expectTerminalText(page, new RegExp(output));
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({ path: "screenshots/terminal-1440.png", animations: "disabled", caret: "hide" });

    await page.setViewportSize({ width: 375, height: 812 });
    await expect(page.getByRole("toolbar", { name: "Terminal keys" })).toBeVisible();
    await expectTerminalText(page, /e2e|crow-/);
    await page.waitForTimeout(500); // the TUI redraws for the narrow size
    await page.screenshot({ path: "screenshots/terminal-375.png", animations: "disabled", caret: "hide" });
    const scroll = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(scroll, "no sideways page scroll at 375 px").toBeLessThanOrEqual(0);
  });

  test("the Fleet pane view runs a command in the pane", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`${instance.baseURL}/fleet`);
    await page.getByRole("button", { name: /^Shell w1:p1/ }).click();
    await expect(page.getByRole("region", { name: "Terminal for pane w1:p1" })).toBeVisible();
    await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible({ timeout: 15_000 });
    const { command, output } = computedEcho();
    await typeInTerminal(page, command);
    await expectTerminalText(page, new RegExp(output));
    await page.screenshot({ path: "screenshots/terminal-pane-1440.png", animations: "disabled", caret: "hide" });
  });

  test("a second browser takes the pane over and the first is told", async ({ browser }) => {
    const first = await browser.newPage();
    const second = await browser.newPage();
    for (const page of [first, second]) await page.setViewportSize({ width: 1440, height: 900 });
    await first.goto(`${instance.baseURL}/fleet?agent=w1%3Ap1`);
    await expect(first.getByRole("status").filter({ hasText: "Connected" })).toBeVisible({ timeout: 15_000 });
    await second.goto(`${instance.baseURL}/fleet?agent=w1%3Ap1`);
    await expect(second.getByRole("status").filter({ hasText: "Connected" })).toBeVisible({ timeout: 15_000 });
    await expect(first.getByRole("status").filter({ hasText: "Ended: terminal attach taken over" })).toBeVisible({ timeout: 15_000 });
    await expect(first.getByRole("button", { name: "Reconnect" })).toBeVisible();
    await first.close();
    await second.close();
  });
});
