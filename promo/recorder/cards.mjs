// Everything the film draws itself rather than records from the app: the title, section, install
// and closing cards, the caption plates, and the phone frame. All of it is HTML in the app's own
// palette and fonts (Inter and JetBrains Mono from web/node_modules, the mark from
// web/public/icons), rendered by headless Chromium.
//
// Cards are drawn frame by frame: every CSS animation on the page is paused and set to the
// frame's time before the screenshot, so a card is a pure function of time and never depends on
// how fast the machine is.
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { REPO } from "./server.mjs";

const FONTS = join(REPO, "web/node_modules/@fontsource-variable");
const MARK = readFileSync(join(REPO, "web/public/icons/crowsnest.svg"), "utf8");

export const PALETTE = {
  chrome: "#0b1220", surface: "#0d1117", raised: "#131a23", border: "#212b37", heading: "#e8edf3",
  body: "#c3cdd9", muted: "#8794a6", accent: "#4ea8f5", sky: "#38bdf8", warning: "#e3a43a",
  success: "#4cc36b", danger: "#f47067",
};
const P = PALETTE;

const BASE_CSS = `
@font-face { font-family: Inter; src: url("${pathToFileURL(join(FONTS, "inter/files/inter-latin-wght-normal.woff2"))}") format("woff2"); font-weight: 100 900; }
@font-face { font-family: Mono; src: url("${pathToFileURL(join(FONTS, "jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2"))}") format("woff2"); font-weight: 100 900; }
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body { width: 1920px; height: 1080px; overflow: hidden; }
body { font-family: Inter, sans-serif; color: ${P.heading}; -webkit-font-smoothing: antialiased;
  background: radial-gradient(ellipse 120% 90% at 50% 38%, #142238 0%, ${P.chrome} 55%, #070b14 100%); }
.mono { font-family: Mono, monospace; }
.mark svg { width: 100%; height: 100%; display: block; }
.grid { position: absolute; inset: 0; opacity: .5;
  background-image: linear-gradient(rgba(78,168,245,.05) 1px, transparent 1px), linear-gradient(90deg, rgba(78,168,245,.05) 1px, transparent 1px);
  background-size: 64px 64px; mask-image: radial-gradient(ellipse at 50% 45%, #000 20%, transparent 75%); }
@keyframes up { from { opacity: 0; transform: translateY(26px) } to { opacity: 1; transform: none } }
@keyframes fade { from { opacity: 0 } to { opacity: 1 } }
@keyframes out { from { opacity: 1 } to { opacity: 0 } }
@keyframes grow { from { transform: scaleX(0) } to { transform: scaleX(1) } }
@keyframes pop { 0% { opacity: 0; transform: scale(.6) } 70% { opacity: 1; transform: scale(1.06) } 100% { opacity: 1; transform: scale(1) } }
@keyframes blink { 0%, 49% { opacity: 1 } 50%, 100% { opacity: 0 } }
@keyframes type { from { width: 0 } to { width: var(--w) } }
.a { animation-fill-mode: both; animation-timing-function: cubic-bezier(.2,.8,.2,1); }
`;

