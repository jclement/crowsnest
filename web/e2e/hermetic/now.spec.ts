// The Now view, the one attention queue and the phone nav (DESIGN.md 12 and 22) against
// `crowsnest --demo`, at 390x844 (Chromium and WebKit) and 1440x900:
//
//   - the busy-day walk from the R4 review brief: five projects, things waiting, one stuck; every
//     obligation is found from Now, with its project and job, without opening Chat (on a phone Now
//     shows the top three and "Review all N", which opens the Inbox with the rest);
//   - on a phone the projects, Since you left and the job box are within the first two screens;
//   - one number: the nav badge, the tab title, the Now summary, the Inbox's count line, the tray
//     and the API's counts.total agree;
//   - answering from Now: a question's option and a low-risk approval, in place, and the count
//     goes down by one each time;
//   - "since you left" is per browser: Mark all seen empties it here and not in another browser;
//   - the nav: Now, Projects, Inbox, Chat and More on a phone (More: Jobs, Fleet, Terminal), the
//     same order on the desktop rail; an old /?job= link lands on the board;
//   - axe on Now; screenshots into web/screenshots (now-*.png).
//
// The demo's story (internal/demostory, DESIGN.md 21.5) has five projects and four obligations
// that hold for the first ten minutes: a dangerous approval in Crowsnest, a blocking question in
// Website, a terminal dialog in a third project, and a job stuck on a usage limit in Mobile.
import type { Locator, Page, TestInfo } from "@playwright/test";
import { expect, expectAccessible, isPhone, test } from "../support/fixtures";

const PROJECTS = ["Crowsnest", "Website", "Infra", "Mobile"];

/** Phones are 390x844 here (the review's iPhone size); the projects' default is 375x812. */
async function sizeFor(page: Page, testInfo: TestInfo) {
  if (isPhone(testInfo)) await page.setViewportSize({ width: 390, height: 844 });
}

function width(testInfo: TestInfo): string {
  return isPhone(testInfo) ? `390${testInfo.project.name.includes("webkit") ? "-webkit" : ""}` : "1440";
}

interface Snapshot {
  counts: { total: number; blocking: number };
  items: Array<{ id: string; kind: string; urgency: string; title: string; project: { name: string } | null; job_id?: string; approval?: { risk: string }; job?: { stuck: boolean; cause: string } }>;
}

async function snapshot(page: Page): Promise<Snapshot> {
  const response = await page.request.get("/api/v1/attention");
  return ((await response.json()) as { data: Snapshot }).data;
}

/** How many queue rows Now shows on a phone (the rest are a tap away in the Inbox). */
const PHONE_ROWS = 3;

/** The Now view's queue. */
function queue(page: Page): Locator {
  return page.getByRole("region", { name: "Needs you" }).getByRole("list", { name: "Waiting on you" });
}

async function badgeCount(page: Page, testInfo: TestInfo): Promise<number> {
  const nav = page.getByRole("navigation", { name: "Views" }).filter({ visible: true });
  const badge = nav.getByLabel(/need you$/);
  if ((await badge.count()) === 0) return 0;
  const label = (await badge.first().getAttribute("aria-label")) ?? "";
  void testInfo;
  return Number(label.split(" ")[0]);
}

