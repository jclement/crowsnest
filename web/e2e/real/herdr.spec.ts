// The fleet, the Inbox and the walls, against a real herdr session (`mise run e2e:real`): a tab
// created through herdr while the page is open appears in Fleet without a reload; a pane whose
// program prints a Claude-shaped permission dialog and waits for one key is detected, listed in
// the Inbox, and pressing an option there delivers that digit to the program (it writes what it
// read to a file the test checks); and with the login on, the real server refuses terminal
// WebSockets without a session or from another origin.
//
// The dialog program is a small shell script, not Claude: it tells herdr it is an agent in the
// blocked state (`herdr pane report-agent`, as Claude's own integration does), so this needs no
// tokens. Every herdr command targets this instance's throwaway crowsnest-test-* session, with
// its throwaway HOME and every HERDR_* variable of ours removed (support/instance.ts).
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { scrubbedEnv, startInstance, type Instance } from "../support/instance";

const LOGIN = { username: "crow", password: "e2e-real-correct-horse" };

const herdrMissing = spawnSync("herdr", ["--version"], { env: scrubbedEnv() }).status !== 0;

function herdr(instance: Instance, ...args: string[]): string {
  const result = spawnSync("herdr", ["--session", instance.session, ...args], { env: instance.env, encoding: "utf8", timeout: 15_000 });
  if (result.status !== 0) throw new Error(`herdr ${args.join(" ")}: ${result.stderr}`);
  return result.stdout;
}

/** A workspace with one shell pane, w1:p1, in the instance's session. */
async function withWorkspace(instance: Instance): Promise<string> {
  const cwd = mkdtempSync("/tmp/cne-ws-");
  herdr(instance, "workspace", "create", "--cwd", cwd, "--label", "e2e");
  await new Promise((resolve) => setTimeout(resolve, 1500)); // the shell's first prompt
  return cwd;
}

/** A script that behaves like Claude at a permission dialog: blocked, numbered options, one key. */
function dialogScript(dir: string): { script: string; result: string } {
  const result = join(dir, "pressed.txt");
  const script = join(dir, "fake-claude-dialog.sh");
  writeFileSync(
    script,
    `#!/bin/bash
# Stand-in for Claude at a permission dialog (web/e2e/real/herdr.spec.ts).
clear
herdr pane report-agent --source e2e --agent claude --state blocked "$HERDR_PANE_ID" >/dev/null 2>&1
rule=$(printf '─%.0s' $(seq 1 80))
printf '\\n⏺ I will clean the build directory first.\\n\\n⏺ Bash(rm -rf build)\\n  ⎿  Running…\\n\\n%s\\n Bash command\\n\\n   rm -rf build\\n   Clean the build directory\\n\\n Do you want to proceed?\\n ❯ 1. Yes\\n   2. Yes, and don\\x27t ask again for rm commands in this project\\n   3. No, and tell Claude what to do differently (esc)\\n\\n Esc to cancel · Tab to amend · ctrl+e to explain\\n' "$rule"
IFS= read -rsn1 key
printf '%s' "$key" > ${JSON.stringify(result)}
# Claude redraws without the dialog once it is answered.
clear
herdr pane report-agent --source e2e --agent claude --state idle "$HERDR_PANE_ID" >/dev/null 2>&1
printf '\\nread: %s\\n' "$key"
`,
    { mode: 0o755 },
  );
  return { script, result };
}

