// Playwright configuration for the Chat screenshots (M4): unlike the fixture screenshots, Chat is
// photographed against the real binary in --demo mode, so the conversation comes through the real
// transcript tailer and the scripted demo orchestrator. Needs `mise run build` first (the
// screenshots:chat task does that). Data and state go to a throwaway directory.
import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.CROWSNEST_E2E_CHAT_PORT ?? 27693);

export default defineConfig({
  testDir: "./e2e",
  testMatch: "chat.spec.ts",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  reporter: [["list"]],
  use: { baseURL: `http://localhost:${PORT}`, trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: `tmp=$(mktemp -d) && CROWSNEST_DATA_DIR=$tmp/data XDG_STATE_HOME=$tmp/state exec ../bin/crowsnest serve --demo --no-auth --port ${PORT}`,
    url: `http://localhost:${PORT}/api/v1/health`,
    reuseExistingServer: false,
    timeout: 30_000,
    stdout: "ignore",
    stderr: "ignore",
  },
});
