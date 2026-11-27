// Publish to your tailnet (DESIGN.md 3.5.1) against `crowsnest --demo` with the login on and a
// FAKE tailscale (internal/tsserve/testdata/fake-tailscale, named by CROWSNEST_DEMO_TAILSCALE;
// a --demo instance never runs any other). From the System page at 390 and 1440 px: the offer
// with the exact address, Publish, the link and the QR code, Stop publishing, with axe and
// screenshots on the way; and on the desktop, the published address gone after crowsnest is
// SIGKILLed.
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";
import { expect, expectAccessible, expectNoSidewaysScroll, sameOriginHeaders, test as base, TEST_LOGIN } from "../support/fixtures";

const FAKE_TAILSCALE = resolve(import.meta.dirname, "../../../internal/tsserve/testdata/fake-tailscale");

const test = base.extend<{ tsDir: string }>({
  // eslint-disable-next-line no-empty-pattern
  tsDir: async ({}, provide) => {
    const dir = mkdtempSync("/tmp/cnts-");
    await provide(dir);
    rmSync(dir, { recursive: true, force: true });
  },
  appOptions: async ({ tsDir }, provide) => provide({ demo: true, auth: TEST_LOGIN, env: { CROWSNEST_DEMO_TAILSCALE: FAKE_TAILSCALE, FAKE_TS_DIR: tsDir } }),
});

/** The fake's running foreground `tailscale serve`, or 0. */
function foregroundPID(dir: string): number {
  const file = join(dir, "fg.pid");
  return existsSync(file) ? Number(readFileSync(file, "utf8").trim()) : 0;
}

function alive(pid: number): boolean {
  if (pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

for (const size of [
  { name: "390", width: 390, height: 844 },
  { name: "1440", width: 1440, height: 900 },
]) {
  test(`publish from Settings → Tailnet at ${size.name}, see the address and QR, stop`, async ({ page, app, tsDir }, testInfo) => {
    await page.setViewportSize({ width: size.width, height: size.height });
    const login = await page.request.post(`${app.baseURL}/api/v1/auth/login`, { headers: sameOriginHeaders(app), data: TEST_LOGIN });
    expect(login.ok()).toBe(true);
    await page.goto("/settings#tailnet"); // R8 moved it off the System page
    const card = page.getByRole("region", { name: "Publish to your tailnet" });
    await expect(card.getByText("https://quark.tail1234.ts.net", { exact: true })).toBeVisible();
    await expect(card.getByText(/gets crowsnest's sign-in page there/)).toBeVisible();
    await card.scrollIntoViewIfNeeded();
    await expectAccessible(page, testInfo, `publish offer ${size.name}`);
    await page.screenshot({ path: testInfo.outputPath(`tailnet-offer-${size.name}.png`), fullPage: false });

    await card.getByRole("button", { name: "Publish" }).click();
    await expect(card.getByText("publishing", { exact: true })).toBeVisible();
    await expect(card.getByRole("link", { name: /https:\/\/quark\.tail1234\.ts\.net/ })).toHaveAttribute("href", "https://quark.tail1234.ts.net");
    const qr = card.getByRole("img", { name: "QR code for https://quark.tail1234.ts.net" });
    await expect(qr).toBeVisible();
    // Decoded, not a broken image: the CSP (img-src 'self' data: blob:) lets the data: PNG through.
    await expect.poll(() => qr.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth)).toBeGreaterThan(100);
    const csp = (await page.request.get(`${app.baseURL}/system`)).headers()["content-security-policy"] ?? "";
    expect(csp).toContain("img-src 'self' data:");
    const pid = foregroundPID(tsDir);
    expect(alive(pid), "the fake tailscale serve runs").toBe(true);
    const argv = readFileSync(join(tsDir, "argv.log"), "utf8");
    expect(argv).toContain(`serve --https=443 http://127.0.0.1:${app.port}`);
    expect(argv).not.toMatch(/funnel|--bg|reset/);
    await card.scrollIntoViewIfNeeded();
    await expectNoSidewaysScroll(page);
    await expectAccessible(page, testInfo, `publishing ${size.name}`);
    await page.screenshot({ path: testInfo.outputPath(`tailnet-publishing-${size.name}.png`), fullPage: false });

    await card.getByRole("button", { name: "Stop publishing" }).click();
    await expect(card.getByText("stopped", { exact: true })).toBeVisible();
    await expect.poll(() => alive(pid), { timeout: 10_000 }).toBe(false);
    await expect(card.getByRole("button", { name: "Publish" })).toBeVisible();
  });
}

test("the published address goes when crowsnest is killed", async ({ page, app, tsDir }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop-chromium", "one engine is enough for a process test");
  await page.request.post(`${app.baseURL}/api/v1/auth/login`, { headers: sameOriginHeaders(app), data: TEST_LOGIN });
  const published = await page.request.post(`${app.baseURL}/api/v1/tailscale/serve`, { headers: sameOriginHeaders(app), data: {} });
  expect(published.ok()).toBe(true);
  await expect.poll(() => alive(foregroundPID(tsDir)), { timeout: 10_000 }).toBe(true);
  const pid = foregroundPID(tsDir);
  await app.kill(); // SIGKILL: no shutdown code runs in crowsnest
  await expect.poll(() => alive(pid), { timeout: 10_000 }).toBe(false);
});
