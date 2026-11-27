// Lays two runs of e2e/screenshots/tour.spec.ts side by side as one contact sheet: for every view
// and size, the first screen of the "before" shot beside the "after" one. Used for R8's
// before/after sheet (web/screenshots/r8-before-after.jpg).
//
//   node scripts/tour-sheet.mjs before after screenshots/r8-before-after.jpg
//
// Uses Playwright's Chromium (already a dev dependency) to draw an HTML page of data: images, so
// no image library is needed.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "@playwright/test";

const [before = "before", after = "after", out = "screenshots/r8-before-after.jpg"] = process.argv.slice(2);
const root = "screenshots/tour";
const names = readdirSync(join(root, after)).filter((name) => name.endsWith(".png")).sort();

/** A shot as a data: URL, or null when that run has none (a view added since). */
function image(run, name) {
  try {
    return `data:image/png;base64,${readFileSync(join(root, run, name)).toString("base64")}`;
  } catch {
    return null;
  }
}

const cell = (src, phone) =>
  src
    ? `<div class="shot ${phone ? "phone" : "desk"}"><img src="${src}"></div>`
    : `<div class="shot ${phone ? "phone" : "desk"} none">(no such view)</div>`;

const rows = names
  .map((name) => {
    const phone = name.endsWith("-390.png");
    return `<section><h2>${name.replace(".png", "")}</h2><div class="pair">${cell(image(before, name), phone)}${cell(image(after, name), phone)}</div></section>`;
  })
  .join("");

const html = `<!doctype html><meta charset="utf-8"><style>
body{font:14px system-ui;margin:16px;background:#fff;color:#111}
h1{font-size:20px} h2{font-size:14px;margin:18px 0 6px;font-family:monospace}
.pair{display:flex;gap:16px;align-items:flex-start}
.head{display:flex;gap:16px;font-weight:600}
.shot{border:1px solid #ccc;overflow:hidden;background:#f4f4f4}
.desk{width:720px;height:450px} .phone{width:390px;height:844px}
.shot img{width:100%;display:block} .none{display:flex;align-items:center;justify-content:center;color:#888}
</style><h1>R8 cuts: ${before} (left) and ${after} (right), first screen of each view</h1>${rows}`;

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1500, height: 900 } });
await page.setContent(html, { waitUntil: "load" });
await page.screenshot({ path: out, fullPage: true, type: "jpeg", quality: 70 });
await browser.close();
console.log(`wrote ${out} (${names.length} views)`);
