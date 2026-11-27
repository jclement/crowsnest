// The terminal's font chain (DESIGN.md 12, "Terminal font") against `crowsnest --demo`, whose toy
// terminal prints a glyph test line (internal/term/demo.go, DemoGlyphs): one or more characters
// from each fallback face, an emoji, and a "|" whose column shows whether the cells stayed on
// the grid. For each project (desktop Chromium, phone Chromium, phone WebKit) it checks that
//
// - drawing the line made the browser fetch exactly the chunk of the intended face for each
//   character (the faces are declared by unicode-range, so nothing else would load them), and
//   nothing it did not draw (no Material Design icons, which are not on screen);
// - each character really comes from that face: drawn on a canvas in the terminal's font stack it
//   has ink and matches, pixel for pixel, the same character drawn by a probe face made from that
//   face's fetched chunk (so it is the face's glyph, not a tofu box or another font's);
// - the "|" sits in the column Unicode 11 widths put it in (the emoji is two cells), within a
//   pixel, so fallback glyphs and the emoji did not push the rest of the row off the grid;
// - every font came from /assets/ with the immutable cache header, and no CSP violation happened.
//
// The installed JetBrainsMono Nerd Font Mono comes first in the chain (local() only) and covers
// most of the line, so on a machine that has it (Jeff's) nothing would be fetched: the spec turns
// it off with the terminal's localStorage switch and tests the self-hosted chain, which is what a
// phone gets.
//
// It also photographs the terminal at 1440 (desktop Chromium) and 375 (phone WebKit) into
// web/screenshots/terminal-glyphs-*.png. A real iPhone is not checked here.
import type { Page } from "@playwright/test";
import { expect, isPhone, test } from "../support/fixtures";

/** Each fallback character on the test line and the face that must draw it. */
const GLYPHS: { char: string; family: string }[] = [
  { char: "", family: "Symbols Nerd Font Mono" }, // powerline branch
  { char: "", family: "Symbols Nerd Font Mono" }, // powerline separators
  { char: "", family: "Symbols Nerd Font Mono" },
  { char: "", family: "Symbols Nerd Font Mono" },
  { char: "", family: "Symbols Nerd Font Mono" },
  { char: "", family: "Symbols Nerd Font Mono" }, // devicons: Node.js
  { char: "", family: "Symbols Nerd Font Mono" }, // devicons: Rust
  { char: "", family: "Symbols Nerd Font Mono" }, // Font Awesome: GitHub
  { char: "┌", family: "DejaVu Sans Mono" }, // box drawing
  { char: "─", family: "DejaVu Sans Mono" },
  { char: "⌘", family: "DejaVu Sans Mono" },
  { char: "⠃", family: "Noto Sans Symbols 2" }, // braille
  { char: "⡇", family: "Noto Sans Symbols 2" },
  { char: "\u{1fb00}", family: "Noto Sans Symbols 2" }, // legacy computing sextant
  { char: "∮", family: "Noto Sans Math" },
  { char: "\u{1d465}", family: "Noto Sans Math" }, // mathematical italic x
];

const EMOJI = "😀";

const rows = (page: Page) => page.locator("[data-terminal] .xterm-rows");

