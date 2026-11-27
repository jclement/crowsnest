// The command palette (DESIGN.md 12, "Command palette") as a user drives it, against
// `crowsnest --demo`: open it with the chord and with the header button, give a job from it and
// see it on the board, jump to a job, answer a question from its options, navigate (rows, g
// sequences, ?), Esc gives focus back, the chord inside the terminal, rows that follow live data,
// and the layout and accessibility rules.
//
// The chord depends on the platform (⌘K on a Mac, Ctrl K elsewhere), and the browser's platform
// is the host's; each test that presses it pins the platform with an init script so the suite
// means the same on a Mac and on a Linux runner.
//
// Demo jobs (internal/jobs/demo.go): j_3 "Migrate the settings page to the new form kit" (stuck on
// a limit). Demo questions (internal/questions/demo.go) include "Apply the fleet cache refactor?"
// (confirm, blocking: "Apply it" or "Not yet"). Demo agents (internal/herdr/fake/demo.go): the
// orchestrator, the linter, and job-<n> for each job with a pane (job-12 reviews the fleet cache).
// A job given here is j_23 (the landing demo is j_18 to j_22).
import type { Page } from "@playwright/test";
import { askAsAgent } from "../support/agent";
import { expect, expectAccessible, expectNoSidewaysScroll, isPhone, test } from "../support/fixtures";

type Platform = "mac" | "other";

/** Pins what lib/hotkeys.ts sees as the platform. */
async function pinPlatform(page: Page, platform: Platform) {
  await page.addInitScript((value) => {
    Object.defineProperty(Navigator.prototype, "platform", { get: () => (value === "mac" ? "MacIntel" : "Linux x86_64") });
    Object.defineProperty(Navigator.prototype, "userAgentData", { get: () => undefined });
  }, platform);
}

const chord = (platform: Platform) => (platform === "mac" ? "Meta+k" : "Control+k");
const palette = (page: Page) => page.getByRole("dialog", { name: "Command palette" });
const input = (page: Page) => page.getByRole("combobox", { name: "Search or run a command" });
const activeOption = (page: Page) => palette(page).locator("[role=option][aria-selected=true]");

async function openBoard(page: Page) {
  await page.goto("/jobs"); // Jobs moved off home (Now) to /jobs
  await expect(page.getByRole("region", { name: "Needs you or stuck" })).toBeVisible();
}

/** Opens the palette the way a phone or a mouse does: the header button. */
async function openWithButton(page: Page) {
  await page.getByRole("button", { name: "Search or run…" }).click();
  await expect(input(page)).toBeFocused();
}

test("the chord opens it from anywhere, even a text box; Esc closes it and gives focus back", async ({ page }) => {
  await pinPlatform(page, "other");
  await openBoard(page);
  const box = page.getByLabel("What needs doing?");
  await box.click();
  await box.pressSequentially("draft");
  await page.keyboard.press(chord("other"));
  await expect(palette(page)).toBeVisible();
  await expect(input(page)).toBeFocused();
  // Ctrl+K never typed a k anywhere.
  await expect(box).toHaveValue("draft");
  await page.keyboard.press("Escape");
  await expect(palette(page)).toBeHidden();
  await expect(box).toBeFocused();
  // The chord toggles.
  await page.keyboard.press(chord("other"));
  await expect(palette(page)).toBeVisible();
  await page.keyboard.press(chord("other"));
  await expect(palette(page)).toBeHidden();
});

test("on a Mac it is ⌘K, and Ctrl+K is left to the text field", async ({ page }) => {
  await pinPlatform(page, "mac");
  await openBoard(page);
  await expect(page.getByRole("button", { name: "Search or run…" })).toHaveAttribute("aria-keyshortcuts", "Meta+K");
  await page.getByLabel("What needs doing?").click();
  await page.keyboard.press("Control+k");
  await expect(palette(page)).toBeHidden();
  await page.keyboard.press("Meta+k");
  await expect(palette(page)).toBeVisible();
});

test("a plain k, and ? or g while typing, do nothing", async ({ page }) => {
  await openBoard(page);
  const box = page.getByLabel("What needs doing?");
  await box.click();
  await box.pressSequentially("kg?gi");
  await expect(box).toHaveValue("kg?gi");
  await expect(palette(page)).toBeHidden();
  await expect(page).toHaveURL(/\/jobs$/);
});

