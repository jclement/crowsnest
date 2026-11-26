// First-run setup from a test: open the link with the one-time token from the instance's log,
// fill the form, submit; and finishing the guided first run's checklist that follows.
import type { Page } from "@playwright/test";
import { expect } from "@playwright/test";
import type { Instance } from "./instance";

export const OWNER = { username: "owner", password: "e2e setup password one" } as const;

/** Opens the setup link and submits the form; the page then waits for the full server. */
export async function completeSetup(page: Page, app: Instance, name = "e2e-box") {
  const link = app.setupLink();
  await page.goto(link);
  // The token is moved out of the address bar at once, and prefilled.
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByLabel("Setup token")).toHaveValue(new URL(link).hash.replace("#token=", ""));
  await page.getByLabel("Username").fill(OWNER.username);
  await page.getByLabel("Password", { exact: true }).fill(OWNER.password);
  await page.getByLabel("Password again").fill(OWNER.password);
  await page.getByLabel("Instance name").fill(name);
  await page.getByRole("button", { name: "Set up crowsnest" }).click();
}

/** On the first-run checklist (after setup), presses Start and waits for Chat. */
export async function finishChecklist(page: Page) {
  await expect(page).toHaveURL(/\/welcome$/, { timeout: 20_000 });
  await page.getByRole("button", { name: "Start" }).click();
  await expect(page).toHaveURL(/\/chat$/, { timeout: 20_000 });
}