/** The faces among GLYPHS whose chunk for that character is not (yet) loaded. */
async function unloadedFaces(page: Page): Promise<string[]> {
  return page.evaluate((glyphs) => {
    const covers = (range: string, point: number) =>
      range.split(",").some((part) => {
        const [first, last] = part.trim().replace(/^U\+/i, "").split("-");
        const low = parseInt(first ?? "", 16);
        return point >= low && point <= (last ? parseInt(last, 16) : low);
      });
    const faces = [...document.fonts];
    return glyphs
      .filter(({ char, family }) => {
        const point = char.codePointAt(0) ?? 0;
        return !faces.some((face) => face.family.replace(/["']/g, "") === family && face.status === "loaded" && covers(face.unicodeRange, point));
      })
      .map(({ char, family }) => `U+${(char.codePointAt(0) ?? 0).toString(16)} (${family})`);
  }, GLYPHS);
}

/** The chunk file-name prefix of each face (fonts/terminal/<prefix><first code point>.woff2). */
const CHUNK_PREFIX: Record<string, string> = {
  "Symbols Nerd Font Mono": "nerd-",
  "DejaVu Sans Mono": "dejavu-",
  "Noto Sans Math": "math-",
  "Noto Sans Symbols 2": "symbols2-",
};

/**
 * Characters the terminal does not draw with their face's glyph. Each is drawn on a canvas in the
 * terminal's own font stack and compared with the same character drawn by a probe face built
 * from each chunk of the intended face that the page fetched: one must match, with ink. (Comparing
 * with plain serif would not do: this Mac has Noto Sans Symbols 2 installed, so the system's
 * fallback for a sextant is the same font.) document.fonts.check must agree too.
 */
async function notFromTheirFace(page: Page): Promise<string[]> {
  return page.evaluate(
    async ({ glyphs, prefixes }) => {
      const canvas = document.createElement("canvas");
      canvas.width = canvas.height = 64;
      const context = canvas.getContext("2d", { willReadFrequently: true });
      if (!context) return ["no canvas"];
      const draw = (font: string, char: string) => {
        context.clearRect(0, 0, 64, 64);
        context.font = font;
        context.fillStyle = "#000";
        // The alphabetic baseline: "middle" is placed from the first font's metrics, which would
        // put the stack (JetBrains Mono first) and the probe at different heights.
        context.textBaseline = "alphabetic";
        context.fillText(char, 8, 44);
        return [...context.getImageData(0, 0, 64, 64).data];
      };
      const same = (left: number[], right: number[]) => left.every((value, index) => value === right[index]);
      const rowsElement = document.querySelector("[data-terminal] .xterm-rows");
      if (!rowsElement) return ["no terminal rows"];
      const stack = getComputedStyle(rowsElement).fontFamily;
      const fetched = performance
        .getEntriesByType("resource")
        .map((entry) => entry.name)
        .filter((url) => /\.woff2$/.test(url));
      const probes = new Map<string, string>(); // chunk URL -> probe family
      const bad: string[] = [];
      for (const { char, family } of glyphs) {
        const name = `U+${(char.codePointAt(0) ?? 0).toString(16)} (${family})`;
        if (!document.fonts.check(`32px "${family}"`, char)) bad.push(`${name}: fonts.check false`);
        const chunks = fetched.filter((url) => (url.split("/").pop() ?? "").startsWith(prefixes[family] ?? "?"));
        for (const url of chunks) {
          if (probes.has(url)) continue;
          const probeFamily = `crowsnest-probe-${probes.size}`;
          document.fonts.add(await new FontFace(probeFamily, `url(${url})`).load());
          probes.set(url, probeFamily);
        }
        const drawn = draw(`32px ${stack}`, char);
        if (!drawn.some((value, index) => index % 4 === 3 && value > 0)) {
          bad.push(`${name}: blank`);
          continue;
        }
        const fromFace = chunks.some((url) => same(drawn, draw(`32px "${probes.get(url)}"`, char)));
        if (!fromFace) bad.push(`${name}: not drawn by any fetched chunk of ${family} (${chunks.length} fetched)`);
      }
      return bad;
    },
    { glyphs: GLYPHS, prefixes: CHUNK_PREFIX },
  );
}

/** How far (px) the "|" ending the glyph line is from where its column should put it. */
async function gridDrift(page: Page): Promise<{ drift: number; expectedColumn: number }> {
  return page.evaluate((emoji) => {
    const row = [...document.querySelectorAll<HTMLElement>("[data-terminal] .xterm-rows > div")].find((element) => element.textContent?.includes("glyphs "));
    if (!row) throw new Error("no glyph row");
    const text = row.textContent ?? "";
    const before = text.slice(0, text.indexOf("|"));
    // Unicode 11 widths: the emoji is two cells, everything else on the line one.
    const expectedColumn = [...before].reduce((sum, char) => sum + (char === emoji ? 2 : 1), 0);
    const rangeOf = (needle: string) => {
      const walker = document.createTreeWalker(row, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        const at = node.textContent?.indexOf(needle) ?? -1;
        if (at >= 0) {
          const range = document.createRange();
          range.setStart(node, at);
          range.setEnd(node, at + needle.length);
          return range.getBoundingClientRect();
        }
      }
      throw new Error(`no ${needle} in the row`);
    };
    // xterm's own cell width: its measure element holds 32 W's. (A Range over the row's text
    // would do on Chromium, but WebKit rounds Range widths to whole pixels.)
    const measure = document.querySelector<HTMLElement>("[data-terminal] .xterm-char-measure-element");
    if (!measure?.textContent) throw new Error("no xterm measure element");
    const cell = measure.getBoundingClientRect().width / measure.textContent.length;
    const rowLeft = row.getBoundingClientRect().left;
    const bar = rangeOf("|");
    return { drift: Math.abs(bar.left - (rowLeft + expectedColumn * cell)), expectedColumn };
  }, EMOJI);
}

test("the glyph test line draws every character from its intended face, on the grid", async ({ page }, testInfo) => {
  const violations: string[] = [];
  page.on("console", (message) => {
    if (/Content.Security.Policy|Refused to load/i.test(message.text())) violations.push(message.text());
  });
  const fonts: { url: string; cacheControl: string; status: number }[] = [];
  page.on("response", (response) => {
    if (/\.woff2?(\?|$)/.test(response.url())) fonts.push({ url: response.url(), cacheControl: response.headers()["cache-control"] ?? "", status: response.status() });
  });
  await page.addInitScript(() => {
    localStorage.setItem("crowsnest-terminal-local-font", "off");
    document.addEventListener("securitypolicyviolation", (event) => console.error(`Content-Security-Policy violation: ${event.violatedDirective} ${event.blockedURI}`));
  });

  await page.goto("/agents?session=true");
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
  await expect.poll(() => rows(page).innerText()).toContain("glyphs ");

  await expect.poll(() => unloadedFaces(page), { message: "every fallback face's chunk is fetched for its glyph" }).toEqual([]);
  expect(await notFromTheirFace(page)).toEqual([]);
  // The re-measure after the last font arrived runs on the next frame; give it a moment.
  await expect.poll(async () => (await gridDrift(page)).drift, { message: "the | after the glyphs stays in its column" }).toBeLessThanOrEqual(1);

  for (const font of fonts) {
    expect(new URL(font.url).pathname, "fonts are bundled assets").toMatch(/^\/assets\/[^/]+\.woff2$/);
    expect(font.status).toBe(200);
    expect(font.cacheControl).toBe("public, max-age=31536000, immutable");
  }
  const names = fonts.map((font) => new URL(font.url).pathname.split("/").pop() ?? "");
  expect(names.some((name) => name.startsWith("nerd-e000-")), "the powerline chunk was fetched").toBe(true);
  expect(names.filter((name) => /^nerd-f[0-9a-f]{4}-/.test(name)), "Material Design icons are not on screen, so not fetched").toEqual([]);
  expect(violations).toEqual([]);

  const shot = testInfo.project.name === "desktop-chromium" ? "1440" : testInfo.project.name === "mobile-webkit" ? "375" : null;
  if (shot) await page.screenshot({ path: `screenshots/terminal-glyphs-${shot}.png`, animations: "disabled", caret: "hide" });
  await testInfo.attach("fonts fetched", { body: names.join("\n"), contentType: "text/plain" });
  if (isPhone(testInfo)) expect(names.length, "a phone fetches only the chunks on screen").toBeLessThan(20);
});