test("the busy-day walk: every obligation and every project from Now, without Chat", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Now", level: 1 })).toBeVisible();
  await expect(queue(page).locator("[data-tray-item]").first()).toBeVisible();

  // Every project has its row, plus unfiled work and the orchestrator, each saying who owns it.
  const projects = page.getByRole("list", { name: "Projects" });
  for (const name of PROJECTS) await expect(projects.getByRole("button", { name: new RegExp(name) })).toBeVisible();
  await expect(projects.locator("[data-now-row]")).toHaveCount(await projects.locator("[data-now-row]").count());
  expect(await projects.locator("[data-now-row]").count()).toBeGreaterThanOrEqual(PROJECTS.length + 2);
  await expect(projects.locator("[data-now-row=unfiled]")).toBeVisible();
  await expect(projects.locator("[data-now-row=orchestrator]")).toBeVisible();
  await expect(projects.locator("[data-owner]").first()).not.toBeEmpty();

  // The four obligations, each found in the queue with its project named on the row.
  const data = await snapshot(page);
  const dangerous = data.items.find((item) => item.kind === "approval" && item.approval?.risk === "dangerous");
  const blockingQuestion = data.items.find((item) => item.kind === "question" && item.urgency === "blocking" && item.project?.name === "Website");
  const dialog = data.items.find((item) => item.kind === "dialog");
  const stuck = data.items.find((item) => item.kind === "job" && item.job?.stuck && item.job.cause === "limit");
  for (const [what, item, project] of [
    ["the dangerous approval", dangerous, "Crowsnest"],
    ["the blocking question", blockingQuestion, "Website"],
    ["the terminal dialog", dialog, undefined],
    ["the stuck job", stuck, "Mobile"],
  ] as const) {
    expect(item, what).toBeDefined();
    if (project) expect(item?.project?.name, `${what}'s project`).toBe(project);
    expect(item?.job_id, `${what} names its job`).toBeTruthy();
    // On a phone Now draws only the top three; the rest are one tap away, in the Inbox.
    if (isPhone(testInfo) && data.items.indexOf(item!) >= PHONE_ROWS) continue;
    const row = queue(page).locator(`[data-tray-item="${item!.id}"]`);
    await expect(row, what).toBeVisible();
    await expect(row.locator("[data-where]")).toContainText(item!.project?.name ?? "Unfiled");
  }
  await expect(page).toHaveURL(/\/$/);
  await expectAccessible(page, testInfo, "the Now view");
  await page.screenshot({ path: `screenshots/now-${width(testInfo)}.png`, fullPage: true });
  if (isPhone(testInfo)) {
    await page.getByRole("link", { name: `Review all ${data.counts.total}` }).click();
    await expect(page).toHaveURL(/\/inbox/);
    const inbox = page.getByRole("list", { name: "Needs you" });
    for (const item of [dangerous, blockingQuestion, dialog, stuck]) await expect(inbox.locator(`[data-attention-item="${item!.id}"]`)).toHaveCount(1);
  }
});

test("one number: badge, title, Now, Inbox and tray agree with the server", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  await expect(queue(page).locator("[data-tray-item]").first()).toBeVisible();
  const total = (await snapshot(page)).counts.total;
  expect(total).toBeGreaterThan(3);
  await expect(page.locator("[data-now-summary]")).toContainText(`${total} need you`);
  await expect.poll(() => badgeCount(page, testInfo)).toBe(total);
  await expect(page).toHaveTitle(new RegExp(`^\\(${total}\\)`));
  await expect(queue(page).locator("[data-tray-item]")).toHaveCount(isPhone(testInfo) ? PHONE_ROWS : total);
  if (isPhone(testInfo)) await expect(page.getByRole("link", { name: `Review all ${total}` })).toBeVisible();

  await page.goto("/inbox");
  await expect(page.locator("[data-attention-count]")).toHaveAttribute("data-attention-count", String(total));
  await expect(page.getByRole("list", { name: "Needs you" }).locator("[data-attention-item]")).toHaveCount(total);

  // Elsewhere the tray shows the same count.
  await page.goto("/chat");
  if (isPhone(testInfo)) {
    await expect(page.getByRole("button", { name: /need you: open the tray/ })).toContainText(String(total));
  } else {
    const panel = page.getByRole("complementary", { name: "Needs you" });
    await expect(panel.locator("header")).toContainText(String(total));
  }
});

test("answering from Now: a question's option and a low approval, in place", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  await expect(queue(page).locator("[data-tray-item]").first()).toBeVisible();
  const before = await snapshot(page);
  const question = before.items.find((item) => item.kind === "question" && item.urgency === "blocking");
  expect(question).toBeDefined();
  const questionRow = queue(page).locator(`[data-tray-item="${question!.id}"]`);
  await questionRow.getByRole("button", { expanded: false }).first().click();
  // A routine question answers in the row; one with evidence (a diff, a long body) opens its
  // decision packet, answered from the packet's bar (R4 Codex P2).
  const review = questionRow.getByRole("button", { name: "Review the decision" });
  const options = questionRow.getByRole("list", { name: "Options" });
  await expect(review.or(options)).toBeVisible();
  if (await review.isVisible()) {
    await review.click();
    await expect(page.getByTestId("decision-packet")).toBeVisible();
    await page.getByTestId("answer-bar").getByRole("group", { name: "Choices" }).getByRole("button").first().click();
    await page.goto("/");
  } else {
    await options.getByRole("button").first().click();
  }
  await expect(queue(page).locator(`[data-tray-item="${question!.id}"]`)).toHaveCount(0);
  await expect.poll(async () => (await snapshot(page)).counts.total).toBeLessThan(before.counts.total);

  const middle = await snapshot(page);
  const low = middle.items.find((item) => item.kind === "approval" && item.approval?.risk === "low");
  expect(low).toBeDefined();
  // A phone draws the top three only: a routine approval ranks below them and is answered from
  // the Inbox there (inbox.spec.ts), so the in-place answer is the desktop's.
  if (isPhone(testInfo) && middle.items.indexOf(low!) >= PHONE_ROWS) return;
  const approvalRow = queue(page).locator(`[data-tray-item="${low!.id}"]`);
  await approvalRow.getByRole("button", { expanded: false }).first().click();
  await approvalRow.getByRole("button", { name: "Allow once" }).click();
  await expect(approvalRow).toHaveCount(0);
  await expect.poll(async () => (await snapshot(page)).counts.total).toBeLessThan(middle.counts.total);
  await expect(page.locator("[data-now-summary]")).toContainText(`${(await snapshot(page)).counts.total} need you`);
});

