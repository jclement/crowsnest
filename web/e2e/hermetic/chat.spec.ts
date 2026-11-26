// Chat with the orchestrator (DESIGN.md 7 and 12), against `crowsnest --demo`, whose scripted
// orchestrator writes a Claude-shaped transcript and answers each prompt after 2.5 s: send,
// Working → reply → Idle; a tool card expands; a second send while it works is "queued"; a draft
// typed at the terminal stops the send until "Send anyway"; Restart and New conversation, each
// behind a confirmation; and a long conversation that follows new messages only while the reader
// is at the bottom, with a "new messages" pill otherwise.
//
// The long conversation is written straight into the demo orchestrator's transcript
// (support/agent.ts), the way Claude writes it, so the test does not wait on forty replies.
import type { Page } from "@playwright/test";
import { appendTurns } from "../support/agent";
import { expect, test } from "../support/fixtures";

async function openChat(page: Page) {
  await page.goto("/chat");
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 15_000 });
}

const composer = (page: Page) => page.getByRole("textbox", { name: "Message to the orchestrator" });

/** How far the conversation's scroll container is from its end, in px. */
function distanceFromBottom(page: Page): Promise<number> {
  return page.getByRole("list", { name: "Conversation" }).evaluate((list) => {
    let node: HTMLElement | null = list.closest<HTMLElement>("[data-scroll-container]");
    for (let cursor = list.parentElement; !node && cursor; cursor = cursor.parentElement) {
      const overflow = getComputedStyle(cursor).overflowY;
      if ((overflow === "auto" || overflow === "scroll") && cursor.scrollHeight > cursor.clientHeight) node = cursor;
    }
    const scroller = node ?? (document.scrollingElement as HTMLElement);
    return scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight;
  });
}

/** Scrolls the conversation's container to its top. */
function scrollToTop(page: Page): Promise<void> {
  return page.getByRole("list", { name: "Conversation" }).evaluate((list) => {
    for (let cursor: HTMLElement | null = list.parentElement; cursor; cursor = cursor.parentElement) {
      const overflow = getComputedStyle(cursor).overflowY;
      if ((overflow === "auto" || overflow === "scroll") && cursor.scrollHeight > cursor.clientHeight) {
        cursor.scrollTop = 0;
        cursor.dispatchEvent(new Event("scroll"));
        return;
      }
    }
    window.scrollTo(0, 0);
  });
}

test("send, Working, the reply arrives live, then Idle; a tool card expands", async ({ page }) => {
  await openChat(page);
  const replies = page.getByRole("article", { name: "Reply" });
  const before = await replies.count();
  await composer(page).fill("What is the builder doing?");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(composer(page)).toHaveValue("");
  await expect(page.getByTestId("chat-state")).toHaveText("Working");
  const conversation = page.getByRole("list", { name: "Conversation" });
  await expect(conversation.getByText("What is the builder doing?", { exact: true })).toBeVisible();
  await expect(replies).toHaveCount(before + 1, { timeout: 15_000 });
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 10_000 });
  // The reply is rendered as Markdown: a code block, not raw fences.
  await expect(replies.last()).not.toContainText("```");
  await expect(replies.last().locator("pre, p").first()).toBeVisible();

  // The newest tool call is one line; expanding shows its input and result.
  const tool = conversation.getByRole("button").filter({ hasText: /^(Bash|Read|Grep|Edit)/ }).last();
  await tool.click();
  await expect(tool).toHaveAttribute("aria-expanded", "true");
  const body = page.locator(`#${await tool.getAttribute("aria-controls")}`);
  await expect(body).toContainText(/Input|Result/);
  await tool.click();
  await expect(body).toHaveCount(0);
});

test("a send while the orchestrator works is labelled queued until it is idle again", async ({ page }) => {
  await openChat(page);
  await composer(page).fill("First question");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByTestId("chat-state")).toHaveText("Working");
  await expect(page.getByText("Working. What you send now is queued until this turn ends.")).toBeVisible();
  await composer(page).fill("Second question, while it works");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByText("Queued: Claude reads it when the current turn ends.")).toBeVisible();
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 20_000 });
  await expect(page.getByText("Queued: Claude reads it when the current turn ends.")).toHaveCount(0);
});

test("a draft typed at the terminal stops the send until 'Send anyway'", async ({ page }) => {
  await openChat(page);
  // The draft check itself is the server's (fleet.DetectDraft, unit-tested on captured screens);
  // here the server answers the first send with 409 DRAFT, as it does when the input box has text.
  const bodies: Array<Record<string, unknown>> = [];
  let refused = false; // set once the server-side refusal has been played
  await page.route("**/api/v1/agents/orchestrator/prompt", async (route) => {
    bodies.push(route.request().postDataJSON() as Record<string, unknown>);
    const body = route.request().postDataJSON() as { force_over_draft?: boolean };
    if (!body.force_over_draft) {
      refused = true;
      await route.fulfill({
        status: 409,
        json: { error: { code: "DRAFT", message: "Someone is typing in that terminal.", detail: { input_found: true, present: true, text: "git push --fo" } } },
      });
      return;
    }
    await route.continue();
  });
  await composer(page).fill("Deploy the docs");
  await page.getByRole("button", { name: "Send" }).click();
  const banner = page.getByRole("alert").filter({ hasText: "Someone typed a draft at the terminal" });
  await expect(banner).toContainText("git push --fo");
  await expect(composer(page)).toHaveValue("Deploy the docs");
  // Cancel keeps the text and sends nothing more.
  await banner.getByRole("button", { name: "Cancel" }).click();
  await expect(banner).toHaveCount(0);
  expect(bodies).toHaveLength(1);
  expect(refused).toBe(true);
  // Again, and this time append to the draft.
  await page.getByRole("button", { name: "Send" }).click();
  await page.getByRole("alert").filter({ hasText: "Someone typed a draft" }).getByRole("button", { name: "Send anyway" }).click();
  await expect(composer(page)).toHaveValue("");
  expect(bodies.at(-1)).toMatchObject({ text: "Deploy the docs", force_over_draft: true });
  expect(bodies[0]).toMatchObject({ force_over_draft: false });
});

