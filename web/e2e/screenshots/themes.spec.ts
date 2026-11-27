// The theme contact sheets (DESIGN.md "Design language"), against `crowsnest --demo`: Now in
// every theme, light and dark, at 1440 and 390 px, composed into one sheet per theme
// (screenshots/themes-<id>.png) and one sheet of all of them at 1440 (themes-all.png); and the
// Appearance gallery (themes-appearance-1440.png, themes-appearance-390.png).
// Run with `mise run screenshots:themes`.
import type { Browser, Page } from "@playwright/test";
import { expect, test } from "../support/fixtures";

const themes = ["crowsnest", "phosphor", "deep-sea", "ember", "contrast"] as const;
const modes = ["light", "dark"] as const;
const sizes = [
  { name: "1440", width: 1440, height: 900 },
  { name: "390", width: 390, height: 844 },
] as const;

type Shot = { theme: string; mode: string; size: string; png: Buffer };

async function nowShot(page: Page, theme: string, mode: string, width: number, height: number): Promise<Buffer> {
  await page.setViewportSize({ width, height });
  await page.addInitScript(([id, value]) => {
    localStorage.setItem("crowsnest-theme-name", id);
    localStorage.setItem("crowsnest-theme", value);
  }, [theme, mode] as const);
  await page.goto("/");
  await expect(page.getByRole("list", { name: "Projects" })).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
  await page.evaluate(() => document.fonts.ready);
  return page.screenshot({ animations: "disabled", caret: "hide" });
}

/** Lays shots out on a page of their own (no app, no CSP) and photographs it. */
async function compose(browser: Browser, path: string, title: string, rows: Array<Array<{ label: string; png: Buffer; width: number }>>) {
  const page = await browser.newPage({ viewport: { width: 1600, height: 900 }, deviceScaleFactor: 1 });
  const cell = (item: { label: string; png: Buffer; width: number }) =>
    `<figure><figcaption>${item.label}</figcaption><img style="width:${item.width}px" src="data:image/png;base64,${item.png.toString("base64")}"></figure>`;
  await page.setContent(`<!doctype html><html><head><style>
    body{margin:0;padding:24px;background:#1b1f24;color:#e6e9ee;font:13px ui-monospace,Menlo,monospace}
    h1{font-size:15px;margin:0 0 16px;letter-spacing:.06em;text-transform:uppercase}
    .row{display:flex;gap:16px;align-items:flex-start;margin-bottom:16px}
    figure{margin:0} figcaption{margin-bottom:6px;color:#9aa3ad}
    img{display:block;border:1px solid #3a414a}
  </style></head><body><h1>${title}</h1>${rows.map((row) => `<div class="row">${row.map(cell).join("")}</div>`).join("")}</body></html>`);
  await page.waitForFunction(() => [...document.images].every((image) => image.complete));
  await page.screenshot({ path, fullPage: true });
  await page.close();
}

test.describe.configure({ mode: "serial" });

const shots: Shot[] = [];

for (const theme of themes) {
  test(`Now in ${theme}, light and dark, 1440 and 390`, async ({ page, browser }) => {
    for (const mode of modes) for (const size of sizes) shots.push({ theme, mode, size: size.name, png: await nowShot(page, theme, mode, size.width, size.height) });
    const mine = (mode: string, size: string) => shots.find((shot) => shot.theme === theme && shot.mode === mode && shot.size === size)!.png;
    await compose(browser, `screenshots/themes-${theme}.png`, `crowsnest · theme ${theme} · Now`, [
      [
        { label: "light · 1440", png: mine("light", "1440"), width: 720 },
        { label: "dark · 1440", png: mine("dark", "1440"), width: 720 },
      ],
      [
        { label: "light · 390", png: mine("light", "390"), width: 312 },
        { label: "dark · 390", png: mine("dark", "390"), width: 312 },
      ],
    ]);
  });
}

test("all themes at 1440, and the Appearance gallery", async ({ page, browser }) => {
  test.skip(shots.length < themes.length * modes.length * sizes.length, "needs the per-theme shots from the tests above");
  await compose(
    browser,
    "screenshots/themes-all.png",
    "crowsnest · every theme · Now at 1440",
    themes.map((theme) => modes.map((mode) => ({ label: `${theme} · ${mode}`, png: shots.find((shot) => shot.theme === theme && shot.mode === mode && shot.size === "1440")!.png, width: 640 }))),
  );
  for (const size of sizes) {
    await page.setViewportSize({ width: size.width, height: size.height });
    await page.goto("/");
    await expect(page.getByRole("list", { name: "Projects" })).toBeVisible();
    await page.getByRole("button", { name: "Account" }).click();
    await page.getByRole("menuitem", { name: "Appearance" }).click();
    const sheet = page.getByRole("dialog", { name: "Appearance" });
    await expect(sheet.locator('[data-theme="contrast"]')).toHaveCount(2);
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({ path: `screenshots/themes-appearance-${size.name}.png`, animations: "disabled", caret: "hide" });
  }
});
