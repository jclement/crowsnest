// Playwright configuration for the Jobs screenshots (M7): photographed against the real binary in
// --demo mode, whose board is seeded with a job in every state (one nudged, one stuck on a limit
// with a retry countdown, one waiting on a question). Needs `mise run build` first (the
// screenshots:jobs task does that). Data and state go to a throwaway directory.
import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.CROWSNEST_E2E_JOBS_PORT ?? 27695);

export default defineConfig({
  testDir: "./e2e",
  testMatch: "jobs.spec.ts",
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