test.describe("real herdr: fleet and Inbox", () => {
  test.skip(herdrMissing, "herdr is not installed");
  let instance: Instance;
  let cwd: string;
  test.beforeAll(async () => {
    instance = await startInstance({ demo: false });
    cwd = await withWorkspace(instance);
  });
  test.afterAll(async () => {
    await instance?.stop();
  });

  test("a tab created through herdr appears in Fleet without a reload", async ({ page }) => {
    await page.goto(`${instance.baseURL}/fleet`);
    const tree = page.getByRole("list", { name: "Workspaces" });
    await expect(tree.getByRole("button", { name: /e2e/ }).first()).toBeVisible();
    await expect(page.getByRole("list", { name: "Tabs in e2e" }).getByText("made-by-herdr")).toHaveCount(0);
    herdr(instance, "tab", "create", "--workspace", "w1", "--cwd", cwd, "--label", "made-by-herdr", "--no-focus");
    // herdr's tab_created event invalidates the fleet cache; the SSE fleet event updates the page.
    await expect(page.getByRole("list", { name: "Tabs in e2e" }).getByText("made-by-herdr")).toBeVisible({ timeout: 10_000 });
    herdr(instance, "tab", "close", "w1:t2");
  });

  test("a dialog in a pane is detected, shown in the Inbox, and the press reaches the program", async ({ page }) => {
    const { script, result } = dialogScript(cwd);
    herdr(instance, "pane", "run", "w1:p1", `bash ${script}`);

    await page.goto(`${instance.baseURL}/inbox`);
    const row = page.getByRole("list", { name: "Needs you" }).getByRole("button", { name: /is waiting at a dialog/ });
    await expect(row).toBeVisible({ timeout: 20_000 });
    await expect(page).toHaveTitle(/^\(1\) crowsnest/);
    await row.click();
    const card = page.getByRole("region", { name: "Selected item" }).getByLabel(/^Dialog from /);
    await expect(card.getByLabel("Screen text")).toContainText("Do you want to proceed?");
    const options = card.getByRole("list", { name: "Options" }).getByRole("button");
    await expect(options).toHaveCount(3);
    await expect(options.nth(1)).toContainText("Risky");
    const pressed = page.waitForResponse((response) => response.url().endsWith("/keys"));
    await options.nth(2).click();
    const response = await pressed;
    expect(response.status(), instance.log().split("\n").filter((line) => /keys|herdr|ERROR|WARN/.test(line)).slice(-6).join("\n")).toBe(200);
    await expect(page.getByText(/^Sent “3” to /)).toBeVisible();

    // The program read exactly the digit, with no Enter after it.
    await expect.poll(() => (existsSync(result) ? readFileSync(result, "utf8") : null), { timeout: 10_000 }).toBe("3");
    // And the Inbox lets go of it once the program reports itself idle.
    await expect(page.getByRole("list", { name: "Needs you" }).getByRole("button", { name: /is waiting at a dialog/ })).toHaveCount(0, { timeout: 15_000 });
  });
});

/** Opens a WebSocket from Node with exactly these headers; "open" or "refused". */
function tryWebSocket(url: string, headers: Record<string, string>): Promise<string> {
  return new Promise((resolve) => {
    const socket = new WebSocket(url, { headers } as unknown as string[]);
    const timer = setTimeout(() => resolve("timeout"), 8_000);
    socket.addEventListener("open", () => {
      clearTimeout(timer);
      socket.close();
      resolve("open");
    });
    socket.addEventListener("error", () => {
      clearTimeout(timer);
      resolve("refused");
    });
  });
}

async function signIn(page: Page, instance: Instance): Promise<string> {
  const response = await page.request.post(`${instance.baseURL}/api/v1/auth/login`, {
    headers: { Origin: instance.baseURL, "X-Crowsnest": "1", "Content-Type": "application/json" },
    data: LOGIN,
  });
  expect(response.status()).toBe(200);
  const cookie = (await page.context().cookies()).find((entry) => entry.name === "crowsnest_session");
  if (!cookie) throw new Error("no session cookie");
  return `${cookie.name}=${cookie.value}`;
}

test.describe("real herdr with the login on", () => {
  test.skip(herdrMissing, "herdr is not installed");
  let instance: Instance;
  test.beforeAll(async () => {
    instance = await startInstance({ demo: false, auth: LOGIN });
    await withWorkspace(instance);
  });
  test.afterAll(async () => {
    await instance?.stop();
  });

  test("terminal WebSockets need a session and the right Origin", async ({ page }) => {
    const ws = `ws://127.0.0.1:${instance.port}/api/v1`;
    for (const path of ["/term/tui", "/term/pane/w1%3Ap1"]) {
      expect(await tryWebSocket(`${ws}${path}`, { Origin: instance.baseURL }), `${path} without a session`).toBe("refused");
    }
    const cookie = await signIn(page, instance);
    for (const path of ["/term/tui", "/term/pane/w1%3Ap1"]) {
      expect(await tryWebSocket(`${ws}${path}`, { Cookie: cookie, Origin: "http://evil.example" }), `${path} cross-origin`).toBe("refused");
      expect(await tryWebSocket(`${ws}${path}`, { Cookie: cookie }), `${path} with no Origin`).toBe("refused");
      expect(await tryWebSocket(`${ws}${path}`, { Cookie: cookie, Origin: instance.baseURL }), `${path} done properly`).toBe("open");
    }
    // The page itself: unauthenticated it goes to the login, and after it the terminal connects.
    await page.context().clearCookies();
    await page.goto(`${instance.baseURL}/terminal`);
    await expect(page).toHaveURL(/\/login/);
    await page.getByLabel("Username").fill(LOGIN.username);
    await page.getByLabel("Password").fill(LOGIN.password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/terminal$/);
    await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible({ timeout: 15_000 });
  });
});
