// Spend as a user sees it (DESIGN.md 7.11), against `crowsnest --demo`, which seeds plausible
// usage for its jobs and the orchestrator through the same counting path real transcripts take:
// the chip on each card (none on the job the orchestrator holds), the job's Spend section with
// its per-call chart and cache share, the Jobs header's today and week, the breakdown sheet by
// job, model and day with sorting and CSV export, the orchestrator's line in the Chat menu, and
// the palette's Spend target and job rows. Then photographs at 375 and 1440.
//
// Demo ids (internal/jobs/demo.go): j_1 running in a worktree, j_4 held by the orchestrator, j_8
// done.
import type { Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { detailRegion, expect, expectNoSidewaysScroll, isPhone, test } from "../support/fixtures";

async function openBoard(page: Page, search = "") {
  await page.goto(`/${search}`);
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
}

const money = /\$\d+\.\d{2,3}/;

test("cards carry an estimated cost; the job held by the orchestrator does not", async ({ page }) => {
  await openBoard(page);
  const running = page.getByRole("region", { name: "In progress" }).getByRole("button", { name: /Add retry back-off/ });
  await expect(running.getByTestId("spend-chip")).toHaveText(money);
  const held = page.getByRole("button", { name: /Decide the release date/ });
  await expect(held).toBeVisible();
  await expect(held.getByTestId("spend-chip")).toHaveCount(0);
});

test("a job's Spend section breaks the cost down, charts its calls and says it is an estimate", async ({ page }, testInfo) => {
  await openBoard(page, "?job=j_1");
  const detail = detailRegion(page, testInfo, "Selected job");
  const section = detail.getByRole("region", { name: "Spend" });
  await expect(section).toBeVisible();
  await expect(section.getByText(/est\.$/).first()).toHaveText(new RegExp(`^${money.source} est\\.$`));
  await expect(section.getByText("Model calls")).toBeVisible();
  await expect(section.getByRole("meter", { name: /from the cache/ })).toHaveAttribute("aria-valuenow", /\d+/);
  const bars = section.getByRole("list", { name: /Tokens per model call/ }).getByRole("listitem");
  expect(await bars.count()).toBeGreaterThan(5);
  await bars.last().focus();
  await expect(section.getByText(/tokens · \$.* est\. · claude-/)).toBeVisible();
  await expect(section.getByText(/not billed per token/)).toBeVisible();
  // The job the orchestrator holds says where its use went.
  await openBoard(page, "?job=j_4");
  await expect(detailRegion(page, testInfo, "Selected job").getByText(/Counted on the orchestrator's line/)).toBeVisible();
});

test("the header's totals open a breakdown by job, model and day that sorts and exports CSV", async ({ page }) => {
  await openBoard(page);
  const button = page.getByRole("button", { name: /^Spend: \$\S+ today · \$\S+ wk \(estimates\)$/ });
  await expect(button).toBeVisible();
  await button.click();
  const sheet = page.getByRole("dialog", { name: "Spend (estimated)" });
  await expect(sheet.getByRole("table")).toBeVisible();
  // By job: the orchestrator is its own row, costs largest first.
  await expect(sheet.getByRole("cell", { name: "Orchestrator", exact: true })).toBeVisible();
  await expect(sheet.getByRole("columnheader", { name: /Est. cost/ })).toHaveAttribute("aria-sort", "descending");
  await sheet.getByRole("button", { name: "Name", exact: true }).click();
  await expect(sheet.getByRole("columnheader", { name: /Name/ })).toHaveAttribute("aria-sort", "ascending");
  // By model.
  await sheet.getByRole("tab", { name: "By model" }).click();
  await expect(sheet.getByRole("cell", { name: "claude-opus-5-5" })).toBeVisible();
  await expect(sheet.getByRole("cell", { name: "claude-sonnet-5-5" })).toBeVisible();
  // By day over 30 days: the orchestrator worked every day this week.
  await sheet.getByRole("tab", { name: "By day" }).click();
  await sheet.getByRole("tab", { name: "30 days" }).click();
  await expect.poll(async () => sheet.getByRole("row").count()).toBeGreaterThan(4);
  await expect(sheet.getByText(/Prices checked \d{4}-\d{2}-\d{2}/)).toBeVisible();
  await expectNoSidewaysScroll(page);
  // CSV: UTF-8 with a BOM, a header, the total last.
  const download = page.waitForEvent("download");
  await sheet.getByRole("button", { name: "CSV" }).click();
  const file = await (await download).path();
  const csv = await readFile(file, "utf8");
  expect(csv.startsWith("﻿key,label,cost_usd_estimate")).toBe(true);
  expect(csv.trim().split("\r\n").at(-1)).toMatch(/^total,Total,\d+\.\d+,/);
});

test("the Chat menu shows the orchestrator's own spend and opens the sheet", async ({ page }) => {
  await page.goto("/chat");
  await page.getByRole("button", { name: "Conversation menu" }).click();
  const line = page.getByTestId("orchestrator-spend");
  await expect(line).toContainText(/\$.* est\. today · \$.* est\. all/);
  await line.click();
  await expect(page.getByRole("dialog", { name: "Spend (estimated)" })).toBeVisible();
});

test("the palette has a Spend target, and job rows show their spend", async ({ page }) => {
  await openBoard(page);
  await page.getByRole("button", { name: "Search or run…" }).click();
  const input = page.getByRole("combobox", { name: "Search or run a command" });
  await input.fill("retry back-off");
  await expect(page.getByRole("option", { name: /Add retry back-off/ })).toContainText(money);
  await input.fill("spend");
  await page.getByRole("option", { name: /^Spend/ }).first().click();
  await expect(page.getByRole("dialog", { name: "Spend (estimated)" })).toBeVisible();
});

// ---- Screenshots -------------------------------------------------------------------------------

for (const size of [
  { name: "375x812", width: 375, height: 812 },
  { name: "1440x900", width: 1440, height: 900 },
]) {
  test(`spend screenshots at ${size.name}`, async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "desktop-chromium", "photographed once, from desktop Chromium at both sizes");
    await page.addInitScript(() => localStorage.setItem("crowsnest-theme", "light"));
    await page.setViewportSize({ width: size.width, height: size.height });
    await openBoard(page, "?job=j_1");
    await page.evaluate(() => document.fonts.ready);
    const phone = size.width < 768 || isPhone(testInfo);
    const scope = phone ? page.getByRole("dialog") : page.getByRole("region", { name: "Selected job" });
    const chart = scope.getByRole("list", { name: /Tokens per model call/ });
    await chart.scrollIntoViewIfNeeded();
    await chart.getByRole("listitem").nth(10).hover();
    if (phone) await expectNoSidewaysScroll(page);
    await page.screenshot({ path: `screenshots/spend-job-${size.name}.png`, animations: "disabled", caret: "hide" });
    if (phone) await page.getByRole("button", { name: "Close" }).click();
    await page.getByRole("button", { name: /^Spend: .* today/ }).click();
    const sheet = page.getByRole("dialog", { name: "Spend (estimated)" });
    await expect(sheet.getByRole("table")).toBeVisible();
    if (phone) await expectNoSidewaysScroll(page);
    await page.screenshot({ path: `screenshots/spend-sheet-${size.name}.png`, animations: "disabled", caret: "hide" });
    await page.emulateMedia({ colorScheme: "dark" });
    await page.evaluate(() => {
      localStorage.setItem("crowsnest-theme", "dark");
      document.documentElement.classList.add("dark");
    });
    await page.screenshot({ path: `screenshots/spend-sheet-dark-${size.name}.png`, animations: "disabled", caret: "hide" });
  });
}
