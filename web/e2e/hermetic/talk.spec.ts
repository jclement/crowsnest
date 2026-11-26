// Talk mode end to end against `crowsnest --demo` and a fake ElevenLabs, with the browser's fake
// microphone:
//
// - The mic in the Chat composer (no switch first): hold → transcribed → sent under the voice
//   header; the demo orchestrator's reply carries a SAY line (it always does now), shown as the
//   caption; with speak mode on (the header switch, on every view) the browser fetches its
//   speech and plays it; with it off nothing is synthesised; a reply's play button speaks it on
//   demand.
// - Hold Space, driven with real key events (Playwright keyboard.down/up): holds of 3 s and 30 s
//   from the page, from a focused button and with auto-repeat; a quick tap that must not lock the
//   mic on or send; Option/Ctrl+Space from the composer while plain Space types; blur mid-hold.
//   These are the cases that were broken (see the voice-fix report); they run in Chromium and
//   in WebKit.
// - The Voice sheet: the account's voices, a preview, and a saved voice and speed used for the
//   next reply.
// - Chat at 375 and 1440 px with the mic recording, photographed.
//
// Each test starts its own fake ElevenLabs on a free port and a demo instance pointed at it, so
// no real ElevenLabs call is ever made. `mise run e2e:voice` runs this file alone.
import { spawn, type ChildProcess } from "node:child_process";
import type { Page } from "@playwright/test";
import { expect, test as base } from "../support/fixtures";
import { freePort } from "../support/instance";

/** The fake's key: not a secret, it only opens the fake. */
const FAKE_KEY = "sk_fake_e2e_key";

