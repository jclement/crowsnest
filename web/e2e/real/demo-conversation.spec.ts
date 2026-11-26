// Conversations against the real binary (`crowsnest --demo --no-auth` on a random port): a
// Claude agent's Conversation tab in the Fleet panel renders its transcript and can be messaged,
// its Terminal tab still connects, and Chat still does a prompt round trip with the demo
// orchestrator. Added when M4 (conversations) was merged with M5 (terminals, real fleet types).
import { expect, test, type Page } from "@playwright/test";
import { startInstance, type Instance } from "../support/instance";

let instance: Instance;

test.beforeAll(async () => {
  instance = await startInstance({ demo: true });
});

test.afterAll(async () => {
  await instance?.stop();
});

function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  return errors;
}

test("a Claude agent's Conversation and Terminal tabs in Fleet", async ({ page }) => {
  const errors = collectErrors(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${instance.baseURL}/fleet`);
  await page.getByRole("list", { name: "Workspaces" }).getByRole("button", { name: /builder/ }).first().click();

  const conversation = page.getByRole("list", { name: "Conversation" });
  await expect(conversation).toBeVisible();
  await expect(conversation.getByText("7 tests")).toBeVisible();
  await expect(conversation.getByRole("button", { name: /^Bash/ })).toBeVisible();
  await page.screenshot({ path: "screenshots/fleet-conversation-1440x900-light.png", animations: "disabled", caret: "hide" });

  // Message the builder from the panel: the demo builder starts working.
  await page.getByRole("textbox", { name: /Message to builder/ }).fill("one more test please");
  const sent = page.waitForResponse((response) => response.url().includes("/prompt") && response.request().method() === "POST");
  await page.getByRole("button", { name: "Send" }).click();
  expect((await sent).status()).toBe(200);

  await page.getByRole("tab", { name: "Terminal" }).click();
  await expect.poll(() => page.locator("[data-terminal] .xterm-rows").innerText(), { timeout: 15_000 }).toMatch(/standing in for pane/);

  await page.getByRole("tab", { name: "Conversation" }).click();
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  expect(errors).toEqual([]);
});

test("Chat still round-trips with the demo orchestrator", async ({ page }) => {
  const errors = collectErrors(page);
  await page.goto(`${instance.baseURL}/chat`);
  const replies = page.getByRole("article", { name: "Reply" });
  await expect(replies.first()).toBeVisible();
  const before = await replies.count();
  await page.getByRole("textbox", { name: /Message to the orchestrator/ }).fill("merge check");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByTestId("chat-state")).toHaveText("Working");
  await expect(replies).toHaveCount(before + 1, { timeout: 15_000 });
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 10_000 });
  expect(errors).toEqual([]);
});