test("the header button opens it (a full-screen sheet on a phone), with the first row active", async ({ page }, testInfo) => {
  await openBoard(page);
  await openWithButton(page);
  const box = await palette(page).boundingBox();
  if (isPhone(testInfo)) {
    expect(box?.width).toBe(375);
    expect(box?.height).toBeGreaterThan(700);
    await palette(page).getByRole("button", { name: "Cancel" }).click();
  } else {
    expect(box?.width).toBeLessThan(700);
    await page.keyboard.press("Escape");
  }
  await expect(palette(page)).toBeHidden();
  // Focus goes back to the button, where the engine gave it focus: WebKit (Safari) never
  // focuses a tapped or clicked button, so there is nothing to give back there.
  if (testInfo.project.name !== "mobile-webkit") await expect(page.getByRole("button", { name: "Search or run…" })).toBeFocused();
});

test("giving a job from the palette puts it on the board, and the toast opens it", async ({ page }, testInfo) => {
  await openBoard(page);
  await openWithButton(page);
  await input(page).fill("Rewrite the onboarding emails for new users");
  await expect(activeOption(page)).toContainText("Give job: Rewrite the onboarding emails for new users");
  await page.keyboard.press("Enter");
  await expect(palette(page)).toBeHidden();
  await expect(page.getByText("Job j_23 given")).toBeVisible();
  await expect(page.getByRole("region", { name: "In progress" }).or(page.getByRole("region", { name: "Needs you or stuck" })).getByText("Rewrite the onboarding emails for new users").first()).toBeVisible();
  await page.getByRole("button", { name: "Open j_23" }).click();
  await expect(page).toHaveURL(/job=j_23/);
  const detail = isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("region", { name: "Selected job" });
  await expect(detail.getByRole("heading", { name: "Rewrite the onboarding emails for new users" })).toBeVisible();
});

test("⌘/Ctrl+Enter on Give job opens the full form with the text in it", async ({ page }) => {
  await pinPlatform(page, "other");
  await page.goto("/chat");
  await page.keyboard.press(chord("other"));
  await input(page).fill("> Tidy the README");
  await page.keyboard.press("Control+Enter");
  await expect(page).toHaveURL(/\/jobs$/);
  await expect(page.getByLabel("What needs doing?")).toHaveValue("Tidy the README");
  await expect(page.getByLabel(/^Where/)).toBeVisible();
});

test("jumping to a job by its title or id opens it", async ({ page }, testInfo) => {
  await openBoard(page);
  await openWithButton(page);
  await input(page).fill("settings form kit");
  await expect(activeOption(page)).toContainText("Migrate the settings page to the new form kit");
  await expect(activeOption(page)).toContainText("Stuck");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/job=j_3/);
  if (isPhone(testInfo)) await page.getByRole("dialog").getByRole("button", { name: "Close" }).click();
  await openWithButton(page);
  await input(page).fill("j_7");
  const job = palette(page).getByRole("group", { name: "Jobs" }).getByRole("option").first();
  await expect(job).toContainText("Upgrade the Postgres driver");
  await job.click();
  await expect(page).toHaveURL(/job=j_7/);
});

test("a question is answered from its options in the palette", async ({ page, request }) => {
  await openBoard(page);
  await openWithButton(page);
  await input(page).fill("apply refactor");
  await expect(activeOption(page)).toContainText("Apply the fleet cache refactor?");
  await page.keyboard.press("ArrowRight");
  const actions = palette(page).getByRole("listbox", { name: /Actions for q_/ });
  await expect(actions.getByRole("option")).toHaveText([/Open in Inbox/, /^Answer: Apply it \(recommended\)/, /^Answer: Not yet/]);
  await actions.getByRole("option", { name: /^Answer: Apply it/ }).click();
  await expect(page.getByText(/Answered q_\d+: Apply it/)).toBeVisible();
  const answered = await request.get("/api/v1/questions?status=answered");
  const list = (await answered.json()) as { data: Array<{ title: string; answer_value: unknown }> };
  expect(list.data.find((question) => question.title === "Apply the fleet cache refactor?")?.answer_value).toBe("yes");
});

