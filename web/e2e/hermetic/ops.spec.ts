// Day thirty on the System page (DESIGN.md 4.17 to 4.21), against `crowsnest --demo`: the Storage
// card with Download backup and a clean-up preview, Machines with a read token that opens the
// summary and nothing else, and Sign in with Tailscale off by default. At 375 and 1440 px.
import { expect, expectNoSidewaysScroll, sameOriginHeaders, test, TEST_LOGIN } from "../support/fixtures";

test.use({ appOptions: { demo: true } });

for (const size of [
  { name: "375", width: 375, height: 812 },
  { name: "1440", width: 1440, height: 900 },
]) {
  test(`System at ${size.name}: storage, a clean-up preview and a backup download`, async ({ page }) => {
    await page.setViewportSize({ width: size.width, height: size.height });
    await page.goto("/system");
    const storage = page.getByRole("region", { name: "Storage" });
    await expect(storage.getByText("Database", { exact: true })).toBeVisible();
    await storage.getByRole("button", { name: "Clean up now…" }).click();
    await expect(page.getByLabel("Clean-up preview")).toContainText("archive");
    await page.getByRole("button", { name: "Cancel" }).click();
    const download = page.waitForEvent("download");
    await storage.getByRole("link", { name: /Download backup/ }).click();
    expect((await download).suggestedFilename()).toMatch(/^crowsnest-.*\.tar\.gz$/);
    await expect(page.getByText("Sign in with Tailscale")).toBeVisible();
    await expect(page.getByText(/--tailscale-auth/).first()).toBeVisible();
    await expectNoSidewaysScroll(page);
  });
}

test.describe("with a login", () => {
  test.use({ appOptions: { demo: true, auth: TEST_LOGIN } });

  test("a read token minted on the Machines card opens the summary and nothing else", async ({ page, app }) => {
    const login = await page.request.post(`${app.baseURL}/api/v1/auth/login`, { headers: sameOriginHeaders(app), data: TEST_LOGIN });
    expect(login.ok()).toBe(true);
    await page.goto("/system");
    await page.getByLabel("Token name").fill("quark");
    await page.getByRole("button", { name: "Mint a token" }).click();
    const shown = page.locator("code", { hasText: /^cnk_/ });
    await expect(shown).toBeVisible();
    const token = (await shown.textContent()) ?? "";
    // Plain fetch carries no cookie: the token alone.
    const summary = await fetch(`${app.baseURL}/api/v1/summary`, { headers: { Authorization: `Bearer ${token}` } });
    expect(summary.status).toBe(200);
    expect(((await summary.json()) as { data: { name: string } }).data.name).toBeTruthy();
    for (const path of ["/api/v1/settings", "/api/v1/auth/me", "/api/v1/backup", "/api/v1/tokens"]) {
      const response = await fetch(`${app.baseURL}${path}`, { headers: { Authorization: `Bearer ${token}` } });
      expect(response.status, path).toBe(401);
    }
    const list = await page.request.get(`${app.baseURL}/api/v1/tokens`);
    const id = ((await list.json()) as { data: { id: number }[] }).data[0]?.id;
    expect(id).toBeDefined();
    await page.request.delete(`${app.baseURL}/api/v1/tokens/${id}`, { headers: sameOriginHeaders(app) });
    const after = await fetch(`${app.baseURL}/api/v1/summary`, { headers: { Authorization: `Bearer ${token}` } });
    expect(after.status).toBe(401);
  });
});
