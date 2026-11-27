// The Fleet view (DESIGN.md 12) against `crowsnest --demo`: the tree of workspaces, tabs and
// agents with the ones that need you first, the filter, an agent's detail with its Conversation
// and Terminal tabs, a plain shell pane's terminal, and a link that lands on an agent.
//
// Demo fleet (internal/herdr/fake/demo.go, names in internal/demostory): workspace w1
// "orchestrator"; one per project, each agent named after the job it holds: w2 "crowsnest"
// (job-1 in w2:p1 with a transcript, job-11, job-12, job-13 at its force-push approval's dialog,
// job-6, a shell in tab "shell" w2:p6), w3 "website" (job-2, job-14 at a dialog, job-15), w4
// "mobile" (job-3, job-16 at a dialog, the hand-started Codex agent "linter" in w4:p3), w5
// "infra" (job-17), w6 "handbook" (job-5 at a terminal dialog, w6:p1); then the leads' workspaces.
import type { Page } from "@playwright/test";
import { detailRegion, expect, isPhone, test } from "../support/fixtures";

async function openFleet(page: Page, search = "") {
  await page.goto(`/fleet${search}`);
  await expect(page.getByRole("list", { name: "Workspaces" })).toBeVisible();
}

/** Crowsnest's workspace row (not its lead's, "lead crowsnest"). */
const crowsnestWorkspace = /(?<!lead )crowsnest\s*(\d+ at a dialog\s*)?\d+ agents?/;

/** The workspace labels in the order the tree shows them. */
async function workspaceOrder(page: Page): Promise<string[]> {
  const groups = page.getByRole("list", { name: "Workspaces" }).locator(":scope > li > button");
  return (await groups.allInnerTexts()).map((text) => text.split("\n").map((part) => part.trim()).filter(Boolean)[1] ?? text);
}

test("the tree shows every workspace, tab and agent, with the ones that need you first", async ({ page }) => {
  await openFleet(page);
  const tree = page.getByRole("list", { name: "Workspaces" });
  for (const name of ["orchestrator", "job-1(?!\\d)", "job-12", "job-5(?!\\d)", "job-17", "linter"]) {
    await expect(tree.getByRole("button", { name: new RegExp(name) }).first()).toBeVisible();
  }
  // The workspaces holding an agent at a dialog sort first, in herdr's order, and say so; the
  // orchestrator's and Infra's, where nothing waits, come after them.
  expect((await workspaceOrder(page)).slice(0, 6)).toEqual(["crowsnest", "website", "mobile", "handbook", "orchestrator", "infra"]);
  await expect(tree.getByRole("button", { name: crowsnestWorkspace })).toContainText("1 at a dialog");
  await expect(tree.getByRole("button", { name: /handbook\s*1 at a dialog\s*1 agent/ })).toBeVisible();
  const handbook = tree.getByRole("button", { name: /job-5(?!\d)/ });
  await expect(handbook).toHaveAttribute("data-needs-you", "true");
  await expect(handbook).toContainText("at a dialog");
  // The shell pane is listed as a pane, not an agent.
  await expect(tree.getByRole("button", { name: /^shell w2:p6/i })).toBeVisible();

  // A workspace collapses and expands.
  const crowsnest = tree.getByRole("button", { name: crowsnestWorkspace });
  await crowsnest.click();
  await expect(crowsnest).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("list", { name: "Tabs in crowsnest" })).toHaveCount(0);
  await crowsnest.click();
  await expect(page.getByRole("list", { name: "Tabs in crowsnest" })).toBeVisible();
});

test("the filter narrows the tree by agent, workspace or path, and clears", async ({ page }) => {
  await openFleet(page);
  const filter = page.getByRole("searchbox", { name: "Filter the fleet" }).or(page.getByRole("textbox", { name: "Filter the fleet" }));
  await filter.fill("lint");
  const tree = page.getByRole("list", { name: "Workspaces" });
  await expect(tree.getByRole("button", { name: /linter/ })).toBeVisible();
  await expect(tree.getByRole("button", { name: /job-1(?!\d)/ })).toHaveCount(0);
  await expect(tree.getByRole("button", { name: /^shell/i })).toHaveCount(0);
  await filter.fill("no-such-agent-anywhere");
  await expect(tree.getByRole("button", { name: /linter|job-/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Clear filter" }).click();
  await expect(filter).toHaveValue("");
  await expect(tree.getByRole("button", { name: /job-1(?!\d)/ })).toBeVisible();
});

test("an agent's detail: its conversation, then its terminal", async ({ page }, testInfo) => {
  await openFleet(page);
  await page.getByRole("list", { name: "Workspaces" }).getByRole("button", { name: /job-1(?!\d)/ }).click();
  await expect(page).toHaveURL(/agent=w2%3Ap1|agent=w2:p1/);
  const detail = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("article", { name: "Agent job-1" });
  await expect(detail.getByText("w2:p1", { exact: true })).toBeVisible();
  // The webhook builder (job-1) has a transcript, so the conversation opens first.
  await expect(detail.getByRole("tab", { name: "Conversation" })).toHaveAttribute("aria-selected", "true");
  await expect(detail.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(detail.getByRole("textbox", { name: "Message to job-1" })).toBeVisible();

  await detail.getByRole("tab", { name: "Terminal" }).click();
  await expect(detail.locator("[data-terminal]")).toBeVisible();
  await expect(detail.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();

  // A reload lands on the same agent.
  await page.reload();
  await expect((isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("article", { name: "Agent job-1" })).getByText("w2:p1", { exact: true })).toBeVisible();
});

test("an agent at a dialog shows the dialog card in its detail", async ({ page }, testInfo) => {
  await openFleet(page, "?agent=w6%3Ap1");
  const detail = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("article", { name: "Agent job-5" });
  await expect(detail.getByLabel("Dialog from job-5")).toBeVisible();
  // Codex agents have no transcript: the linter opens on its terminal.
  await openFleet(page, "?agent=w4%3Ap3");
  const linter = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("article", { name: "Agent linter" });
  await expect(linter.getByRole("tab", { name: "Terminal" })).toHaveAttribute("aria-selected", "true");
});

test("a plain shell pane opens a terminal on that pane", async ({ page }, testInfo) => {
  await openFleet(page);
  await page.getByRole("button", { name: /^shell w2:p6/i }).click();
  const detail = detailRegion(page, testInfo, "unused").or(page.getByRole("article", { name: "Shell w2:p6" }));
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
  await page.locator("[data-terminal]").click();
  await page.keyboard.type("hello pane", { delay: 10 });
  await page.keyboard.press("Enter");
  await expect.poll(() => page.locator("[data-terminal] .xterm-rows").innerText()).toContain("you typed: hello pane");
  await expect(detail.first()).toBeVisible();
});

test("an unknown agent in the URL shows the tree with nothing selected", async ({ page }) => {
  await openFleet(page, "?agent=w99%3Ap9");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Workspaces" })).toBeVisible();
});