test("navigating: a view, the shortcuts sheet with ?, and g i / g f", async ({ page }, testInfo) => {
  await openBoard(page);
  await openWithButton(page);
  await input(page).fill("fleet");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/fleet$/);
  if (isPhone(testInfo)) return; // no keyboard sequences on a phone
  await page.locator("body").click({ position: { x: 5, y: 300 } });
  await page.keyboard.press("g");
  await page.keyboard.press("i");
  await expect(page).toHaveURL(/\/inbox$/);
  await page.keyboard.press("g");
  await page.keyboard.press("j");
  await expect(page).toHaveURL(/\/jobs$/);
  await page.keyboard.press("g");
  await page.keyboard.press("h");
  await expect(page).toHaveURL(/\/$/);
  await page.keyboard.press("Shift+?");
  const sheet = page.getByRole("dialog", { name: "Keyboard shortcuts" });
  await expect(sheet).toBeVisible();
  await expect(sheet.getByText("Go to Inbox")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
});

test("an agent opens straight to its terminal, and the theme changes from the palette", async ({ page }) => {
  await openBoard(page);
  await openWithButton(page);
  await input(page).fill("theme dark");
  await page.keyboard.press("Enter");
  await expect(page.locator("html")).toHaveClass(/dark/);
  await expect(page.getByRole("button", { name: /Dark theme/ })).toBeVisible();
  await openWithButton(page);
  await input(page).fill("job-12");
  const agent = palette(page).getByRole("group", { name: "Agents and panes" }).getByRole("option").first();
  await expect(agent).toContainText("job-12");
  await agent.hover();
  await page.keyboard.press("ArrowRight");
  await palette(page).getByRole("option", { name: "Open terminal" }).click();
  await expect(page).toHaveURL(/view=terminal/);
  await expect(page.getByRole("tab", { name: "Terminal", selected: true })).toBeVisible();
});

for (const platform of ["mac", "other"] as const) {
  test(`in the terminal (${platform}) only ⌘K opens it; Ctrl+K and k reach the shell`, async ({ page }, testInfo) => {
    test.skip(isPhone(testInfo), "a phone has no chord");
    await pinPlatform(page, platform);
    await page.goto("/terminal");
    await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
    const rows = page.locator("[data-terminal] .xterm-rows");
    await expect.poll(() => rows.innerText()).toContain("demo terminal");
    await page.locator("[data-terminal]").click();
    await page.keyboard.press("Control+k");
    await expect(palette(page)).toBeHidden();
    await page.keyboard.type("kk", { delay: 10 });
    await expect.poll(() => rows.innerText()).toContain("kk");
    await page.keyboard.press("Meta+k");
    if (platform === "mac") {
      await expect(palette(page)).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(palette(page)).toBeHidden();
      // Focus is back in the terminal: what is typed reaches the shell.
      await expect(page.getByRole("textbox", { name: "Terminal input" })).toBeFocused();
      await page.keyboard.type("z", { delay: 10 });
      await expect.poll(() => rows.innerText()).toContain("kkz");
    } else {
      await expect(palette(page)).toBeHidden();
    }
  });
}

test("rows follow live data: a question asked while it is open appears, and the selection stays put", async ({ page, app }) => {
  await openBoard(page);
  await openWithButton(page);
  await input(page).fill("e");
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowDown");
  const before = await activeOption(page).getAttribute("id");
  const beforeText = (await activeOption(page).textContent()) ?? "";
  await askAsAgent(app, { title: "Rename the e2e helper?", kind: "confirm", recommend: "yes", urgency: "blocking" });
  await expect(palette(page).getByRole("option").filter({ hasText: "Rename the e2e helper?" })).toBeVisible();
  await expect(activeOption(page)).toHaveText(beforeText);
  expect(before).toBeTruthy();
});

test("layout and accessibility: axe clean, no sideways scroll, 44 px rows on touch", async ({ page }, testInfo) => {
  await openBoard(page);
  await openWithButton(page);
  await expect(palette(page).getByRole("option").first()).toBeVisible();
  await expectAccessible(page, testInfo, "palette-empty");
  await input(page).fill("job");
  await expect(palette(page).getByRole("status")).toHaveText(/\d+ results?/);
  await expectAccessible(page, testInfo, "palette-query");
  await expectNoSidewaysScroll(page);
  if (isPhone(testInfo)) {
    const heights = await palette(page).getByRole("option").evaluateAll((options) => options.map((option) => option.getBoundingClientRect().height));
    expect(Math.min(...heights)).toBeGreaterThanOrEqual(44);
  }
  await page.keyboard.press("ArrowRight");
  await expectAccessible(page, testInfo, "palette-actions");
});