const test = base.extend<{ fakeURL: string }>({
  fakeURL: async ({}, use) => {
    const port = await freePort();
    const child: ChildProcess = spawn(process.execPath, ["e2e/support/fake-elevenlabs.mjs"], {
      env: { ...process.env, FAKE_ELEVENLABS_PORT: String(port), FAKE_ELEVENLABS_KEY: FAKE_KEY },
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

test.use({
  permissions: ["microphone"],
  // Chromium switches. WebKit on macOS ignores unknown arguments, but on Linux (CI) it refuses to
  // start with "Unknown option", which failed every WebKit test in this file there.
  launchOptions: async ({ browserName }, use) =>
    use(browserName === "chromium" ? { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=user-gesture-required"] } : {}),
});
// The fake microphone is a Chromium switch; WebKit (Playwright's build) has a mock capture device
// of its own. Mobile Chromium adds nothing here, so only desktop Chromium and WebKit run.
test.skip(({ browserName, isMobile }) => browserName === "chromium" && isMobile, "desktop Chromium covers Chromium");
const chromiumOnly = (browserName: string) => test.skip(browserName !== "chromium", "this flow uses Chromium's fake microphone and audio checks");

interface FakeRequest {
  method: string;
  path: string;
  keyOk: boolean;
  keyPresent: boolean;
  fileType?: string;
  fileBytes?: number;
  model?: string;
  json?: { text?: string; model_id?: string; voice_settings?: { speed?: number } } | null;
}

async function fakeRequests(page: Page, fakeURL: string): Promise<FakeRequest[]> {
  const response = await page.request.get(`${fakeURL}/__requests`);
  return (await response.json()) as FakeRequest[];
}

async function openChat(page: Page) {
  await page.goto("/chat");
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 15_000 });
}

/** Holds the push-to-talk button for ms with the mouse, as a person would. */
/** Holds the mic button for ms with the mouse, as a person would. */
async function holdToTalk(page: Page, ms: number) {
  const button = page.getByTestId("mic-button");
  const box = await button.boundingBox();
  if (!box) throw new Error("no mic button");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await expect(button).toHaveAttribute("data-phase", "recording");
  await expect(page.getByTestId("recording-elapsed")).toBeVisible();
  await page.waitForTimeout(ms);
  await page.mouse.up();
}

/** Counts this page's speech-to-text uploads. */
function countUploads(page: Page): () => number {
  let uploads = 0;
  page.on("request", (request) => {
    if (request.url().endsWith("/api/v1/voice/stt")) uploads++;
  });
  return () => uploads;
}

/** Holds Space for ms with real key events, sending auto-repeat keydowns every 100 ms if asked. */
async function holdSpace(page: Page, ms: number, repeat: boolean) {
  await page.keyboard.down("Space");
  await expect(page.getByTestId("mic-button")).toHaveAttribute("data-phase", "recording");
  const end = Date.now() + ms;
  while (Date.now() < end) {
    await page.waitForTimeout(100);
    if (repeat) await page.keyboard.down("Space"); // Playwright marks a second down as a repeat
  }
  await page.keyboard.up("Space");
}

const pageScroll = (page: Page) => page.evaluate(() => [window.scrollY, document.querySelector("main")?.scrollTop ?? 0, document.querySelector("[data-scroll-container]")?.scrollTop ?? 0]);

// ---- The flow ----------------------------------------------------------------------------

test("mic → transcribed → sent; with speak mode on the SAY reply is fetched and played", async ({ page, fakeURL, browserName }) => {
  chromiumOnly(browserName);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openChat(page);
  const me = await (await page.request.get("/api/v1/auth/me")).json();
  expect(me.data.features.voice).toBe(true);

  // Speak mode is the header's switch, on every view; it is the gesture that unlocks audio.
  const toggle = page.getByTestId("speak-toggle");
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-pressed", "true");

  // The mic is in the composer without any switch, with the Space hint beside it.
  await expect(page.getByTestId("space-hint")).toContainText("Hold Space to talk");
  const upload = page.waitForResponse((response) => response.url().endsWith("/api/v1/voice/stt"));
  await holdToTalk(page, 1200);
  const stt = await upload;
  expect(stt.status()).toBe(200);
  expect(stt.request().headers()["content-type"]).toMatch(/^audio\/webm/);

  // The transcript is shown with Cancel for 1.5 s, then sent with the voice flag.
  await expect(page.getByTestId("voice-transcript")).toHaveText("what is the status");
  const prompt = await page.waitForRequest((request) => request.url().endsWith("/api/v1/agents/orchestrator/prompt"), { timeout: 5_000 });
  expect(prompt.postDataJSON()).toEqual({ text: "what is the status", voice: true });
  const conversation = page.getByRole("list", { name: "Conversation" });
  await expect(conversation.getByText("what is the status", { exact: true })).toBeVisible();

  // The demo orchestrator's reply has a SAY line; the server makes it a `say` event and this
  // browser, with speak mode on, fetches its speech.
  const tts = await page.waitForResponse((response) => response.url().includes("/api/v1/voice/tts?text_id=t_"), { timeout: 20_000 });
  expect(tts.status()).toBe(200);
  expect(tts.headers()["content-type"]).toBe("audio/wav");
  const textId = new URL(tts.url()).searchParams.get("text_id");
  const caption = page.getByTestId("spoken-caption").last();
  await expect(caption).toBeVisible();
  await expect(page.getByRole("article", { name: "Reply" }).last()).not.toContainText("SAY:");
  const audio = page.locator('audio[data-testid="speech-audio"]');
  await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => element.currentSrc)).toContain(`text_id=${textId}`);
  await expect
    .poll(() => audio.evaluate((element: HTMLAudioElement) => ({ played: element.played.length > 0, error: element.error?.code ?? 0 })), { timeout: 10_000 })
    .toEqual({ played: true, error: 0 });

  // Upstream: the key was sent on every call, the recording went as webm with scribe_v3, the TTS
  // text is the SAY sentence, on the fast model.
  const upstream = await fakeRequests(page, fakeURL);
  expect(upstream.every((request) => request.keyOk)).toBe(true);
  const sttCall = upstream.find((request) => request.path === "/v1/speech-to-text");
  expect(sttCall?.fileType).toMatch(/^audio\/webm/);
  expect(sttCall?.model).toBe("scribe_v3");
  expect(sttCall?.fileBytes).toBeGreaterThan(1000);
  const ttsCall = upstream.find((request) => request.path?.startsWith("/v1/text-to-speech/"));
  expect(ttsCall?.json?.text).toBe((await caption.textContent())?.trim());
  expect(ttsCall?.json?.model_id).toBe("eleven_flash_v2_5");
  expect(ttsCall?.path).toContain("output_format=mp3_44100_64");
  expect(JSON.stringify(me)).not.toContain(FAKE_KEY);
});

test("speak mode off: replies are captioned and nothing is synthesised; the play button speaks one on demand", async ({ page, fakeURL, browserName }) => {
  chromiumOnly(browserName);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openChat(page);
  await holdToTalk(page, 900);
  await page.getByRole("button", { name: "Send now" }).click();
  // The demo's seeded replies and the new one all carry SAY captions.
  await expect(page.getByTestId("spoken-caption")).toHaveCount(3, { timeout: 20_000 });
  await page.waitForTimeout(1_000);
  let upstream = await fakeRequests(page, fakeURL);
  expect(upstream.filter((request) => request.path?.startsWith("/v1/text-to-speech/"))).toHaveLength(0);

  // Play one reply on demand, speak mode still off.
  const tts = page.waitForResponse((response) => response.url().includes("/api/v1/voice/tts?text_id=t_"));
  await page.getByRole("button", { name: "Play this reply aloud" }).first().click();
  expect((await tts).status()).toBe(200);
  await expect(page.getByTestId("player-bar")).toBeVisible();
  upstream = await fakeRequests(page, fakeURL);
  expect(upstream.filter((request) => request.path?.startsWith("/v1/text-to-speech/"))).toHaveLength(1);
  await expect(page.getByTestId("speak-toggle")).toHaveAttribute("aria-pressed", "false");
});

test("speak mode is remembered and works from another view", async ({ page, browserName }) => {
  chromiumOnly(browserName);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await page.getByTestId("speak-toggle").click();
  await page.reload();
  await expect(page.getByTestId("speak-toggle")).toHaveAttribute("aria-pressed", "true");
  // The switch is in the header of every view, not only Chat.
  for (const path of ["/inbox", "/fleet", "/chat"]) {
    await page.goto(path);
    await expect(page.getByTestId("speak-toggle")).toHaveAttribute("aria-pressed", "true");
  }
});

test("the browser cannot make crowsnest speak arbitrary text", async ({ page }) => {
  await page.goto("/chat");
  expect((await page.request.get("/api/v1/voice/tts?text_id=t_000000000000000000000000")).status()).toBe(404);
  expect((await page.request.get("/api/v1/voice/tts?text=hello")).status()).toBe(400);
});

test("Cancel during the review sends nothing", async ({ page, browserName }) => {
  chromiumOnly(browserName);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openChat(page);
  let prompts = 0;
  page.on("request", (request) => {
    if (request.url().endsWith("/prompt")) prompts++;
  });
  await holdToTalk(page, 800);
  await expect(page.getByTestId("voice-transcript")).toBeVisible();
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.waitForTimeout(2_000);
  expect(prompts).toBe(0);
});

// ---- Hold Space, with real key events ----------------------------------------------------------

test.describe("hold Space", () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await openChat(page);
  });

  for (const [name, ms] of [
    ["3 s", 3_000],
    ["30 s", 30_000],
  ] as const) {
    test(`held ${name} on the page with auto-repeat: records the whole time, sends once, never scrolls`, async ({ page }) => {
      test.setTimeout(90_000);
      await page.locator("main").click({ position: { x: 5, y: 5 } });
      const uploads = countUploads(page);
      const before = await pageScroll(page);
      await holdSpace(page, ms, true);
      await expect(page.getByTestId("voice-transcript")).toHaveText("what is the status");
      expect(uploads()).toBe(1);
      expect(await pageScroll(page)).toEqual(before);
    });
  }

  test("with a button focused, Space talks and does not press the button", async ({ page }) => {
    const menu = page.getByRole("button", { name: "Conversation menu" });
    await menu.focus();
    await holdSpace(page, 3_000, true);
    await expect(page.getByTestId("voice-transcript")).toBeVisible();
    await expect(menu).toHaveAttribute("aria-expanded", "false");
  });

  test("with the speak toggle focused, Space talks and leaves speak mode alone", async ({ page }) => {
    const toggle = page.getByTestId("speak-toggle");
    await toggle.click(); // on, and focused
    await holdSpace(page, 1_500, false);
    await expect(page.getByTestId("voice-transcript")).toBeVisible();
    await expect(toggle).toHaveAttribute("aria-pressed", "true");
  });

  test("a quick tap does not lock the mic on and sends nothing", async ({ page }) => {
    await page.locator("main").click({ position: { x: 5, y: 5 } });
    const uploads = countUploads(page);
    await page.keyboard.down("Space");
    await page.waitForTimeout(100);
    await page.keyboard.up("Space");
    // Which hint depends on when the browser delivers the keyup relative to the mic starting
    // (under load WebKit has delivered a 100 ms tap's keyup 450 ms late): before it, "hold the
    // key down"; after it, with under half a second recorded, "too short to send". Neither locks
    // the mic on or uploads, which is the point.
    await expect(page.getByRole("alert")).toContainText(/Hold the key down|too short to send/);
    await page.waitForTimeout(1_500);
    await expect(page.getByTestId("mic-button")).not.toHaveAttribute("data-phase", "recording");
    expect(uploads()).toBe(0);
  });

  test("in the composer Space types a space, and the chord talks", async ({ page }) => {
    const box = page.getByRole("textbox", { name: /Message to/ });
    await box.click();
    const uploads = countUploads(page);
    await page.keyboard.down("Space");
    await page.waitForTimeout(500);
    await page.keyboard.up("Space");
    await expect(box).toHaveValue(" ");
    expect(uploads()).toBe(0);
    await page.keyboard.down("Alt");
    await page.keyboard.down("Space");
    await expect(page.getByTestId("mic-button")).toHaveAttribute("data-phase", "recording");
    await page.waitForTimeout(1_500);
    await page.keyboard.up("Alt"); // the modifier first: the recording carries on until Space is up
    await page.waitForTimeout(300);
    await expect(page.getByTestId("mic-button")).toHaveAttribute("data-phase", "recording");
    await page.keyboard.up("Space");
    await expect(page.getByTestId("voice-transcript")).toBeVisible();
    await expect(box).toHaveValue(" "); // no non-breaking space typed by Option+Space
    expect(uploads()).toBe(1);
  });

  test("losing focus mid-hold ends the recording as a release", async ({ page }) => {
    await page.locator("main").click({ position: { x: 5, y: 5 } });
    await page.keyboard.down("Space");
    await expect(page.getByTestId("mic-button")).toHaveAttribute("data-phase", "recording");
    await page.waitForTimeout(1_500);
    await page.evaluate(() => window.dispatchEvent(new Event("blur")));
    await expect(page.getByTestId("voice-transcript")).toBeVisible();
    await page.keyboard.up("Space");
  });
});