const page = (body, extra = "") => `<!doctype html><html><head><meta charset="utf-8"><style>${BASE_CSS}${extra}</style></head><body>${body}</body></html>`;
const anim = (name, at, dur = 0.7) => `animation: ${name} ${dur}s ${at.toFixed(3)}s both cubic-bezier(.2,.8,.2,1);`;
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/** The HTML of one card scene. `scene` is its timeline entry (lines carry their offsets). */
export function cardHTML(scene) {
  const L = scene.lines.map((l) => l.offset);
  const D = scene.duration;
  const fadeOut = `#all { ${anim("out", D - 0.45, 0.45)} }`;
  switch (scene.card) {
    case "problem": {
      // Line 1: "You give an agent a job, and it just works for a while. And then it stops."
      const l1 = scene.lines[0];
      const t = (f) => L[0] + l1.duration * f;
      const l2 = scene.lines[1];
      const u = (f) => L[1] + l2.duration * f;
      return page(`<div class="grid"></div><div id="all" style="position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:30px">
        <div style="font-size:84px;font-weight:650;letter-spacing:-1.5px;${anim("up", t(0.02))}">You give it a job.</div>
        <div style="font-size:84px;font-weight:650;letter-spacing:-1.5px;color:${P.body};${anim("up", t(0.36))}">It works for a while.</div>
        <div style="font-size:84px;font-weight:650;letter-spacing:-1.5px;color:${P.warning};${anim("up", t(0.78))}">Then it stops.</div>
        <div style="display:flex;gap:26px;margin-top:56px;font-size:34px;color:${P.muted}">
          <span style="padding:14px 26px;border:1px solid ${P.border};border-radius:14px;background:${P.raised};${anim("pop", u(0.02), 0.5)}">Finished?</span>
          <span style="padding:14px 26px;border:1px solid ${P.border};border-radius:14px;background:${P.raised};${anim("pop", u(0.17), 0.5)}">Waiting on your answer?</span>
          <span style="padding:14px 26px;border:1px solid ${P.border};border-radius:14px;background:${P.raised};${anim("pop", u(0.55), 0.5)}">Just… stopped?</span>
        </div></div>`, fadeOut);
    }
    case "title":
      return page(`<div class="grid"></div><div id="all" style="position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center">
        <div class="mark" style="width:190px;height:190px;border-radius:40px;overflow:hidden;box-shadow:0 20px 60px rgba(56,189,248,.25);${anim("pop", 0.15, 0.8)}">${MARK}</div>
        <div style="font-size:140px;font-weight:700;letter-spacing:-4px;margin-top:44px;${anim("up", 0.45, 0.8)}">crowsnest</div>
        <div style="height:4px;width:520px;margin-top:26px;background:linear-gradient(90deg,transparent,${P.sky},transparent);transform-origin:center;${anim("grow", 0.9, 0.9)}"></div>
        <div style="font-size:42px;color:${P.body};margin-top:34px;${anim("up", L[0] + 0.6)}">Runs your coding agents. Keeps them working. Asks when it needs you.</div>
      </div>`, fadeOut);
    case "section":
      return page(`<div class="grid"></div><div id="all" style="position:absolute;inset:0;display:flex;align-items:center;justify-content:center;gap:56px">
        <div class="mono" style="font-size:170px;font-weight:700;color:${P.sky};${anim("pop", 0.05, 0.6)}">${esc(scene.section)}</div>
        <div style="display:flex;flex-direction:column;gap:18px">
          <div style="height:4px;width:120px;background:${P.sky};transform-origin:left;${anim("grow", 0.2, 0.6)}"></div>
          <div style="font-size:92px;font-weight:680;letter-spacing:-2px;${anim("up", 0.18, 0.7)}">${esc(scene.heading)}</div>
        </div></div>`, `#all { ${anim("out", D - 0.3, 0.3)} }`);
    case "install": {
      const cmd1 = "brew install jclement/tap/crowsnest";
      const cmd2 = "crowsnest";
      const w = (s) => `${s.length * 0.6}em`;
      return page(`<div class="grid"></div><div id="all" style="position:absolute;inset:0;display:flex;align-items:center;justify-content:center">
        <div style="width:1360px;border-radius:18px;overflow:hidden;background:#090d12;border:1px solid ${P.border};box-shadow:0 30px 90px rgba(0,0,0,.55);${anim("up", 0.05, 0.6)}">
          <div style="height:58px;background:${P.raised};border-bottom:1px solid ${P.border};display:flex;align-items:center;gap:12px;padding:0 24px">
            <i style="width:16px;height:16px;border-radius:50%;background:#f47067"></i><i style="width:16px;height:16px;border-radius:50%;background:#e3a43a"></i><i style="width:16px;height:16px;border-radius:50%;background:#4cc36b"></i>
            <span style="margin-left:18px;color:${P.muted};font-size:22px">Terminal</span></div>
          <div class="mono" style="padding:40px 46px;font-size:36px;line-height:1.75;min-height:420px">
            <div style="display:flex"><span style="color:${P.success}">$&nbsp;</span><span style="--w:${w(cmd1)};white-space:nowrap;overflow:hidden;${anim("type", 0.4, 1.2).replace("cubic-bezier(.2,.8,.2,1)", "steps(" + cmd1.length + ")")}">${cmd1}</span></div>
            <div style="color:${P.muted};font-size:28px;${anim("fade", 1.9, 0.2)}">==&gt; Installing crowsnest… ✓</div>
            <div style="display:flex"><span style="color:${P.success};${anim("fade", 2.3, 0.1)}">$&nbsp;</span><span style="--w:${w(cmd2)};white-space:nowrap;overflow:hidden;${anim("type", 2.4, 0.45).replace("cubic-bezier(.2,.8,.2,1)", "steps(" + cmd2.length + ")")}">${cmd2}</span></div>
            <div style="font-size:28px;color:${P.warning};${anim("fade", 3.0, 0.2)}">SETUP REQUIRED: open this link to choose the username and password</div>
            <div style="font-size:28px;color:${P.accent};text-decoration:underline;${anim("fade", 3.15, 0.2)}">http://localhost:2769/setup#token=…</div>
          </div></div></div>`, `#all { ${anim("out", D - 0.3, 0.3)} }`);
    }
    case "close":
      return page(`<div class="grid"></div><div id="all" style="position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center">
        <div style="display:flex;align-items:center;gap:40px;${anim("up", 0.1, 0.8)}">
          <div class="mark" style="width:150px;height:150px;border-radius:32px;overflow:hidden">${MARK}</div>
          <div style="font-size:120px;font-weight:700;letter-spacing:-3.5px">crowsnest</div></div>
        <div style="font-size:64px;font-weight:600;margin-top:60px;${anim("up", L[0] + 2.3, 0.8)}">Give it a job. <span style="color:${P.sky}">It keeps going.</span></div>
        <div class="mono" style="font-size:32px;color:${P.muted};margin-top:56px;${anim("fade", L[0] + 3.4, 0.8)}">brew install jclement/tap/crowsnest &nbsp;·&nbsp; github.com/jclement/crowsnest</div>
      </div>`, `#all { ${anim("out", D - 0.6, 0.6)} }`);
    default:
      throw new Error(`unknown card ${scene.card}`);
  }
}

