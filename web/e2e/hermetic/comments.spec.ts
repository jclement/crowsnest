// Line comments on the diff (DESIGN.md 6.21), as a user drives them, against `crowsnest --demo`.
// The diff demo's running job (internal/jobs/demo_diff.go, on job/120-checkout-redesign) has an
// agent in its pane and a transcript, so what is typed to it shows in its Conversation; it starts
// with review comments the demo left (cmd/crowsnest/demo_comments.go: two drafts, one sent, one
// outdated). POST /changes/jobs/{id}/demo-touch makes the agent edit web/src/cart.ts, which is how
// a comment on that file goes outdated here.
//
// Desktop (1440) comments from the job's Review tab; phones (390, Chromium and WebKit) from the
// full-screen diff page, a tap on a line number and the selection bar's Comment. Screenshots
// (Chromium, light and dark) go to web/screenshots/comments-*.png and are committed after being
// looked at.
import type { APIRequestContext, Locator, Page, TestInfo } from "@playwright/test";
import { detailRegion, expect, expectAccessible, expectNoSidewaysScroll, isPhone, sameOriginHeaders, smallTapTargets, test } from "../support/fixtures";
import type { Instance } from "../support/instance";

async function runningJob(request: APIRequestContext): Promise<string> {
  const response = await request.get("/api/v1/jobs");
  const body = (await response.json()) as { data: Array<{ id: string; worktree_branch?: string }> };
  const job = body.data.find((entry) => entry.worktree_branch === "job/120-checkout-redesign");
  if (!job) throw new Error("no demo job on job/120-checkout-redesign");
  // The repository, the worktrees and the demo's own comments are made in the background.
  await expect
    .poll(async () => {
      const list = await request.get(`/api/v1/changes/jobs/${job.id}/comments`);
      return list.ok() ? ((await list.json()) as { data: { outdated: number } }).data.outdated : -1;
    }, { timeout: 30_000 })
    .toBe(1);
  return job.id;
}

async function shot(page: Page, testInfo: TestInfo, name: string) {
  if (testInfo.project.name.includes("webkit")) return; // one set: Chromium
  await page.screenshot({ path: `screenshots/comments-${name}-${isPhone(testInfo) ? 390 : 1440}.png`, animations: "disabled", caret: "hide" });
}

async function touch(page: Page, app: Instance, id: string) {
  await expect
    .poll(async () => (await page.request.post(`/api/v1/changes/jobs/${id}/demo-touch`, { headers: sameOriginHeaders(app) })).ok(), { timeout: 20_000 })
    .toBe(true);
}

/** Selects a line by its number (a tap on a phone), opens the editor, writes the note and saves it. */
async function commentOn(page: Page, viewer: Locator, line: Locator, path: string, note: string, phone: boolean) {
  await line.click();
  if (phone) await viewer.getByRole("status", { name: "Selected lines" }).getByRole("button", { name: "Comment" }).click();
  else await page.keyboard.press("c");
  const editor = viewer.getByRole("form", { name: new RegExp(`^Comment on lines? \\d+.* of ${path.replace(/[./]/g, "\\$&")}$`) });
  await editor.getByRole("textbox", { name: "Comment" }).fill(note);
  await editor.getByRole("button", { name: "Save comment" }).click();
  await expect(viewer.getByRole("article", { name: new RegExp(`^Comment on ${path.replace(/[./]/g, "\\$&")}, line`) }).filter({ hasText: note })).toBeVisible();
}