// ---- The Voice sheet ---------------------------------------------------------------------------

test("Voice sheet: the account's voices, a preview, and a saved voice and speed used for the next reply", async ({ page, fakeURL, browserName }) => {
  chromiumOnly(browserName);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openChat(page);
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Voice" }).click();
  const sheet = page.getByRole("dialog", { name: "Voice" });
  await expect(sheet.getByTestId("voice-source")).toContainText("stock voice");
  const select = sheet.getByRole("combobox", { name: "Voice" });
  await expect(select.locator("option")).toHaveCount(3); // default + two voices
  await select.selectOption({ label: "Brian (american, male, middle aged)" });
  await sheet.getByRole("slider", { name: "Speed" }).fill("1.5");
  const preview = page.waitForResponse((response) => response.url().includes("/api/v1/voice/tts?text_id="));
  await sheet.getByRole("button", { name: "Preview" }).click();
  expect((await preview).status()).toBe(200);
  const audio = page.locator('audio[data-testid="speech-audio"]');
  await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => element.playbackRate)).toBe(1.25);
  let upstream = await fakeRequests(page, fakeURL);
  const previewCall = upstream.find((request) => request.path?.startsWith("/v1/text-to-speech/nPczCjzI2devNBz1zQrb/"));
  expect(previewCall?.json).toMatchObject({ voice_settings: { speed: 1.2 } });

  await sheet.getByRole("button", { name: "Save" }).click();
  await expect(sheet).toBeHidden();
  const saved = await (await page.request.get("/api/v1/voice/settings")).json();
  expect(saved.data).toMatchObject({ voice_id: "nPczCjzI2devNBz1zQrb", voice_source: "settings", speed: 1.5, playback_rate: 1.25 });

  // The next reply is spoken in the new voice.
  await page.getByTestId("speak-toggle").click();
  await holdToTalk(page, 900);
  await page.getByRole("button", { name: "Send now" }).click();
  await page.waitForResponse((response) => response.url().includes("/api/v1/voice/tts?text_id=t_"), { timeout: 20_000 });
  upstream = await fakeRequests(page, fakeURL);
  const replyCall = upstream.filter((request) => request.path?.startsWith("/v1/text-to-speech/")).at(-1);
  expect(replyCall?.path).toContain("/nPczCjzI2devNBz1zQrb/");
  await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => element.playbackRate)).toBe(1.25);
});

