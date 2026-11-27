// Giving a job by voice from Now (R4 Fable 3.5, Codex P3/V1) against `crowsnest --demo` and a fake
// ElevenLabs whose transcript is "Website: fix the checkout validation", with Chromium's fake
// microphone and WebKit's mock one: hold the mic, see the readback before anything is created,
// Create, then Undo. Never the real ElevenLabs.
import { spawn, type ChildProcess } from "node:child_process";
import type { Page, TestInfo } from "@playwright/test";
import { expect, isPhone, test } from "../support/fixtures";
import { freePort } from "../support/instance";

async function sizeFor(page: Page, testInfo: TestInfo) {
  if (isPhone(testInfo)) await page.setViewportSize({ width: 390, height: 844 });
}

function width(testInfo: TestInfo): string {
  return isPhone(testInfo) ? `390${testInfo.project.name.includes("webkit") ? "-webkit" : ""}` : "1440";
}

// ---- Voice, with a fake ElevenLabs --------------------------------------------------------------

const FAKE_KEY = "sk_fake_e2e_key";

const voiceTest = test.extend<{ fakeURL: string }>({
  fakeURL: async ({}, use) => {
    const port = await freePort();
    const child: ChildProcess = spawn(process.execPath, ["e2e/support/fake-elevenlabs.mjs"], {
      env: { ...process.env, FAKE_ELEVENLABS_PORT: String(port), FAKE_ELEVENLABS_KEY: FAKE_KEY, FAKE_ELEVENLABS_TRANSCRIPT: "Website: fix the checkout validation" },
      stdio: "ignore",
    });
    const url = `http://127.0.0.1:${port}`;
    for (let i = 0; i < 100; i++) {
      if (await fetch(`${url}/health`).then((r) => r.ok, () => false)) break;
      await new Promise((r) => setTimeout(r, 50));
    }
    await use(url);
    child.kill();
  },
  appOptions: async ({ fakeURL }, use) => use({ demo: true, env: { ELEVENLABS_API_KEY: FAKE_KEY, CROWSNEST_ELEVENLABS_URL: fakeURL } }),
});

voiceTest.use({
    permissions: ["microphone"],
    launchOptions: async ({ browserName }, use) => use(browserName === "chromium" ? { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"] } : {}),
});
  // Chromium's fake microphone is a desktop switch; WebKit has its own mock device.
  voiceTest.skip(({ browserName, isMobile }) => browserName === "chromium" && isMobile, "desktop Chromium covers Chromium");
  // The same rule as talk.spec.ts's hold-Space tests: holding the mic must record, and Playwright's
  // WebKit has a mock capture device on macOS but none on Linux, where the button never reaches
  // "Listening" (every CI run). CI covers this flow with desktop Chromium; a Mac run covers WebKit.
  voiceTest.skip(({ browserName }) => browserName === "webkit" && process.platform === "linux", "Playwright's WebKit has no mock microphone on Linux");

  voiceTest("give a job by voice: readback before anything is created, then a job id with Undo", async ({ page }, testInfo) => {
    await sizeFor(page, testInfo);
    await page.goto("/");
    const mic = page.getByRole("button", { name: /Give a job by voice/ });
    await expect(mic).toBeVisible();
    // The job box is the last section on a phone: bring it on screen before pressing at its centre.
    await mic.scrollIntoViewIfNeeded();
    const box = await mic.boundingBox();
    if (!box) throw new Error("no mic");
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await expect(page.getByRole("button", { name: /Listening/ })).toBeVisible();
    await page.waitForTimeout(1200);
    await page.mouse.up();

    const readback = page.getByTestId("readback");
    await expect(readback).toHaveText("Website: Fix the checkout validation. Create the job?", { timeout: 15_000 });
    const before = ((await (await page.request.get("/api/v1/jobs")).json()) as { data: unknown[] }).data.length;
    await page.screenshot({ path: `screenshots/phone-voice-job-${width(testInfo)}.png` });
    await page.getByRole("button", { name: /Create the job/ }).click();
    const created = page.getByTestId("voice-job-created");
    await expect(created).toContainText(/Created j_\d+ for Website/);
    const after = ((await (await page.request.get("/api/v1/jobs")).json()) as { data: unknown[] }).data.length;
    expect(after).toBe(before + 1);
    await created.getByRole("button", { name: /Undo/ }).click();
    await expect(page.getByText(/Undone: j_\d+ cancelled/)).toBeVisible();
  });
