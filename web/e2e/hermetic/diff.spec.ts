// The diff view and viewed tracking as a user drives them (DESIGN.md 6.21), against `crowsnest
// --demo`. The diff demo (internal/jobs/demo_diff.go): the Website project's folder is a real
// throwaway git repository with the lead's uncommitted edits, a running job on
// job/120-checkout-redesign (Go, TypeScript, CSS, Markdown, a rename, a deletion, a PNG and a
// large generated file, plus uncommitted work whose web/src/cart.ts keeps changing), and a done
// job on job/121-price-rounding. POST /changes/jobs/{id}/demo-touch makes the running job edit its
// cart now, which is how these tests make "the file changes".
//
// Phones run at 390x844 here. Screenshots (Chromium, light and dark, 390 and 1440 px) go to
// web/screenshots/diff-*.png and are committed after being looked at.
import type { APIRequestContext, Page, TestInfo } from "@playwright/test";
import { detailRegion, expect, expectAccessible, expectNoSidewaysScroll, isPhone, sameOriginHeaders, test } from "../support/fixtures";
import type { Instance } from "../support/instance";

async function jobId(request: APIRequestContext, branch: string): Promise<string> {
  const response = await request.get("/api/v1/jobs");
  const body = (await response.json()) as { data: Array<{ id: string; worktree_branch?: string }> };
  const job = body.data.find((entry) => entry.worktree_branch === branch);
  if (!job) throw new Error(`no demo job on ${branch}`);
  // The demo makes the repository and worktrees in the background, after the rows: wait for both
  // diffs to read (the running job's, and the Website project's main checkout).
  await expect.poll(async () => (await request.get(`/api/v1/changes/jobs/${job.id}`)).status(), { timeout: 20_000 }).toBe(200);
  await expect.poll(async () => (await request.get("/api/v1/changes/projects/website?scope=uncommitted")).status(), { timeout: 20_000 }).toBe(200);
  return job.id;
}

async function phoneSize(page: Page, testInfo: TestInfo) {
  if (isPhone(testInfo)) await page.setViewportSize({ width: 390, height: 844 });
}

async function shot(page: Page, testInfo: TestInfo, name: string) {
  if (testInfo.project.name.includes("webkit")) return; // one set: Chromium
  await page.screenshot({ path: `screenshots/diff-${name}-${isPhone(testInfo) ? 390 : 1440}.png`, animations: "disabled", caret: "hide" });
}

async function touch(page: Page, app: Instance, id: string) {
  await expect
    .poll(async () => (await page.request.post(`/api/v1/changes/jobs/${id}/demo-touch`, { headers: sameOriginHeaders(app) })).ok(), { timeout: 20_000 })
    .toBe(true);
}

