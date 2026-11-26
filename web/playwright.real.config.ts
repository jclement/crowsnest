// Playwright configuration for the "real" suite (`mise run e2e:real`): specs under e2e/real start
// the built binary themselves (e2e/real/instance.ts), on a random localhost port, either with
// --demo or against a throwaway real herdr session, and stop it when they finish. No web server
// here, and no fixtures: the page talks to the real API.
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e/real",
  fullyParallel: false,
  workers: 1,
  timeout: 90_000,
  forbidOnly: !!process.env.CI,
  reporter: [["list"]],
  use: { trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