async function openViewer(page: Page, testInfo: TestInfo, id: string, file?: string): Promise<Locator> {
  if (isPhone(testInfo)) {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/diff?job=${id}${file ? `&file=${encodeURIComponent(file)}` : ""}`);
    return page.getByRole("region", { name: /^Changes: / });
  }
  await page.goto(`/jobs?job=${id}`);
  const detail = detailRegion(page, testInfo, "Selected job");
  await detail.getByRole("tab", { name: /^Review/ }).click();
  return detail.getByRole("region", { name: /^Changes: / });
}

async function showFile(viewer: Locator, path: string, phone: boolean) {
  if (phone) {
    await viewer.getByRole("button", { name: "Files", exact: true }).click();
    await viewer.page().getByRole("dialog").getByRole("list", { name: "Files" }).getByRole("button", { name: new RegExp(path.replace(/[./]/g, "\\$&")) }).click();
  } else {
    // The Review tab's viewer starts with its file list folded away.
    await expect(viewer.getByRole("button", { name: /^(Show|Hide) the file list$/ })).toBeVisible();
    const list = viewer.getByRole("list", { name: "Files" });
    if (!(await list.isVisible())) await viewer.getByRole("button", { name: "Show the file list" }).click();
    await list.getByRole("button", { name: new RegExp(path.replace(/[./]/g, "\\$&")) }).click();
  }
}

test("comment on lines in two files, send them, the agent gets one message with both; the file changes and its comment goes outdated", async ({ page, app }, testInfo) => {
  const phone = isPhone(testInfo);
  const id = await runningJob(page.request);
  if (!phone) {
    // The job's Review tab counts the comments waiting to be sent (the demo left two).
    await page.goto(`/jobs?job=${id}`);
    await expect(detailRegion(page, testInfo, "Selected job").getByRole("tab", { name: "Review · 2 comments to send" })).toBeVisible();
  }
  const viewer = await openViewer(page, testInfo, id, "server/checkout.go");

  // A line of the Go handler.
  if (!phone) await showFile(viewer, "server/checkout.go", phone);
  const handler = viewer.getByRole("table", { name: "Diff of server/checkout.go" });
  await expect(handler).toBeVisible();
  const handlerLine = handler.getByRole("row").filter({ hasText: "Checkout takes the cart" }).getByRole("button", { name: /new side/ }).first();
  await commentOn(page, viewer, handlerLine, "server/checkout.go", "Say in the comment what it answers with", phone);

  // A line of the cart, in another file.
  await showFile(viewer, "web/src/cart.ts", phone);
  const cart = viewer.getByRole("table", { name: "Diff of web/src/cart.ts" });
  await expect(cart).toContainText("item.quantity");
  const cartLine = cart.getByRole("row").filter({ hasText: "item.quantity" }).getByRole("button", { name: /new side/ }).first();
  await commentOn(page, viewer, cartLine, "web/src/cart.ts", "Name the sum: count", phone);
  await expect(cart.getByRole("button", { name: /new side, has a comment$/ }).first()).toBeVisible();
  await expectNoSidewaysScroll(page);
  if (phone) expect(await smallTapTargets(page, "[data-file-section]")).toEqual([]);
  await shot(page, testInfo, "drafts");

  // The header counts them (the demo's two drafts and these two) and the sheet sends them as one message.
  await viewer.getByRole("button", { name: "Comments: 4 to send" }).click();
  const sheet = page.getByRole("dialog", { name: /^Comments · / });
  await sheet.getByRole("textbox", { name: "Overall note (optional)" }).fill("Two small things, then it is ready.");
  await sheet.getByRole("button", { name: "Preview the message" }).click();
  await expect(sheet.getByLabel("The message the agent gets")).toContainText("Name the sum: count");
  await shot(page, testInfo, "send");
  await sheet.getByRole("button", { name: "Send 4 comments to the agent" }).click();
  await expect(page.getByText("Comments sent")).toBeVisible();
  await expect(sheet.getByText(/0 to send · 5 sent · 1 outdated/)).toBeVisible();
  // Escape closes the sheet only: the viewer stays (on a phone it is the full-screen page).
  await page.keyboard.press("Escape");
  await expect(sheet).toHaveCount(0);
  await expect(viewer).toBeVisible();

  // The agent's conversation shows one message holding both.
  await page.goto(`/jobs?job=${id}`);
  const detail = detailRegion(page, testInfo, "Selected job");
  await detail.getByRole("tab", { name: "Conversation" }).click();
  const message = detail.getByText(/Review comments on your changes: 4 comments/);
  await expect(message).toHaveCount(1, { timeout: 15_000 });
  await expect(message).toContainText("server/checkout.go");
  await expect(message).toContainText("Say in the comment what it answers with");
  await expect(message).toContainText("web/src/cart.ts");
  await expect(message).toContainText("sum + item.quantity");
  await expect(message).toContainText("Overall: Two small things, then it is ready.");

  // The agent edits the cart: its comment goes outdated, still showing the code it was about.
  await touch(page, app, id);
  const again = await openViewer(page, testInfo, id, "web/src/cart.ts");
  if (!phone) await showFile(again, "web/src/cart.ts", phone);
  const section = again.getByRole("region", { name: "web/src/cart.ts" });
  const outdated = section.locator("[data-comment-status=outdated]").filter({ hasText: "Name the sum: count" });
  await expect(outdated).toBeVisible({ timeout: 20_000 });
  await expect(outdated).toContainText("The file has changed since this was written");
  await expect(outdated).toContainText("item.quantity");
  await expect(outdated.getByRole("button", { name: "Re-send" })).toBeVisible();
  await shot(page, testInfo, "outdated");
  await outdated.getByRole("button", { name: "Dismiss" }).click();
  await expect(section.locator("[data-comment-status=outdated]").filter({ hasText: "Name the sum: count" })).toHaveCount(0);
});

test("the diff with comments passes axe in light and dark", async ({ page }, testInfo) => {
  const id = await runningJob(page.request);
  const viewer = await openViewer(page, testInfo, id, "server/checkout.go");
  if (!isPhone(testInfo)) await showFile(viewer, "server/checkout.go", false);
  await expect(viewer.getByRole("table", { name: "Diff of server/checkout.go" }).getByRole("article").first()).toBeVisible();
  await expectAccessible(page, testInfo, "comments-light");
  await page.emulateMedia({ colorScheme: "dark" });
  const dark = await openViewer(page, testInfo, id, "server/checkout.go");
  await expect(page.locator("html")).toHaveClass(/dark/);
  if (!isPhone(testInfo)) await showFile(dark, "server/checkout.go", false);
  await expect(dark.getByRole("table", { name: "Diff of server/checkout.go" }).getByRole("article").first()).toBeVisible();
  await expectAccessible(page, testInfo, "comments-dark");
  await shot(page, testInfo, "dark");
});

test("a drag across line numbers selects the range (desktop)", async ({ page }, testInfo) => {
  test.skip(isPhone(testInfo), "a phone taps one number, then another; covered above");
  const id = await runningJob(page.request);
  await page.goto(`/diff?job=${id}&file=server/checkout.go`);
  const viewer = page.getByRole("region", { name: /^Changes: / });
  const table = viewer.getByRole("table", { name: "Diff of server/checkout.go" });
  const numbers = table.getByRole("button", { name: /^Line \d+, new side$/ });
  await expect(numbers.nth(4)).toBeVisible();
  const from = await numbers.nth(1).boundingBox();
  const to = await numbers.nth(4).boundingBox();
  if (!from || !to) throw new Error("no line numbers");
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
  await page.mouse.down();
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, { steps: 6 });
  await page.mouse.up();
  await expect(viewer.getByRole("status", { name: "Selected lines" })).toContainText(/lines \d+–\d+ of server\/checkout\.go selected/);
  await expect(table.getByRole("button", { name: /^Line \d+, new side$/, pressed: true })).toHaveCount(4);
});
