// The Chat view against `crowsnest --demo`, photographed at phone and desktop sizes in both
// themes, after a real prompt round trip through the scripted demo orchestrator (send, "working",
// the reply arriving over SSE). Also checks the 375 px rules: no sideways scroll, 44 px targets.
// Run with `mise run screenshots:chat` (each test gets its own demo instance).
import { type Page } from "@playwright/test";
import { expect, test } from "../support/fixtures";

const themes = ["light", "dark"] as const;
const sizes = [
  { name: "375x812", width: 375, height: 812 },
  { name: "1440x900", width: 1440, height: 900 },
] as const;

async function openChat(page: Page, theme: (typeof themes)[number]) {
  await page.addInitScript((value) => localStorage.setItem("crowsnest-theme", value), theme);
  await page.goto("/chat");
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

/** Sends a prompt and waits for the demo orchestrator's reply to arrive. */
async function roundTrip(page: Page, text: string) {
  const replies = page.getByRole("article", { name: "Reply" });
  const before = await replies.count();
  await page.getByRole("textbox", { name: /Message to the orchestrator/ }).fill(text);
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByTestId("chat-state")).toHaveText("Working");
  // At least one more: a fresh demo may also be answering the answer it was typed at start-up.
  await expect.poll(() => replies.count(), { timeout: 15_000 }).toBeGreaterThan(before);
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 10_000 });
}

for (const theme of themes) {
  for (const size of sizes) {
    test(`chat at ${size.name} (${theme})`, async ({ page }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      await openChat(page, theme);
      await roundTrip(page, `Status check from the ${theme} ${size.name} screenshot run`);
      // Open the newest tool card so the screenshot shows an expanded one beside the new reply.
      const conversation = page.getByRole("list", { name: "Conversation" });
      await conversation.getByRole("button", { name: /^(Bash|Read|Grep)/ }).last().click();
      // Expanding scrolled the card into view; end at the bottom, where a reader of the reply is.
      await page.evaluate(() => {
        for (const element of document.querySelectorAll<HTMLElement>("[data-scroll-container], main")) element.scrollTop = element.scrollHeight;
      });
      if (size.width < 768) {
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        expect(overflow).toBeLessThanOrEqual(0);
      }
      await page.screenshot({ path: `screenshots/chat-${size.name}-${theme}.png`, animations: "disabled", caret: "hide" });
    });
  }
}

test("tap targets in Chat are at least 44 px on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await openChat(page, "light");
  const small = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("main button, main textarea, main a[href]")]
      .filter((element) => {
        const box = element.getBoundingClientRect();
        return box.width > 0 && box.height > 0 && (box.width < 43.5 || box.height < 43.5);
      })
      .map((element) => `${element.tagName} "${element.getAttribute("aria-label") ?? element.textContent?.trim().slice(0, 30)}"`),
  );
  expect(small).toEqual([]);
});