/** Renders a card to JPEG frames, every animation set to the frame's time. Returns the frame files. */
export async function renderCard(browser, scene, dir, fps) {
  mkdirSync(dir, { recursive: true });
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
  const p = await ctx.newPage();
  const file = join(dir, "card.html");
  writeFileSync(file, cardHTML(scene));
  await p.goto(pathToFileURL(file).href);
  await p.evaluate(() => document.fonts.ready);
  const n = Math.round(scene.duration * fps);
  const files = [];
  for (let i = 0; i < n; i++) {
    const ms = (i / fps) * 1000;
    await p.evaluate((ms) => { for (const a of document.getAnimations()) { a.pause(); a.currentTime = ms; } }, ms);
    const f = join(dir, `${String(i).padStart(5, "0")}.jpg`);
    await p.screenshot({ path: f, type: "jpeg", quality: 92 });
    files.push(f);
  }
  await ctx.close();
  return files;
}

/**
 * Splits narration into caption plates of at most ~2 lines (maxChars), breaking at a sentence or
 * clause boundary near each ideal split point when there is one, and timed by share of characters.
 */
export function captionChunks(line, maxChars = 84) {
  const text = line.text;
  const n = Math.ceil(text.length / maxChars);
  const chunks = [];
  let from = 0;
  for (let k = 1; k < n; k++) {
    const ideal = from + (text.length - from) / (n - k + 1);
    let best = -1, score = Infinity;
    for (let i = from + 10; i < text.length - 10; i++) {
      if (text[i] !== " ") continue;
      const prev = text[i - 1];
      const penalty = /[.?!]/.test(prev) ? 0 : /[,:;]/.test(prev) ? 8 : 30;
      const len = i - from;
      if (len > maxChars) break;
      const sc = Math.abs(i - ideal) + penalty;
      if (sc < score) { score = sc; best = i; }
    }
    if (best < 0) break;
    chunks.push(text.slice(from, best));
    from = best + 1;
  }
  chunks.push(text.slice(from));
  const total = chunks.reduce((s, c) => s + c.length, 0);
  let t = line.start;
  return chunks.map((text) => {
    const dur = (line.duration * text.length) / total;
    const c = { text, start: t, end: t + dur };
    t += dur;
    return c;
  });
}