test("a running job's diff: mark a file viewed, hide it, it changes, it comes back flagged, since-viewed shows only the new edit", async ({ page, app }, testInfo) => {
  await phoneSize(page, testInfo);
  const id = await jobId(page.request, "job/120-checkout-redesign");
  let viewer;
  if (isPhone(testInfo)) {
    // On a phone the diff is its own full-screen page, one file at a time.
    await page.goto(`/diff?job=${id}&file=web/src/cart.ts`);
    viewer = page.getByRole("region", { name: /^Changes: / });
    await expect(page.getByRole("navigation", { name: "Views" })).toHaveCount(0); // the bottom bar is hidden here
    await expect(viewer.getByRole("navigation", { name: "Files" })).toContainText(/\d+ \/ 10/);
  } else {
    await page.goto(`/jobs?job=${id}`);
    const detail = detailRegion(page, testInfo, "Selected job");
    await detail.getByRole("tab", { name: "Review" }).click();
    viewer = detail.getByRole("region", { name: /^Changes: / });
    // Narrower than the window, the viewer starts with its file list folded away.
    await viewer.getByRole("button", { name: "Show the file list" }).click();
    await expect(viewer.getByRole("complementary", { name: "Changed files" })).toBeVisible();
    // Every kind of change is there: a rename, a deletion, a binary file, a collapsed generated file.
    const list = viewer.getByRole("list", { name: "Files" });
    await expect(list).toContainText("web/src/lib/format.ts");
    await expect(viewer.getByRole("region", { name: "server/legacy_checkout.go" })).toBeVisible();
    await expect(viewer.getByRole("region", { name: "web/src/generated/api.ts" })).toContainText("generated code");
    await expect(viewer.getByRole("region", { name: "web/public/logo.png" })).toContainText("an image or font"); // collapsed, with why
    // Syntax colours on the Go handler, both sides (split view from lg).
    const handler = viewer.getByRole("table", { name: "Diff of server/checkout.go" });
    await expect(handler.locator(".hljs-keyword").first()).toBeVisible();
    await list.getByRole("button", { name: /web\/src\/cart\.ts/ }).click();
  }
  const cart = viewer.getByRole("region", { name: "web/src/cart.ts" });
  await expect(cart.getByRole("table", { name: "Diff of web/src/cart.ts" })).toContainText("item.quantity");
  await shot(page, testInfo, "running");

  await viewer.getByRole("checkbox", { name: "Viewed: web/src/cart.ts" }).first().check();
  await expect(viewer.getByText("1 of 10 viewed")).toBeVisible();
  if (!isPhone(testInfo)) await expect(cart.getByRole("table")).toHaveCount(0); // viewed collapses it
  await viewer.getByRole("button", { name: "Hide viewed" }).click();
  if (!isPhone(testInfo)) await expect(viewer.getByRole("list", { name: "Files" })).not.toContainText("web/src/cart.ts");

  // The agent edits the cart: the mark clears by itself and the file comes back, flagged.
  await touch(page, app, id);
  const back = viewer.getByRole("region", { name: "web/src/cart.ts" });
  await expect(back.getByText("changed since you viewed it")).toBeVisible({ timeout: 20_000 });
  await expect(viewer.getByText("0 of 10 viewed")).toBeVisible();
  await back.getByRole("button", { name: "Show since viewed" }).click();
  await expect(back.getByText(/only the new edits/)).toBeVisible();
  const since = back.getByRole("table", { name: "Diff of web/src/cart.ts" });
  await expect(since).toContainText("Edit 1: keep the cart badge in step with the server");
  // What was already seen is not shown as a change again: only the three new lines are added, and
  // nothing is removed (the earlier edit is plain context now).
  await expect(since.getByText("added", { exact: true })).toHaveCount(3);
  await expect(since.getByText("removed", { exact: true })).toHaveCount(0);
  await expectNoSidewaysScroll(page);
  await shot(page, testInfo, "since-viewed");
});

test("full screen keeps the scope and file, hides the app's chrome, and Esc comes back", async ({ page }, testInfo) => {
  test.skip(isPhone(testInfo), "a phone opens the diff page directly; covered above");
  const id = await jobId(page.request, "job/120-checkout-redesign");
  await page.goto(`/jobs?job=${id}`);
  const detail = detailRegion(page, testInfo, "Selected job");
  await detail.getByRole("tab", { name: "Review" }).click();
  const viewer = detail.getByRole("region", { name: /^Changes: / });
  await viewer.getByRole("radio", { name: /Uncommitted/ }).click();
  await viewer.getByRole("button", { name: "Show the file list" }).click();
  await viewer.getByRole("list", { name: "Files" }).getByRole("button", { name: /docs\/checkout\.md/ }).click();
  await page.keyboard.press("f");
  await expect(page).toHaveURL(/\/diff\?.*job=j_\d+.*scope=uncommitted/);
  await expect(page).toHaveURL(/file=docs%2Fcheckout\.md|file=docs\/checkout\.md/);
  await expect(page.getByRole("navigation", { name: "Views" })).toHaveCount(0);
  await expect(page.locator("header img[src='/icons/crowsnest.svg']")).toHaveCount(0);
  const full = page.getByRole("region", { name: /^Changes: / });
  await expect(full.getByRole("radio", { name: /Uncommitted/ })).toHaveAttribute("aria-checked", "true");
  // n and j move through changes and files without leaving the page.
  await page.keyboard.press("j");
  await page.keyboard.press("n");
  await expect(full.getByRole("table", { name: "Diff of docs/checkout.md" })).toBeVisible();
  await shot(page, testInfo, "fullscreen");
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(new RegExp(`/jobs\\?job=${id}`));
});

