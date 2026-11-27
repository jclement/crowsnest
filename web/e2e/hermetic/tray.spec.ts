// The needs-you tray and approval cards (DESIGN.md 8.7) against `crowsnest --demo`: on every view
// at 1440 and 375 px, never covering the Chat composer, the terminal's last rows or the bottom
// nav; an approval allowed and one denied with a note from the tray; the dangerous one's second
// confirmation; a question answered from the tray; the keyboard path (g n, Enter, y/n only on a
// focused card); axe. Screenshots of each view with the tray into web/screenshots.
//
// Demo approvals (internal/approvals/demo.go), each from a demo job's pane: ap_1 Bash `npx
// linkinator ./dist --recurse` (Website's j_14, job-14, low), ap_2 Edit of InboxScreen.tsx
// (Mobile's j_16, job-16, low), ap_3 `git push --force origin main` (Crowsnest's reviewer j_13,
// job-13, dangerous). Demo questions: q_1 blocking confirm, q_3 a choice ("Where should the
// release notes live?").
import type { Locator, Page, TestInfo } from "@playwright/test";
import { expect, expectAccessible, isPhone, test } from "../support/fixtures";

const views = [
  { name: "jobs", path: "/jobs", ready: (page: Page) => page.getByRole("region", { name: "Needs you or stuck" }) },
  { name: "chat", path: "/chat", ready: (page: Page) => page.getByRole("list", { name: "Conversation" }) },
  { name: "inbox", path: "/inbox", ready: (page: Page) => page.getByRole("list", { name: "Needs you" }) },
  { name: "fleet", path: "/fleet", ready: (page: Page) => page.getByRole("list", { name: "Workspaces" }) },
  { name: "terminal", path: "/terminal", ready: (page: Page) => page.getByRole("status").filter({ hasText: "Connected" }) },
] as const;

interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

function overlaps(a: Box, b: Box): boolean {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

async function box(locator: Locator): Promise<Box> {
  const found = await locator.boundingBox();
  expect(found, "element has a box").not.toBeNull();
  return found!;
}

/** The tray's list, opened: the desktop panel, or the phone's sheet. */
async function openTray(page: Page, testInfo: TestInfo): Promise<Locator> {
  if (isPhone(testInfo)) {
    await page.getByRole("button", { name: /need you: open the tray/ }).click();
    return page.getByRole("dialog");
  }
  const panel = page.getByRole("complementary", { name: "Needs you" });
  await expect(panel).toBeVisible();
  const showAll = panel.getByRole("button", { name: /^Show all/ });
  if (await showAll.isVisible()) await showAll.click();
  return panel;
}

/** A tray row by its title. */
function row(tray: Locator, title: RegExp): Locator {
  return tray.getByRole("button", { name: title, expanded: false });
}

async function approvalStatus(page: Page, id: string): Promise<{ status: string; note?: string; decided_via?: string }> {
  const response = await page.request.get(`/api/v1/approvals/${id}`);
  return ((await response.json()) as { data: { status: string; note?: string; decided_via?: string } }).data;
}

for (const view of views) {
  test(`${view.name}: the tray is there and covers nothing it must not`, async ({ page }, testInfo) => {
    await page.goto(view.path);
    await expect(view.ready(page).first()).toBeVisible({ timeout: 15_000 });
    if (isPhone(testInfo)) {
      const bar = page.getByRole("button", { name: /need you: open the tray/ });
      await expect(bar).toBeVisible();
      const nav = page.getByRole("navigation", { name: "Views" }).last();
      const barBox = await box(bar);
      expect(barBox.y + barBox.height, "the bar sits above the bottom nav").toBeLessThanOrEqual((await box(nav)).y + 0.5);
      if (view.name === "chat") {
        const composer = await box(page.getByRole("form", { name: /^Message/ }));
        expect(composer.y + composer.height, "the composer sits above the bar").toBeLessThanOrEqual(barBox.y + 0.5);
      }
    } else {
      const panel = page.getByRole("complementary", { name: "Needs you" });
      await expect(panel).toBeVisible();
      await expect(panel.getByRole("button", { name: /git push --force origin main/, expanded: false })).toBeVisible();
      await page.waitForTimeout(300); // the keep-out offset follows layout on the next frame
      const panelBox = await box(panel);
      if (view.name === "chat") expect(overlaps(panelBox, await box(page.getByRole("form", { name: /^Message/ }))), "the tray covers the composer").toBe(false);
      if (view.name === "terminal") {
        const terminal = await box(page.locator(".xterm").first());
        const lastRows = { x: terminal.x, y: terminal.y + terminal.height - Math.min(terminal.height * 0.4, 240), width: terminal.width, height: Math.min(terminal.height * 0.4, 240) };
        expect(overlaps(panelBox, lastRows), "the tray covers the terminal's last rows").toBe(false);
      }
    }
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({ path: `screenshots/tray-${view.name}-${isPhone(testInfo) ? 375 : 1440}${testInfo.project.name.includes("webkit") ? "-webkit" : ""}.png` });
  });
}

test("an approval allowed and one denied with a note, from the tray", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  let tray = await openTray(page, testInfo);
  await row(tray, /npx linkinator/).click();
  const card = tray.locator("[data-approval=ap_1]");
  await expect(card.getByLabel("Command")).toHaveText("npx linkinator ./dist --recurse");
  await expect(card.getByTestId("risk-line")).toContainText("No risky pattern");
  await card.getByRole("button", { name: "Allow once" }).click();
  await expect.poll(async () => (await approvalStatus(page, "ap_1")).status).toBe("allowed");
  await expect(tray.getByRole("button", { name: /npx linkinator/, expanded: false })).toHaveCount(0);

  if (isPhone(testInfo) && !(await tray.isVisible())) tray = await openTray(page, testInfo);
  await row(tray, /InboxScreen\.tsx/).click();
  const edit = tray.locator("[data-approval=ap_2]");
  await expect(edit.getByRole("region", { name: /Diff of/ })).toBeVisible();
  await edit.getByLabel(/Note to the agent/).fill("use the shared RefreshList instead");
  await edit.getByRole("button", { name: "Deny" }).click();
  await expect.poll(async () => (await approvalStatus(page, "ap_2")).status).toBe("denied");
  expect((await approvalStatus(page, "ap_2")).note).toBe("use the shared RefreshList instead");
});