test("Restart and New conversation each ask first; a new conversation starts empty and works", async ({ page }) => {
  await openChat(page);
  const menu = page.getByRole("button", { name: "Conversation menu" });

  await menu.click();
  await page.getByRole("menuitem", { name: "Restart" }).click();
  const confirm = page.getByRole("dialog", { name: "Restart the orchestrator?" });
  await expect(confirm).toContainText("started again on the same conversation");
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(confirm).toHaveCount(0);

  await menu.click();
  await page.getByRole("menuitem", { name: "Restart" }).click();
  await page.getByRole("dialog", { name: "Restart the orchestrator?" }).getByRole("button", { name: "Restart" }).click();
  await expect(page.getByText("Orchestrator restarted")).toBeVisible({ timeout: 30_000 });
  // Same conversation: the seeded messages are still there.
  await expect(page.getByRole("list", { name: "Conversation" })).toContainText("What's the state of the crowsnest build?");

  await menu.click();
  await page.getByRole("menuitem", { name: "New conversation" }).click();
  await page.getByRole("dialog", { name: "Start a new conversation?" }).getByRole("button", { name: "New conversation" }).click();
  await expect(page.getByText("New conversation started")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "No messages yet" })).toBeVisible({ timeout: 15_000 });

  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 15_000 });
  await composer(page).fill("Hello again");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByRole("article", { name: "Reply" })).toHaveCount(1, { timeout: 20_000 });
});

test("Show thinking reveals the thinking blocks the reader hides by default", async ({ page }) => {
  await openChat(page);
  const conversation = page.getByRole("list", { name: "Conversation" });
  await expect(conversation.getByText("Let me look at the fleet before answering")).toHaveCount(0);
  await page.getByRole("button", { name: "Conversation menu" }).click();
  await page.getByRole("menuitem", { name: "Show thinking" }).click();
  const thinking = conversation.getByRole("button", { name: /Thinking/ }).first();
  await thinking.click();
  await expect(conversation.getByText("Let me look at the fleet before answering")).toBeVisible();
});

test("a long conversation sticks to the bottom only when the reader is there, with a 'new messages' pill", async ({ app, page }) => {
  await openChat(page);
  appendTurns(app, Array.from({ length: 30 }, (_, index) => ({ user: `Question ${index + 1}`, reply: `Answer number ${index + 1}.\n\n${"Some detail. ".repeat(20)}` })));
  const conversation = page.getByRole("list", { name: "Conversation" });
  await expect(conversation.getByText("Answer number 30.")).toBeVisible({ timeout: 10_000 });
  // At the bottom when it arrived, so it followed.
  await expect.poll(() => distanceFromBottom(page)).toBeLessThan(60);

  // Scrolled up, new messages do not move the view; the pill counts them.
  await scrollToTop(page);
  await expect.poll(() => distanceFromBottom(page)).toBeGreaterThan(500);
  appendTurns(app, [{ user: "Question 31", reply: "Answer number 31." }]);
  const pill = page.getByRole("button", { name: /^\d+ new messages?$/ });
  await expect(pill).toHaveText("2 new messages", { timeout: 10_000 });
  expect(await distanceFromBottom(page)).toBeGreaterThan(500);
  // The view stayed where the reader put it: the first message shown is still in view.
  await expect(conversation.getByRole("listitem").first()).toBeInViewport();

  // Nothing covers the pill (on a phone the sticky composer once did: M9 bug).
  const covered = await pill.evaluate((element) => {
    const box = element.getBoundingClientRect();
    const top = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
    return top && element.contains(top) ? "" : `${JSON.stringify(box)} under ${top?.outerHTML.slice(0, 200)}`;
  });
  expect(covered, "the new-messages pill is covered").toBe("");
  // The pill takes the reader down and goes away.
  await pill.click();
  await expect.poll(() => distanceFromBottom(page)).toBeLessThan(60);
  await expect(pill).toHaveCount(0);
  await expect(conversation.getByText("Answer number 31.")).toBeInViewport();

  // At the bottom again, the next message is followed without a pill.
  appendTurns(app, [{ user: "Question 32", reply: "Answer number 32." }]);
  await expect(conversation.getByText("Answer number 32.")).toBeInViewport({ timeout: 10_000 });
  await expect(pill).toHaveCount(0);
});
