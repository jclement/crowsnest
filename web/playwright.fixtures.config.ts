// Playwright configuration for the fixture screenshots and the 375 px layout checks
// (e2e/screenshots/fleet-fixtures.spec.ts, `mise run screenshots`). Chromium only. The web server
// is Vite in development mode, because fixtures mode (?fixtures=1) exists only in development
// builds; Playwright starts it and stops it. The hermetic suite and the other screenshots run
// against the built binary instead (playwright.config.ts).
import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.CROWSNEST_E2E_WEB_PORT ?? 5179);

export default defineConfig({
  testDir: "./e2e/screenshots",
  testMatch: "fleet-fixtures.spec.ts",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  reporter: [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: `npx vite --host 127.0.0.1 --port ${PORT} --strictPort`,
    url: `http://127.0.0.1:${PORT}/`,
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: "ignore",
  },
});
