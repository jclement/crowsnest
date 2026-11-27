// "Open in Claude" (DESIGN.md 7.10) against `crowsnest --demo`, on the desktop and on phones
// (Chromium and WebKit). The demo never starts Remote Control; it gives the orchestrator and each
// running lead a made-up session link of the real shape (https://claude.ai/code/session_demo…),
// so the link shows on Chat's header (orchestrator and a lead), a lead's card on its project page
// and Now's rows, always opening in a new tab with no opener and no referrer, at least 44 px tall
// on a phone; and its "?" opens the help sheet, "Crowsnest or the Claude app?".
import type { Locator, Page } from "@playwright/test";
import { expect, isPhone, test } from "../support/fixtures";

const demoLink = /^https:\/\/claude\.ai\/code\/session_demo[A-Za-z0-9]+$/;

async function expectSafeLink(link: Locator, phone: boolean) {
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute("href", demoLink);
  await expect(link).toHaveAttribute("target", "_blank");
  await expect(link).toHaveAttribute("rel", "noopener noreferrer");
  await expect(link).toHaveAttribute("title", /^Open ".+" in the Claude app$/);
  if (phone) {
    const box = await link.boundingBox();
    expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
  }
}

/** Chat's header (the sticky bar above the conversation). */
function chatHeader(page: Page): Locator {
  return page.locator("section header").first();
}

test("Open in Claude on Chat's header, for the orchestrator and for a lead", async ({ page }, testInfo) => {
  await page.goto("/chat");
  const orchestratorLink = chatHeader(page).getByTestId("open-in-claude-link");
  await expectSafeLink(orchestratorLink, isPhone(testInfo));
  await expect(orchestratorLink).toHaveAttribute("title", /crowsnest/);
  const orchestratorHref = await orchestratorLink.getAttribute("href");

  await page.goto("/chat?target=lead-website");
  const leadLink = chatHeader(page).getByTestId("open-in-claude-link");
  await expectSafeLink(leadLink, isPhone(testInfo));
  await expect(leadLink).toHaveAttribute("title", /Website/);
  expect(await leadLink.getAttribute("href")).not.toBe(orchestratorHref);
});

test("Open in Claude on a lead's card, and the help sheet", async ({ page }, testInfo) => {
  await page.goto("/projects/website");
  // The demo starts its leads in the background after the server answers, so straight after the
  // instance comes up the lead may not be running yet (no card until it is): under a full,
  // parallel run that start took longer than the default 10 s. Wait for the lead, then check it.
  await expect(page.getByTestId("open-in-claude-link").first()).toBeVisible({ timeout: 45_000 });
  const card = page.getByTestId("open-in-claude").first();
  await expectSafeLink(card.getByTestId("open-in-claude-link"), isPhone(testInfo));
  await card.getByRole("button", { name: "Crowsnest or the Claude app?" }).click();
  const sheet = page.getByRole("dialog", { name: "Crowsnest or the Claude app?" });
  await expect(sheet).toBeVisible();
  await expect(sheet.getByTestId("remote-help-sheet")).toContainText("long conversation with one agent");
  await expect(sheet.getByRole("table")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
});

test("Open in Claude on Now's orchestrator and running leads' rows, not on unfiled work", async ({ page }, testInfo) => {
  await page.goto("/");
  const projects = page.getByRole("list", { name: "Projects" });
  await expect(projects.locator("[data-now-row=orchestrator]")).toBeVisible();
  for (const row of ["orchestrator", "website", "crowsnest"]) {
    await expectSafeLink(projects.locator(`[data-now-row=${row}]`).getByTestId("open-in-claude-link"), isPhone(testInfo));
  }
  await expect(projects.locator("[data-now-row=unfiled]").getByTestId("open-in-claude-link")).toHaveCount(0);
  await expect(projects.locator("[data-now-row=infra]").getByTestId("open-in-claude-link")).toHaveCount(0); // its lead is not running
});
