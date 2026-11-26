// The web app against the real API: `crowsnest --demo --no-auth`, no fixtures. This is the proof
// that the client's types match what the server sends (DESIGN.md section 10): agents render, the
// demo's blocked docs writer shows its dialog card, and pressing an option is accepted.
import { expect, test } from "@playwright/test";
import { startInstance, type Instance } from "./instance";

let instance: Instance;

test.beforeAll(async () => {
  instance = await startInstance({ demo: true });
});

test.afterAll(async () => {
  await instance?.stop();
});

test("the fleet renders from the real API and a dialog press works", async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => consoleErrors.push(error.message));
  await page.goto(`${instance.baseURL}/fleet`);

  const tree = page.getByRole("list", { name: "Workspaces" });
  await expect(tree).toBeVisible();
  for (const name of ["orchestrator", "builder", "reviewer", "docs-writer", "linter"]) {
    await expect(tree.getByRole("button", { name: new RegExp(`^.*${name}`) }).first()).toBeVisible();
  }

  await tree.getByRole("button", { name: /docs-writer/ }).click();
  const card = page.getByRole("region", { name: "Dialog from docs-writer" }).or(page.getByLabel("Dialog from docs-writer"));
  await expect(card).toBeVisible();
  const options = card.getByRole("list", { name: "Options" }).getByRole("button");
  await expect(options.first()).toBeVisible();
  expect(await options.count()).toBeGreaterThanOrEqual(2);

  const pressed = page.waitForResponse((response) => response.url().includes("/keys") && response.request().method() === "POST");
  await options.first().click();
  const response = await pressed;
  expect(response.status()).toBe(200);
  await expect(page.getByText(/^Sent “1” to docs-writer/)).toBeVisible();
  expect(consoleErrors).toEqual([]);
});