test("a dangerous approval needs a second confirmation", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  const tray = await openTray(page, testInfo);
  await row(tray, /git push --force origin main/).click();
  const card = tray.locator("[data-approval=ap_3]");
  await expect(card.getByText("Dangerous", { exact: true })).toBeVisible();
  await expect(card.getByRole("button", { name: "Allow once" })).toHaveCount(0);
  await card.getByRole("button", { name: "Allow…" }).click();
  const confirm = card.getByRole("alertdialog");
  await expect(confirm).toContainText("This is dangerous");
  await expect(confirm).toContainText("Force push");
  expect((await approvalStatus(page, "ap_3")).status).toBe("pending");
  // The server refuses an unconfirmed allow too.
  const refused = await page.request.post("/api/v1/approvals/ap_3/decide", { headers: { Origin: new URL(page.url()).origin, "X-Crowsnest": "1" }, data: { decision: "allow" } });
  expect(refused.status()).toBe(409);
  await page.screenshot({ path: `screenshots/tray-dangerous-${isPhone(testInfo) ? 375 : 1440}${testInfo.project.name.includes("webkit") ? "-webkit" : ""}.png` });
  await confirm.getByRole("button", { name: "Yes, allow it" }).click();
  await expect.poll(async () => (await approvalStatus(page, "ap_3")).status).toBe("allowed");
});

test("a question answered from the tray", async ({ page }, testInfo) => {
  await page.goto("/fleet");
  const tray = await openTray(page, testInfo);
  await row(tray, /Where should the release notes live/).click();
  await tray.getByRole("button", { name: /Both, generated from commits/ }).click();
  await expect
    .poll(async () => {
      const response = await page.request.get("/api/v1/questions/q_3");
      return ((await response.json()) as { data: { status: string } }).data.status;
    })
    .toBe("answered");
});

test("the keyboard: g n focuses the tray, y and n answer only a focused card", async ({ page }, testInfo) => {
  test.skip(isPhone(testInfo), "a keyboard path on the desktop");
  await page.goto("/jobs");
  await expect(page.getByRole("complementary", { name: "Needs you" })).toBeVisible();
  // A stray y or n anywhere answers nothing.
  await page.locator("body").press("y");
  await page.locator("body").press("n");
  expect((await approvalStatus(page, "ap_1")).status).toBe("pending");
  expect((await approvalStatus(page, "ap_3")).status).toBe("pending");
  await page.keyboard.press("g");
  await page.keyboard.press("n");
  const first = page.locator("[data-tray-row]").first();
  await expect(first).toBeFocused();
  await expect(first).toContainText("git push --force origin main");
  // Enter opens the dangerous approval and focuses its card; y only opens the confirmation.
  await page.keyboard.press("Enter");
  const dangerous = page.locator("[data-approval=ap_3]");
  await expect(dangerous).toBeFocused();
  await page.keyboard.press("y");
  await expect(dangerous.getByRole("alertdialog")).toBeVisible();
  expect((await approvalStatus(page, "ap_3")).status).toBe("pending");
  // Another approval, opened from the keyboard, denied with n. The stack shows three items, and
  // two blocking questions rank above this low approval, so it is behind "Show all".
  const panel = page.getByRole("complementary", { name: "Needs you" });
  const showAll = panel.getByRole("button", { name: /^Show all/ });
  if (await showAll.isVisible()) await showAll.click();
  await panel.getByRole("button", { name: /InboxScreen\.tsx/, expanded: false }).focus();
  await page.keyboard.press("Enter");
  const edit = page.locator("[data-approval=ap_2]");
  await expect(edit).toBeFocused();
  await page.keyboard.press("n");
  await expect.poll(async () => (await approvalStatus(page, "ap_2")).status).toBe("denied");
});

test("snooze hides an item for ten minutes", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  const tray = await openTray(page, testInfo);
  await tray.getByRole("button", { name: /Snooze for 10 minutes: Bash: npx linkinator/ }).click();
  await expect(tray.getByRole("button", { name: /Snooze for 10 minutes: Bash: npx linkinator/ })).toHaveCount(0);
  await page.reload();
  const again = await openTray(page, testInfo);
  await expect(again.getByRole("button", { name: /git push --force/, expanded: false })).toBeVisible();
  await expect(again.getByRole("button", { name: /Snooze for 10 minutes: Bash: npx linkinator/ })).toHaveCount(0);
});

test("the Inbox lists approvals and opens one by its link", async ({ page }, testInfo) => {
  await page.goto("/inbox?q=approval%3Aap_3");
  const detail = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("region", { name: "Selected item" });
  await expect(detail.locator("[data-approval=ap_3]")).toBeVisible();
  await expect(detail.getByText(/Always allow/)).toBeVisible(); // the footnote: terminal only
  await expect(detail.getByRole("button", { name: /always allow/i })).toHaveCount(0);
});

test("axe: the tray open with an approval card", async ({ page }, testInfo) => {
  await page.goto("/jobs");
  const tray = await openTray(page, testInfo);
  await row(tray, /npx linkinator/).click();
  await expect(tray.locator("[data-approval=ap_1]")).toBeVisible();
  await expectAccessible(page, testInfo, "tray");
});
