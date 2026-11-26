// Talk mode end to end (M11) against `crowsnest --demo` and a fake ElevenLabs, with Chromium's
// fake microphone: turn talk mode on, hold to talk, the recording is transcribed (by the fake)
// and sent to the orchestrator under the voice header; the demo orchestrator answers with a
// SAY line, which the server turns into a `say` event; the browser fetches /voice/tts for it and
// plays it through the speech <audio> element. Also photographs Chat with talk mode on at 375
// and 1440 px. Run with `mise run e2e:voice`.
import { expect, test, type Page } from "@playwright/test";

const FAKE_URL = `http://127.0.0.1:${process.env.FAKE_ELEVENLABS_PORT ?? 27699}`;

interface FakeRequest {
  method: string;
  path: string;
  keyOk: boolean;
  keyPresent: boolean;
  fileType?: string;
  fileBytes?: number;
  model?: string;
  json?: { text?: string; model_id?: string } | null;
}

async function fakeRequests(page: Page): Promise<FakeRequest[]> {
  const response = await page.request.get(`${FAKE_URL}/__requests`);
  return (await response.json()) as FakeRequest[];
}

async function openChat(page: Page) {
  await page.goto("/chat");
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 15_000 });
}

/** Holds the push-to-talk button for ms with the mouse, as a person would. */
async function holdToTalk(page: Page, ms: number) {
  const button = page.getByRole("button", { name: "Hold to talk" });
  const box = await button.boundingBox();
  if (!box) throw new Error("no push-to-talk button");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await expect(page.locator('[data-phase="recording"]')).toBeVisible();
  await expect(page.getByTestId("recording-elapsed")).toBeVisible();
  await page.waitForTimeout(ms);
  await page.mouse.up();
}

test("hold to talk → transcribed → sent → SAY reply fetched from /voice/tts and played", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openChat(page);
  const me = await (await page.request.get("/api/v1/auth/me")).json();
  expect(me.data.features.voice).toBe(true);

  // The toggle is the gesture that unlocks audio and starts the talk heartbeat.
  const heartbeat = page.waitForRequest((request) => request.url().endsWith("/api/v1/voice/talk") && request.method() === "POST");
  await page.getByRole("button", { name: "Talk", exact: true }).click();
  await expect(page.getByRole("button", { name: "Talk", exact: true })).toHaveAttribute("aria-pressed", "true");
  expect((await heartbeat).postDataJSON()).toMatchObject({ on: true });

  const upload = page.waitForResponse((response) => response.url().endsWith("/api/v1/voice/stt"));
  await holdToTalk(page, 1200);
  const stt = await upload;
  expect(stt.status()).toBe(200);
  expect(stt.request().headers()["content-type"]).toMatch(/^audio\/webm/);

  // The transcript is shown with Cancel for 1.5 s, then sent with the voice flag.
  await expect(page.getByTestId("voice-transcript")).toHaveText("what is the status");
  const prompt = await page.waitForRequest((request) => request.url().endsWith("/api/v1/agents/orchestrator/prompt"), { timeout: 5_000 });
  expect(prompt.postDataJSON()).toEqual({ text: "what is the status", voice: true });

  // The voice message shows with its chip, without the header line.
  const conversation = page.getByRole("list", { name: "Conversation" });
  await expect(conversation.getByText("what is the status", { exact: true })).toBeVisible();

  // The demo orchestrator replies with a SAY line (talk=on); the server makes it a `say` event
  // and the browser fetches its speech.
  const tts = await page.waitForResponse((response) => response.url().includes("/api/v1/voice/tts?text_id=t_"), { timeout: 20_000 });
  expect(tts.status()).toBe(200);
  expect(tts.headers()["content-type"]).toBe("audio/wav");
  const textId = new URL(tts.url()).searchParams.get("text_id");

  // The reply renders with the SAY sentence as a small spoken caption, not as Markdown.
  const caption = page.getByTestId("spoken-caption").last();
  await expect(caption).toBeVisible();
  await expect(page.getByRole("article", { name: "Reply" }).last()).not.toContainText("SAY:");

  // The audio element played that text to the end.
  const audio = page.locator('audio[data-testid="speech-audio"]');
  await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => element.currentSrc)).toContain(`text_id=${textId}`);
  await expect
    .poll(() => audio.evaluate((element: HTMLAudioElement) => ({ played: element.played.length > 0, error: element.error?.code ?? 0 })), { timeout: 10_000 })
    .toEqual({ played: true, error: 0 });

  // Upstream: the key was sent on every call, the recording went as webm, the TTS text is the SAY sentence.
  const upstream = await fakeRequests(page);
  expect(upstream.length).toBeGreaterThanOrEqual(2);
  expect(upstream.every((request) => request.keyOk)).toBe(true);
  const sttCall = upstream.find((request) => request.path === "/v1/speech-to-text");
  expect(sttCall?.fileType).toMatch(/^audio\/webm/);
  expect(sttCall?.fileBytes).toBeGreaterThan(1000);
  const ttsCall = upstream.find((request) => request.path?.startsWith("/v1/text-to-speech/"));
  expect(ttsCall?.json?.text).toBe(await caption.textContent());

  // The key never reached the browser: not in /auth/me, not in any page the test can read.
  expect(JSON.stringify(me)).not.toContain("sk_fake_e2e_key");
});

test("the browser cannot make crowsnest speak arbitrary text", async ({ page }) => {
  await page.goto("/chat");
  const made = await page.request.get("/api/v1/voice/tts?text_id=t_000000000000000000000000");
  expect(made.status()).toBe(404);
  const text = await page.request.get("/api/v1/voice/tts?text=hello");
  expect(text.status()).toBe(400);
});

test("Cancel during the review sends nothing", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openChat(page);
  await page.getByRole("button", { name: "Talk", exact: true }).click();
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

for (const size of [
  { name: "375x812", width: 375, height: 812 },
  { name: "1440x900", width: 1440, height: 900 },
]) {
  test(`chat with talk mode at ${size.name}`, async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("crowsnest-theme", "light"));
    await page.setViewportSize({ width: size.width, height: size.height });
    await openChat(page);
    await page.evaluate(() => document.fonts.ready);
    await page.getByRole("button", { name: "Talk", exact: true }).click();
    await holdToTalk(page, 900);
    await expect(page.getByTestId("voice-transcript")).toBeVisible();
    await page.waitForResponse((response) => response.url().includes("/api/v1/voice/tts?text_id="), { timeout: 20_000 });
    await expect(page.getByTestId("spoken-caption").last()).toBeVisible();
    await page.evaluate(() => {
      for (const element of document.querySelectorAll<HTMLElement>("[data-scroll-container], main")) element.scrollTop = element.scrollHeight;
    });
    if (size.width < 768) {
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
    }
    await page.screenshot({ path: `screenshots/chat-talk-${size.name}.png`, animations: "disabled", caret: "hide" });
  });
}
