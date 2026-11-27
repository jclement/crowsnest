// Playwright configuration for the real-Claude browser suite (`mise run e2e:claude`): opt-in and
// paid (haiku, a few cents a run). One crowsnest serves a throwaway crowsnest-test-* herdr session
// with a real orchestrator; the specs under e2e/claude drive it through the browser, one at a
// time, with long timeouts (a Claude turn takes seconds to minutes). No retries: a retry would
// spend tokens to hide a failure.
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e/claude",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 6 * 60_000,
  expect: { timeout: 30_000 },
  forbidOnly: !!process.env.CI,
  reporter: [["list"]],
  use: { trace: "retain-on-failure", screenshot: "only-on-failure", viewport: { width: 1440, height: 900 } },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } }],
});
