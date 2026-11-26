// Playwright configuration for talk mode (M11): the built binary in --demo mode with talk mode on,
// pointed at a fake ElevenLabs (e2e/support/fake-elevenlabs.mjs) through CROWSNEST_ELEVENLABS_URL,
// so no real ElevenLabs call is ever made. Chromium runs with a fake microphone (a generated
// tone) and auto-granted permission, so push-to-talk records real audio. Needs `mise run build`
// first (the e2e:voice task does that). Data and state go to a throwaway directory.
import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.CROWSNEST_E2E_VOICE_PORT ?? 27695);
const FAKE_PORT = Number(process.env.FAKE_ELEVENLABS_PORT ?? 27699);
/** The fake's key: not a secret, it only opens the fake. */
export const FAKE_KEY = "sk_fake_e2e_key";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "voice.spec.ts",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  reporter: [["list"]],
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: "retain-on-failure",
    permissions: ["microphone"],
    launchOptions: { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=user-gesture-required"] },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      command: `FAKE_ELEVENLABS_PORT=${FAKE_PORT} FAKE_ELEVENLABS_KEY=${FAKE_KEY} exec node e2e/support/fake-elevenlabs.mjs`,
      url: `http://127.0.0.1:${FAKE_PORT}/health`,
      reuseExistingServer: false,
      timeout: 10_000,
    },
    {
      command: `tmp=$(mktemp -d) && CROWSNEST_DATA_DIR=$tmp/data XDG_STATE_HOME=$tmp/state ELEVENLABS_API_KEY=${FAKE_KEY} CROWSNEST_ELEVENLABS_URL=http://127.0.0.1:${FAKE_PORT} exec ../bin/crowsnest serve --demo --no-auth --port ${PORT}`,
      url: `http://localhost:${PORT}/api/v1/health`,
      reuseExistingServer: false,
      timeout: 30_000,
      stdout: "ignore",
      stderr: "ignore",
    },
  ],
});