/** One caption plate as a transparent 1920x1080 PNG (lower third). */
export async function renderCaption(browser, text, file) {
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
  const p = await ctx.newPage();
  await p.setContent(page(`<div style="position:absolute;left:0;right:0;bottom:64px;display:flex;justify-content:center">
    <div style="max-width:1400px;padding:16px 34px 18px;border-radius:16px;background:rgba(7,11,20,.82);border:1px solid rgba(78,168,245,.28);
      box-shadow:0 10px 30px rgba(0,0,0,.45);font-size:40px;line-height:1.32;font-weight:520;text-align:center;color:#f1f5f9;text-wrap:balance">${esc(text)}</div></div>`,
    "html, body { background: transparent !important; }"));
  await p.evaluate(() => document.fonts.ready);
  await p.screenshot({ path: file, omitBackground: true });
  await ctx.close();
}

/** The phone scene's backdrop and bezel: two 1920x1080 PNGs, and where the screen sits. */
export const PHONE = { x: 1140, y: 58, w: 410, h: 888, radius: 52, bezel: 16 };

export async function renderPhoneFrame(browser, { title, subtitle }, bgFile, bezelFile) {
  const { x, y, w, h, radius, bezel } = PHONE;
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
  const p = await ctx.newPage();
  await p.setContent(page(`<div class="grid"></div>
    <div style="position:absolute;left:220px;top:330px;width:760px">
      <div class="mono" style="font-size:30px;color:${P.sky};letter-spacing:2px">5 · FROM YOUR PHONE</div>
      <div style="font-size:76px;font-weight:680;letter-spacing:-2px;margin-top:18px;line-height:1.08">${esc(title)}</div>
      <div style="font-size:34px;color:${P.body};margin-top:26px;line-height:1.4">${esc(subtitle)}</div>
    </div>
    <div style="position:absolute;left:${x - bezel}px;top:${y - bezel}px;width:${w + bezel * 2}px;height:${h + bezel * 2}px;border-radius:${radius + bezel}px;
      background:#05080d;box-shadow:0 40px 120px rgba(0,0,0,.6), 0 0 0 2px #2a3442"></div>`));
  await p.evaluate(() => document.fonts.ready);
  await p.screenshot({ path: bgFile });
  // The bezel: everything transparent except the frame, whose inner corners round the screen off.
  await p.setContent(page(`<div style="position:absolute;left:${x - bezel}px;top:${y - bezel}px;width:${w + bezel * 2}px;height:${h + bezel * 2}px;
      border-radius:${radius + bezel}px;border:${bezel}px solid #05080d;box-shadow:inset 0 0 0 ${radius}px transparent"></div>
    <div style="position:absolute;left:${x - bezel + 2}px;top:${y - bezel + 2}px;width:${w + bezel * 2 - 4}px;height:${h + bezel * 2 - 4}px;border-radius:${radius + bezel}px;box-shadow:0 0 0 2px #2a3442"></div>
    <svg style="position:absolute;left:${x}px;top:${y}px" width="${w}" height="${h}"><path fill="#05080d" fill-rule="evenodd"
      d="M0 0H${w}V${h}H0Z M${radius} 0 H${w - radius} A${radius} ${radius} 0 0 1 ${w} ${radius} V${h - radius} A${radius} ${radius} 0 0 1 ${w - radius} ${h} H${radius} A${radius} ${radius} 0 0 1 0 ${h - radius} V${radius} A${radius} ${radius} 0 0 1 ${radius} 0Z"/></svg>`,
    "html, body { background: transparent !important; }"));
  await p.screenshot({ path: bezelFile, omitBackground: true });
  await ctx.close();
}
