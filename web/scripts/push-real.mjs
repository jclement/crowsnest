// A real Web Push round trip on this machine (DESIGN.md 8.5, REPORT): not part of any suite.
//
//   node scripts/push-real.mjs      (from web/, after `mise run build`)
//
// Starts the built binary in --demo on a random localhost port (localhost is a secure context),
// opens it in the installed Google Chrome (channel "chrome", new headless mode, a throwaway
// profile), turns notifications on through the real PushManager (so Chrome subscribes with
// Google's FCM), presses "Send a test", and waits for the service worker to show the
// notification: crowsnest → FCM → Chrome → sw.js → showNotification, nothing mocked.
//
// It talks to Google's push service, so it needs the network and costs nothing. It exits 0 when
// the notification arrived, 1 otherwise, and always stops the binary and the browser.
import { chromium } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const binary = resolve(import.meta.dirname, "../../bin/crowsnest");
const port = await new Promise((done) => {
  const probe = createServer().listen(0, "127.0.0.1", () => {
    const { port: free } = probe.address();
    probe.close(() => done(free));
  });
});
const scratch = mkdtempSync(join(tmpdir(), "cn-push-real-"));
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith("HERDR_") && !key.startsWith("CROWSNEST_")));
const server = spawn(binary, ["serve", "--demo", "--no-auth", "--port", String(port), "--data-dir", join(scratch, "data")], {
  env: { ...env, TMPDIR: scratch },
  stdio: ["ignore", "pipe", "pipe"],
});
let log = "";
server.stdout.on("data", (chunk) => (log += chunk));
server.stderr.on("data", (chunk) => (log += chunk));
const base = `http://localhost:${port}`;

let context;
let chrome;
let ok = false;
try {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(`${base}/api/v1/health`)).ok) break;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  // Chrome is started directly, not by Playwright, and attached over CDP: Playwright's launch flags
  // (--disable-background-networking, --disable-sync, --disable-component-update, …) stop
  // Chrome's connection to Google's push service, so FCM answered 410 to every push, or took it
  // and never delivered it (found 2026-11-26). A plain Chrome with a fresh profile behaves as a
  // person's would.
  const chromePath = process.env.CHROME_PATH ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  const debugPort = port + 1;
  chrome = spawn(chromePath, [
    ...(process.env.PUSH_REAL_HEADED ? ["--window-position=-3000,-3000", "--window-size=500,400"] : ["--headless=new"]),
    `--remote-debugging-port=${debugPort}`, `--user-data-dir=${join(scratch, "profile")}`,
    "--no-first-run", "--no-default-browser-check", "about:blank",
  ], { stdio: "ignore" });
  let browser;
  for (let i = 0; i < 100 && !browser; i++) {
    browser = await chromium.connectOverCDP(`http://127.0.0.1:${debugPort}`).catch(() => undefined);
    if (!browser) await new Promise((r) => setTimeout(r, 200));
  }
  if (!browser) throw new Error("could not attach to Chrome");
  context = browser.contexts()[0];
  await context.grantPermissions(["notifications"], { origin: base });
  const page = await context.newPage();
  await page.goto(base + "/");
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Notifications" }).click();
  const sheet = page.getByRole("dialog", { name: "Notifications" });
  await sheet.getByRole("button", { name: "Turn on for this device" }).click();
  const outcome = await Promise.race([
    page.getByText("Notifications are on for this device").waitFor({ timeout: 30_000 }).then(() => "on"),
    page.getByText("Could not turn notifications on").waitFor({ timeout: 30_000 }).then(async () => `failed: ${await page.getByRole("status").allTextContents()}`),
  ]);
  console.log("subscribe:", outcome);
  if (outcome !== "on") throw new Error("subscribing failed");
  const service = await sheet.getByRole("list", { name: "Devices" }).textContent();
  console.log("device:", service);
  // A brand-new FCM registration can be refused (410) for a few seconds after Chrome makes it.
  await page.waitForTimeout(Number(process.env.PUSH_REAL_SETTLE_MS ?? 15_000));
  await sheet.getByRole("button", { name: "Send a test" }).click();
  await page.getByText(/Test sent|not delivered|Could not send/).first().waitFor();
  console.log("test toast:", await page.getByText(/Test sent|not delivered|Could not send/).first().textContent());
  const deadline = Date.now() + Number(process.env.PUSH_REAL_WAIT_MS ?? 90_000);
  while (Date.now() < deadline) {
    const shown = await page.evaluate(async () => (await (await navigator.serviceWorker.ready).getNotifications()).map((n) => `${n.title}: ${n.body}`));
    if (shown.some((line) => line.startsWith("crowsnest: "))) {
      console.log("notification shown by the service worker:", shown);
      ok = true;
      break;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  if (!ok) {
    console.log("the test notice did not arrive in time; shown:", await page.evaluate(async () => (await (await navigator.serviceWorker.ready).getNotifications()).map((n) => `${n.title}: ${n.body}`)));
    // Chrome's own view of its push connection, for the report.
    const internals = await context.newPage();
    await internals.goto("chrome://gcm-internals").catch(() => {});
    await internals.waitForTimeout(1500);
    console.log("chrome://gcm-internals:\n" + ((await internals.locator("body").innerText().catch(() => "")) ?? "").slice(0, 3000));
  }
} catch (error) {
  console.log("error:", error instanceof Error ? error.message : error);
} finally {
  await context?.close().catch(() => {});
  chrome?.kill("SIGTERM");
  server.kill("SIGTERM");
  await new Promise((r) => server.once("exit", r));
  const pushLines = log.split("\n").filter((line) => /push|Push/.test(line));
  console.log("server log (push lines):\n" + pushLines.join("\n"));
  rmSync(scratch, { recursive: true, force: true });
}
process.exit(ok ? 0 : 1);
