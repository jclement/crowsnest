// Renders web/public/icons/crowsnest.svg to the PNG icons the manifest and iOS need. Run once
// with `node scripts/make-icons.mjs` from web/ (uses Playwright's Chromium); the PNGs are
// committed, so the build never runs this.
import { chromium } from "@playwright/test";
import { readFileSync } from "node:fs";

const svg = readFileSync("public/icons/crowsnest.svg", "utf8");
const inner = svg.replace(/^[\s\S]*?<svg[^>]*>/, "").replace(/<\/svg>\s*$/, "").replace(/<rect[^>]*\/>/, "");
// "any": the icon as drawn (rounded square). "maskable": a full-bleed square with the drawing
// shrunk into the central 80 % safe zone, so any mask shape (circle, squircle) keeps it whole.
const any = (size) => `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 64 64">${svg.replace(/^[\s\S]*?<svg[^>]*>/, "").replace(/<\/svg>\s*$/, "")}</svg>`;
const full = (size, scale) => `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 64 64"><rect width="64" height="64" fill="#08111b"/><g transform="translate(${32 - 32 * scale} ${32 - 32 * scale}) scale(${scale})">${inner}</g></svg>`;
const outputs = [
  ["public/icons/icon-192.png", any(192), 192],
  ["public/icons/icon-512.png", any(512), 512],
  ["public/icons/maskable-512.png", full(512, 0.72), 512],
  ["public/icons/apple-touch-icon.png", full(180, 0.86), 180],
];
const browser = await chromium.launch();
const page = await browser.newPage();
for (const [path, markup, size] of outputs) {
  await page.setViewportSize({ width: size, height: size });
  await page.setContent(`<html><body style="margin:0;background:transparent">${markup}</body></html>`);
  await page.locator("svg").screenshot({ path, omitBackground: true });
  console.log("wrote", path);
}
await browser.close();
