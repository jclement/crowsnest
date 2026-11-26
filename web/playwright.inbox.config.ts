// Playwright configuration for the Inbox screenshots (M6): photographed against the real binary in
// --demo mode, whose inbox is seeded with a question of every kind (a mermaid body, a diff, a
// multi-select) and a terminal dialog from the demo fleet. Needs `mise run build` first (the
// screenshots:inbox task does that). Data and state go to a throwaway directory.
import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.CROWSNEST_E2E_INBOX_PORT ?? 27694);

export default defineConfig({
  testDir: "./e2e",
  testMatch: "inbox.spec.ts",
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
