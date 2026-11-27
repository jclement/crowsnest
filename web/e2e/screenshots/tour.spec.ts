// Every view of the app against `crowsnest --demo`, at a phone (390x844) and a desktop
// (1440x900), light theme: the "before/after" sheet of a change that moves doors around. Each
// view is one full-page PNG under screenshots/tour/<label>/ (TOUR_LABEL, default "now"), and
// web/scripts/tour-sheet.py lays two labels side by side as one contact sheet.
// Run with `TOUR_LABEL=after npx playwright test --project=screenshots e2e/screenshots/tour.spec.ts`.
import { type Page } from "@playwright/test";
import { test } from "../support/fixtures";

const label = process.env.TOUR_LABEL ?? "now";

const sizes = [
  { name: "390", width: 390, height: 844 },
  { name: "1440", width: 1440, height: 900 },
] as const;

/** The views and the places a door was moved to: a route, and what to do once it is open. */
const stops: { name: string; path: string; then?: (page: Page) => Promise<void> }[] = [
  { name: "now", path: "/" },
  { name: "projects", path: "/projects" },
  { name: "project", path: "/projects/crowsnest" },
  { name: "inbox", path: "/inbox" },
  { name: "chat", path: "/chat" },
  { name: "jobs", path: "/jobs" },
  { name: "job", path: "/jobs?job=j_4" },
  { name: "agents", path: "/agents" },
  { name: "fleet", path: "/fleet" },
  { name: "terminal", path: "/terminal" },
  { name: "help", path: "/help" },
  { name: "system", path: "/system" },
  { name: "settings", path: "/settings" },
  {
    name: "account-menu",
    path: "/",
    then: async (page) => {
      await page.getByRole("button", { name: /account|menu/i }).first().click();
    },
  },
];

for (const size of sizes) {
  for (const stop of stops) {
    test(`${stop.name} at ${size.name}`, async ({ page }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      await page.addInitScript(() => localStorage.setItem("crowsnest-theme", "light"));
      await page.goto(stop.path);
      await page.waitForLoadState("networkidle").catch(() => undefined);
      await page.evaluate(() => document.fonts.ready);
      await page.waitForTimeout(600);
      if (stop.then) await stop.then(page).catch(() => undefined);
      await page.waitForTimeout(300);
      await page.screenshot({ path: `screenshots/tour/${label}/${stop.name}-${size.name}.png`, fullPage: true, animations: "disabled", caret: "hide" });
    });
  }
}