test("a lead's uncommitted changes, from the project page, in light and dark", async ({ page }, testInfo) => {
  await phoneSize(page, testInfo);
  await jobId(page.request, "job/120-checkout-redesign"); // waits for the demo's repository
  await page.goto("/projects/website");
  const card = page.getByRole("region", { name: "Changes", exact: true });
  await expect(card).toContainText("Main checkout (the lead's)");
  await expect(card).toContainText("Redesign the checkout");
  await card.getByRole("link", { name: /Main checkout/ }).click();
  await expect(page).toHaveURL(/\/diff\?project=website/);
  const viewer = page.getByRole("region", { name: /^Changes: Website/ });
  await viewer.getByRole("radio", { name: /Uncommitted/ }).click();
  await expect(viewer.getByText(/of 2 viewed/)).toBeVisible();
  await expect(viewer.getByRole("table", { name: "Diff of NOTES.md" })).toContainText("Lead's notes");
  if (!isPhone(testInfo)) {
    await expect(viewer.getByRole("table", { name: "Diff of DEPLOY.md" })).toContainText("Push to main; the site rebuilds in a minute.");
  } else {
    await viewer.getByRole("navigation", { name: "Files" }).getByRole("button", { name: "Next" }).click();
    await expect(viewer.getByRole("table", { name: "Diff of DEPLOY.md" })).toContainText("Push to main; the site rebuilds in a minute.");
  }
  await expectAccessible(page, testInfo, "diff-light");
  await shot(page, testInfo, "lead-light");
  await page.emulateMedia({ colorScheme: "dark" });
  await page.reload();
  await expect(page.locator("html")).toHaveClass(/dark/);
  await expect(page.getByRole("region", { name: /^Changes: Website/ }).getByRole("table").first()).toBeVisible();
  await expectAccessible(page, testInfo, "diff-dark");
  await shot(page, testInfo, "lead-dark");
});

test("the palette opens a job's changes", async ({ page }, testInfo) => {
  test.skip(isPhone(testInfo), "the palette's keyboard path is the desktop's");
  await page.goto("/jobs");
  await page.getByRole("button", { name: "Search or run…" }).click();
  await page.getByRole("combobox", { name: "Search or run a command" }).fill("changes redesign");
  await page.getByRole("option", { name: /Changes: Redesign the checkout/ }).click();
  await expect(page).toHaveURL(/\/diff\?job=j_\d+/);
  await expect(page.getByRole("region", { name: /^Changes: / })).toBeVisible();
});

// R10 O-B7 (the demo's half): the lead's uncommitted work in Website's main checkout was a README
// edit, so Land refused the demo's ready Website job every time. The lead's edits are new files
// now, and the job merges.
test("the demo's ready Website job can land beside the lead's uncommitted notes", async ({ page, baseURL }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop-chromium", "one run is enough: it merges for real");
  const id = await jobId(page.request, "job/121-price-rounding");
  // What the Review tab shows is what lands: send its head, as the Merge button does.
  const review = ((await (await page.request.get(`/api/v1/jobs/${id}/review`)).json()) as { data: { land: { head?: string } } }).data;
  const response = await page.request.post(`/api/v1/jobs/${id}/land`, {
    headers: { Origin: baseURL!, "X-Crowsnest": "1", "Content-Type": "application/json" },
    data: { action: "merge", confirm: true, expected_head: review.land.head },
  });
  const body = await response.text();
  expect(response.status(), body).toBeLessThan(300);
  expect(body).not.toContain("uncommitted changes");
});