// ---- The command palette ----------------------------------------------------------------------

test("the palette turns speak mode on and off and opens the Voice sheet", async ({ page, browserName }) => {
  chromiumOnly(browserName);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  const toggle = page.getByTestId("speak-toggle");
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  const palette = async (query: string) => {
    await page.getByRole("button", { name: "Search or run…" }).click();
    await page.keyboard.type(query);
    await page.keyboard.press("Enter");
  };
  await palette("speak replies");
  await expect(toggle).toHaveAttribute("aria-pressed", "true");
  await palette("speak replies");
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  await palette("voice settings");
  await expect(page.getByRole("dialog", { name: "Voice" }).getByRole("combobox", { name: "Voice" })).toBeVisible();
});

// ---- Screenshots -------------------------------------------------------------------------------

for (const size of [
  { name: "375x812", width: 375, height: 812 },
  { name: "1440x900", width: 1440, height: 900 },
]) {
  test(`chat with the mic recording at ${size.name}`, async ({ page, browserName }) => {
    chromiumOnly(browserName);
    await page.addInitScript(() => localStorage.setItem("crowsnest-theme", "light"));
    await page.setViewportSize({ width: size.width, height: size.height });
    await openChat(page);
    await page.evaluate(() => document.fonts.ready);
    const mic = page.getByTestId("mic-button");
    const box = await mic.boundingBox();
    expect(box?.width).toBeGreaterThanOrEqual(size.width < 768 ? 44 : 36);
    expect(box?.height).toBeGreaterThanOrEqual(size.width < 768 ? 44 : 36);
    // Tap to lock, so the recording state can be photographed.
    await mic.click();
    await expect(mic).toHaveAttribute("data-phase", "recording");
    await page.waitForTimeout(1_200);
    if (size.width < 768) {
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
    }
    await page.screenshot({ path: `screenshots/chat-talk-${size.name}.png`, animations: "disabled", caret: "hide" });
    await mic.click();
    await expect(page.getByTestId("voice-transcript")).toBeVisible();
  });
}
