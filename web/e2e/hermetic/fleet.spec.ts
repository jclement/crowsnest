// The Fleet view (DESIGN.md 12) against `crowsnest --demo`: the tree of workspaces, tabs and
// agents with the ones that need you first, the filter, an agent's detail with its Conversation
// and Terminal tabs, a plain shell pane's terminal, and a link that lands on an agent.
//
// Demo fleet (internal/herdr/fake/demo.go): workspace "orchestrator" (the orchestrator),
// "crowsnest" (builder, reviewer, a shell in tab "shell"), "website" (docs-writer at a dialog,
// linter, a Codex agent).
import type { Page } from "@playwright/test";
import { detailRegion, expect, isPhone, test } from "../support/fixtures";

async function openFleet(page: Page, search = "") {
  await page.goto(`/fleet${search}`);
  await expect(page.getByRole("list", { name: "Workspaces" })).toBeVisible();
}

/** The workspace labels in the order the tree shows them. */
async function workspaceOrder(page: Page): Promise<string[]> {
  const groups = page.getByRole("list", { name: "Workspaces" }).locator(":scope > li > button");
  return (await groups.allInnerTexts()).map((text) => text.split("\n").map((part) => part.trim()).filter(Boolean)[1] ?? text);
}

test("the tree shows every workspace, tab and agent, with the ones that need you first", async ({ page }) => {
  await openFleet(page);
  const tree = page.getByRole("list", { name: "Workspaces" });
  for (const name of ["orchestrator", "builder", "reviewer", "docs-writer", "linter"]) {
    await expect(tree.getByRole("button", { name: new RegExp(name) }).first()).toBeVisible();
  }
  // The workspace holding the agent at a dialog sorts first and says so.
  expect((await workspaceOrder(page))[0]).toBe("website");
  await expect(tree.getByRole("button", { name: /website/ }).first()).toContainText("1 need you");
  const docs = tree.getByRole("button", { name: /docs-writer/ });
  await expect(docs).toHaveAttribute("data-needs-you", "true");
  await expect(docs).toContainText("needs you");
  // The shell pane is listed as a pane, not an agent.
  await expect(tree.getByRole("button", { name: /^shell w2:p3/i })).toBeVisible();

  // A workspace collapses and expands.
  const crowsnest = tree.getByRole("button", { name: /(?<!lead )crowsnest\s*\d+ agents?/ });
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
  await expect(tree.getByRole("button", { name: /builder/ })).toHaveCount(0);
  await expect(tree.getByRole("button", { name: /^shell/i })).toHaveCount(0);
  await filter.fill("no-such-agent-anywhere");
  await expect(tree.getByRole("button", { name: /linter|builder|reviewer/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Clear filter" }).click();
  await expect(filter).toHaveValue("");
  await expect(tree.getByRole("button", { name: /builder/ })).toBeVisible();
});

test("an agent's detail: its conversation, then its terminal", async ({ page }, testInfo) => {
  await openFleet(page);
  await page.getByRole("list", { name: "Workspaces" }).getByRole("button", { name: /builder/ }).click();
  await expect(page).toHaveURL(/agent=w2%3Ap1|agent=w2:p1/);
  const detail = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("article", { name: "Agent builder" });
  await expect(detail.getByText("w2:p1", { exact: true })).toBeVisible();
  // The builder has a transcript, so the conversation opens first.
  await expect(detail.getByRole("tab", { name: "Conversation" })).toHaveAttribute("aria-selected", "true");
  await expect(detail.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(detail.getByRole("textbox", { name: "Message to builder" })).toBeVisible();

  await detail.getByRole("tab", { name: "Terminal" }).click();
  await expect(detail.locator("[data-terminal]")).toBeVisible();
  await expect(detail.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();

  // A reload lands on the same agent.
  await page.reload();
  await expect((isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("article", { name: "Agent builder" })).getByText("w2:p1", { exact: true })).toBeVisible();
});

test("an agent at a dialog shows the dialog card in its detail", async ({ page }, testInfo) => {
  await openFleet(page, "?agent=w3%3Ap1");
  const detail = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("article", { name: "Agent docs-writer" });
  await expect(detail.getByLabel("Dialog from docs-writer")).toBeVisible();
  // Codex agents have no transcript: the linter opens on its terminal.
  await openFleet(page, "?agent=w3%3Ap2");
  const linter = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("article", { name: "Agent linter" });
  await expect(linter.getByRole("tab", { name: "Terminal" })).toHaveAttribute("aria-selected", "true");
});

test("a plain shell pane opens a terminal on that pane", async ({ page }, testInfo) => {
  await openFleet(page);
  await page.getByRole("button", { name: /^shell w2:p3/i }).click();
  const detail = detailRegion(page, testInfo, "unused").or(page.getByRole("article", { name: "Shell w2:p3" }));
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