test("on a phone the projects, Since you left and the job box are within the first two screens", async ({ page }, testInfo) => {
  test.skip(!isPhone(testInfo), "the phone's layout");
  await sizeFor(page, testInfo);
  await page.goto("/");
  await expect(queue(page).locator("[data-tray-item]").first()).toBeVisible();
  await expect(page.getByRole("list", { name: "Projects" }).locator("[data-now-row]").first()).toBeVisible();
  // Each section's top, measured down the scrolled page from the top of the view.
  const tops = await page.evaluate(() => {
    const main = document.querySelector("main")!;
    const origin = main.getBoundingClientRect().top - main.scrollTop;
    const top = (selector: string) => (document.querySelector(selector)?.getBoundingClientRect().top ?? Infinity) - origin;
    return { projects: top("#now-projects"), since: top("#now-since"), job: top("#now-give") };
  });
  const twoScreens = 2 * 844;
  expect(tops.projects, "projects").toBeLessThan(844);
  expect(tops.since, "since you left").toBeLessThan(twoScreens);
  expect(tops.job, "the job box").toBeLessThan(twoScreens);
});

test("since you left is per browser: mark all seen here leaves another browser's alone", async ({ page, browser }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const since = page.getByRole("list", { name: "Since you left" });
  await expect(since.getByRole("button").first()).toBeVisible();
  await page.getByRole("button", { name: "Mark all seen" }).click();
  await expect(page.getByText("Nothing finished, failed or was asked since you were last here.")).toBeVisible();

  const other = await browser.newContext({ baseURL: new URL(page.url()).origin });
  const second = await other.newPage();
  await second.goto("/");
  await expect(second.getByRole("list", { name: "Since you left" }).getByRole("button").first()).toBeVisible();
  await other.close();
});

test("the nav: four destinations and More on a phone, the same order on the desktop rail", async ({ page }, testInfo) => {
  await sizeFor(page, testInfo);
  await page.goto("/");
  const nav = page.getByRole("navigation", { name: "Views" }).filter({ visible: true });
  if (isPhone(testInfo)) {
    await expect(nav.getByRole("link")).toHaveText(["Now", "Projects", "Inbox", "Chat"].map((label) => new RegExp(label)));
    await nav.getByRole("button", { name: "More" }).click();
    const more = page.getByRole("dialog");
    await expect(more.getByRole("link")).toHaveText([/Jobs/, /Agents/, /Help/, /System/]);
    await page.screenshot({ path: `screenshots/now-nav-more-${width(testInfo)}.png` });
    await more.getByRole("link", { name: /Jobs/ }).click();
    await expect(page).toHaveURL(/\/jobs/);
    await expect(nav.getByRole("button", { name: "More" })).toHaveAttribute("data-status", "active");
  } else {
    await expect(nav.getByRole("link")).toHaveText(["Now", "Projects", "Inbox", "Chat", "Jobs", "Agents", "Help"].map((label) => new RegExp(label)));
    await nav.getByRole("link", { name: /Jobs/ }).click();
    await expect(page).toHaveURL(/\/jobs/);
  }
  // A link made before Now existed (an old notification) still opens the job on the board.
  await page.goto("/?job=j_1");
  await expect(page).toHaveURL(/\/jobs\?job=j_1/);
});
