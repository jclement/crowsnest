// Playwright configuration for the hermetic suite (`mise run e2e`, DESIGN.md section 15) and the
// screenshots taken against the built binary (`mise run screenshots:*`).
//
// Every test starts its own `crowsnest --demo` on a random port (e2e/support/fixtures.ts), so
// there is no shared web server and tests run in parallel. Needs `mise run build` first (the
// tasks do that).
//
// Projects:
//   desktop-chromium  1440x900, mouse
//   mobile-chromium   375x812, touch, a phone user agent
//   mobile-webkit     375x812, touch, WebKit (Safari's engine, not Safari on a real iPhone)
//   screenshots       the photo specs under e2e/screenshots; they size the viewport themselves
//
// `mise run e2e` runs the first three over e2e/hermetic. A failure keeps its trace, and the HTML
// report goes to playwright-report/ (CI uploads it when the suite fails).
import { defineConfig, devices } from "@playwright/test";

const phone = { viewport: { width: 375, height: 812 }, screen: { width: 375, height: 812 } };

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  // Each test runs a demo server and a browser; four at a time is what a third of a laptop
  // (heavy's cap) carries, and what a CI runner carries without timing out.
  workers: Number(process.env.E2E_WORKERS ?? (process.env.CI ? 3 : 4)),
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }], ["github"]] : [["list"], ["html", { open: "never" }]],
  use: { trace: "retain-on-failure", screenshot: "only-on-failure" },
  projects: [
    {
      name: "desktop-chromium",
      testDir: "./e2e/hermetic",
      use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } },
    },
    {
      name: "mobile-chromium",
      testDir: "./e2e/hermetic",
      use: { ...devices["Pixel 7"], ...phone },
    },
    {
      name: "mobile-webkit",
      testDir: "./e2e/hermetic",
      use: { ...devices["iPhone 13"], ...phone },
    },
    {
      name: "screenshots",
      testDir: "./e2e/screenshots",
      testIgnore: "fleet-fixtures.spec.ts",
      fullyParallel: false,
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
