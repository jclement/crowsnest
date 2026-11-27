// The Jobs view against `crowsnest --demo`: the board (needs-you and stuck first, then running,
// then finished today), the new-job form open, and a job's detail (one running, one stuck on a
// limit with its retry countdown) at phone and desktop sizes; plus the 375 px rules (no sideways
// scroll, 44 px targets). Run with `mise run screenshots:jobs` (each test gets its own demo instance).
//
// Demo job ids, in seed order: j_1 running (worktree), j_2 nudged, j_3 stuck on a limit, j_4
// needs you (a question), j_5 needs you (a dialog), j_6 unresponsive, j_7 blocked, j_8 done,
// j_9 failed (older), j_10 queued for the orchestrator, then j_11 to j_17 (internal/jobs/demo.go);
// a job given here is j_18.
import { type Page } from "@playwright/test";
import { expect, test } from "../support/fixtures";

const sizes = [
  { name: "375x812", width: 375, height: 812 },
  { name: "1440x900", width: 1440, height: 900 },
] as const;

async function openJobs(page: Page, theme: "light" | "dark", search = "") {
  await page.addInitScript((value) => localStorage.setItem("crowsnest-theme", value), theme);
  await page.goto(`/${search}`);
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

async function noSidewaysScroll(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
}

const shot = (page: Page, name: string) => page.screenshot({ path: `screenshots/${name}.png`, animations: "disabled", caret: "hide" });

for (const theme of ["light", "dark"] as const) {
  for (const size of sizes) {
    test(`board at ${size.name} (${theme})`, async ({ page }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      await openJobs(page, theme);
      const attention = page.getByRole("region", { name: "Needs you or stuck" });
      await expect(attention.getByRole("button").first()).toContainText("Decide the release date");
      await expect(attention.getByText("Retry in", { exact: false })).toBeVisible();
      await expect(page.getByRole("region", { name: "In progress" }).getByText("2 nudges")).toBeVisible();
      await expect(page.getByRole("region", { name: "Finished today" })).toBeVisible();
      await expect(page.getByRole("button", { name: /Show 1 older/ })).toBeVisible();
      if (size.width < 768) await noSidewaysScroll(page);
      await shot(page, `jobs-board-${size.name}-${theme}`);
    });
  }
}

for (const size of sizes) {
  test(`new-job form open at ${size.name}`, async ({ page }) => {
    await page.setViewportSize({ width: size.width, height: size.height });
    await openJobs(page, "light");
    await page.getByLabel("What needs doing?").fill("Add a health check to the webhook sender\n\nIt should report the last delivery and the queue depth.");
    await page.getByRole("button", { name: /To the orchestrator/ }).click();
    await page.getByLabel(/Where/).fill("~/src/crowsnest");
    await page.getByLabel("Done when").fill("tests pass and the README documents the endpoint");
    await expect(page.getByLabel("Role")).toBeVisible();
    if (size.width < 768) await noSidewaysScroll(page);
    await shot(page, `jobs-new-${size.name}-light`);
  });

  test(`running job at ${size.name}`, async ({ page }) => {
    await page.setViewportSize({ width: size.width, height: size.height });
    await openJobs(page, "light", "?job=j_1");
    const detail = size.width < 768 ? page.getByRole("dialog") : page.getByRole("region", { name: "Selected job" });
    await expect(detail.getByRole("heading", { name: "Add retry back-off to the webhook sender" })).toBeVisible();
    await expect(detail.getByRole("list", { name: "Timeline" })).toBeVisible();
    await expect(detail.getByText("Doing now:")).toBeVisible();
    if (size.width < 768) await noSidewaysScroll(page);
    await shot(page, `jobs-detail-running-${size.name}-light`);
  });

  test(`stuck job at ${size.name}`, async ({ page }) => {
    await page.setViewportSize({ width: size.width, height: size.height });
    await openJobs(page, "light", "?job=j_3");
    const detail = size.width < 768 ? page.getByRole("dialog") : page.getByRole("region", { name: "Selected job" });
    const banner = detail.getByRole("alert");
    await expect(banner).toContainText("Hit a usage or rate limit");
    await expect(banner).toContainText("Retry in");
    await expect(banner.getByRole("button", { name: "Retry now" })).toBeVisible();
    if (size.width < 768) await noSidewaysScroll(page);
    await shot(page, `jobs-detail-stuck-${size.name}-light`);
  });
}

test("a finished job shows its result and evidence", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openJobs(page, "dark", "?job=j_8");
  const detail = page.getByRole("region", { name: "Selected job" });
  await expect(detail.getByRole("region", { name: "Verified", exact: true })).toContainText("mise run check: green");
  await expect(detail.getByRole("region", { name: "Not verified" })).toContainText("Safari");
  await shot(page, "jobs-detail-done-1440x900-dark");
});

test("tap targets on the Jobs view are at least 44 px on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await openJobs(page, "light");
  await page.getByRole("button", { name: /To the orchestrator/ }).click();
  const small = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("main button, main textarea, main input, main select")]
      .filter((element) => {
        const box = element.getBoundingClientRect();
        return box.width > 0 && box.height > 0 && (box.width < 43.5 || box.height < 43.5);
      })
      .map((element) => element.getAttribute("aria-label") ?? element.textContent?.trim() ?? element.tagName),
  );
  expect(small).toEqual([]);
});

test("giving a job adds it to the board", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openJobs(page, "light");
  await page.getByLabel("What needs doing?").fill("Summarise yesterday's merged PRs");
  await page.getByRole("button", { name: "Give job" }).click();
  await expect(page.getByRole("region", { name: "Selected job" }).getByRole("heading", { name: "Summarise yesterday's merged PRs" })).toBeVisible();
  await expect(page).toHaveURL(/job=j_18/);
});
