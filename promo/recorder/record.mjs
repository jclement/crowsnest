// Records every scene of out/timeline.json into out/clips/<scene>.mp4 (1920x1080, constant frame
// rate, exactly the scene's length), and renders the caption plates into out/captions/.
//
//   node recorder/record.mjs                 every scene
//   node recorder/record.mjs give nudge      only these scenes (the rest keep their clips)
//   node recorder/record.mjs --captions      only the caption plates
//
// App and phone scenes are the real app: ./bin/crowsnest --demo --no-auth on a free localhost
// port, a fresh instance per scene so every scene starts from the same seeded state. The page is
// captured with Chromium's screencast (a frame on every repaint, timestamped), and the frames are
// laid onto a constant 30 fps clock with ffmpeg. The scene's actions (scenes.mjs) wait on the
// narration's line starts, so the picture follows the words.
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { REPO, startDemo } from "./server.mjs";
import { stageScript } from "./stage.mjs";
import { PHONE, captionChunks, renderCaption, renderCard, renderPhoneFrame } from "./cards.mjs";
import { SCENES } from "./scenes.mjs";

const require = createRequire(join(REPO, "web/package.json"));
const { chromium } = require("playwright");

const PROMO = join(REPO, "promo");
const OUT = join(PROMO, "out");
const timeline = JSON.parse(readFileSync(join(OUT, "timeline.json"), "utf8"));
const FPS = timeline.fps;

const args = process.argv.slice(2);
const captionsOnly = args.includes("--captions");
const only = args.filter((a) => !a.startsWith("--"));

const sleep = (ms) => new Promise((r) => setTimeout(r, Math.max(0, ms)));
const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);

function ffmpeg(argv) {
  execFileSync("ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", ...argv], { stdio: ["ignore", "inherit", "inherit"] });
}

/** Lays timestamped frames onto a constant clock: an exact-length H.264 clip. */
function framesToClip(frames, duration, file, { filter = "scale=1920:1080:flags=lanczos", inputs = [] } = {}) {
  const list = [];
  for (let i = 0; i < frames.length; i++) {
    const end = i + 1 < frames.length ? frames[i + 1].t : duration;
    const dur = Math.max(0.001, end - Math.max(0, frames[i].t));
    if (end <= 0) continue; // superseded before the clock started
    list.push(`file '${frames[i].file}'`, `duration ${dur.toFixed(4)}`);
  }
  list.push(`file '${frames.at(-1).file}'`);
  const listFile = `${file}.txt`;
  writeFileSync(listFile, list.join("\n") + "\n");
  const extra = inputs.flatMap((f) => ["-loop", "1", "-i", f]);
  ffmpeg(["-f", "concat", "-safe", "0", "-i", listFile, ...extra, "-filter_complex", `[0:v]fps=${FPS},${filter},format=yuv420p[v]`,
    "-map", "[v]", "-t", duration.toFixed(3), "-r", String(FPS), "-c:v", "libx264", "-preset", "medium", "-crf", "16", file]);
  rmSync(listFile);
}

/** Captures a page with the screencast until stop() is called; frames land in dir. */
async function startCapture(page, dir, { maxWidth, maxHeight }) {
  mkdirSync(dir, { recursive: true });
  const cdp = await page.context().newCDPSession(page);
  const frames = [];
  let t0 = null;
  let n = 0;
  cdp.on("Page.screencastFrame", ({ data, metadata, sessionId }) => {
    cdp.send("Page.screencastFrameAck", { sessionId }).catch(() => {});
    if (t0 === null) return;
    const file = join(dir, `${String(n++).padStart(5, "0")}.jpg`);
    writeFileSync(file, Buffer.from(data, "base64"));
    frames.push({ t: metadata.timestamp - t0, file });
  });
  // A first frame from a real screenshot, so the clip never starts black.
  const first = join(dir, "first.jpg");
  await page.screenshot({ path: first, type: "jpeg", quality: 92 });
  await cdp.send("Page.startScreencast", { format: "jpeg", quality: 92, maxWidth, maxHeight, everyNthFrame: 1 });
  t0 = Date.now() / 1000;
  frames.push({ t: 0, file: first });
  return {
    t0: () => t0,
    stop: async () => {
      await cdp.send("Page.stopScreencast").catch(() => {});
      await sleep(100);
      frames.sort((a, b) => a.t - b.t);
      return frames;
    },
  };
}

/** The helpers a scene function gets: timing on the narration, cursor moves, clicks, highlights. */
function sceneContext(page, scene, demo, { phone }) {
  let t0 = Date.now() / 1000;
  const now = () => Date.now() / 1000 - t0;
  let mouse = { x: 720, y: 460 };
  const center = async (target) => {
    const loc = typeof target === "string" ? page.locator(target).first() : target;
    await loc.waitFor({ state: "visible", timeout: 8000 });
    await loc.scrollIntoViewIfNeeded().catch(() => {});
    const b = await loc.boundingBox();
    if (!b) throw new Error(`no box for ${target}`);
    return { x: b.x + b.width / 2, y: b.y + b.height / 2, box: b, loc };
  };
  const ctx = {
    page, scene, demo, phone,
    base: demo.baseURL,
    now,
    restartClock: (t) => { t0 = t; },
    /** Waits until the scene clock reaches t seconds. */
    at: async (t) => sleep((t - now()) * 1000),
    /** Waits until narration line i (1-based) starts, plus a delay. */
    line: async (i, plus = 0) => ctx.at((scene.lines[i - 1]?.offset ?? 0) + plus),
    /** The end of line i (1-based), for pacing an action to finish with the words. */
    lineEnd: (i) => scene.lines[i - 1].offset + scene.lines[i - 1].duration,
    wait: (s) => sleep(s * 1000),
    /** Glides the cursor to an element (or {x, y}) over ms milliseconds. */
    async move(target, ms = 650) {
      const to = target.x !== undefined && !target.waitFor ? target : await center(target);
      const from = { ...mouse };
      const steps = Math.max(8, Math.round(ms / 16));
      const start = Date.now();
      for (let i = 1; i <= steps; i++) {
        const k = ease(i / steps);
        await page.mouse.move(from.x + (to.x - from.x) * k, from.y + (to.y - from.y) * k);
        await sleep(start + (ms * i) / steps - Date.now());
      }
      mouse = { x: to.x, y: to.y };
      return to;
    },
    async click(target, { ms = 650, pause = 180 } = {}) {
      if (phone) return ctx.tap(target);
      if (process.env.PROMO_DEBUG) console.log(`    ${scene.id} ${now().toFixed(1)}s click ${target}`);
      await ctx.move(target, ms);
      await sleep(pause);
      await page.mouse.down();
      await sleep(90);
      await page.mouse.up();
    },
    async tap(target) {
      if (process.env.PROMO_DEBUG) console.log(`    ${scene.id} ${now().toFixed(1)}s tap ${target}`);
      const c = await center(target);
      await page.evaluate(({ x, y }) => window.__promo.tap(x, y), { x: c.x, y: c.y });
      await sleep(160);
      await c.loc.tap();
    },
    /** Types into a field at a readable pace (characters per second). */
    async type(target, text, cps = 22) {
      await ctx.click(target);
      await sleep(200);
      for (const ch of text) {
        await page.keyboard.type(ch);
        await sleep(1000 / cps * (ch === " " ? 0.7 : 1) * (0.75 + Math.random() * 0.5));
      }
    },
    /** Dims everything but target (an element, or several unioned). */
    async spot(targets, { pad = 8, dim = true } = {}) {
      const list = Array.isArray(targets) ? targets : [targets];
      const boxes = [];
      for (const t of list) {
        const loc = typeof t === "string" ? page.locator(t).first() : t;
        await loc.first().waitFor({ state: "visible", timeout: 8000 });
        for (const one of await loc.all()) if (await one.isVisible()) boxes.push(await one.boundingBox());
      }
      const x = Math.min(...boxes.map((b) => b.x)), y = Math.min(...boxes.map((b) => b.y));
      const r = Math.max(...boxes.map((b) => b.x + b.width)), btm = Math.max(...boxes.map((b) => b.y + b.height));
      await page.evaluate(([rect, o]) => window.__promo.spot(rect, o), [{ x, y, width: r - x, height: btm - y }, { pad, dim }]);
    },
    unspot: () => page.evaluate(() => window.__promo.unspot()),
    /** Smoothly scrolls the nearest scrollable ancestor so target sits `at` (0..1) down the viewport. */
    async scrollTo(target, at = 0.3, ms = 900) {
      const loc = typeof target === "string" ? page.locator(target).first() : target;
      await loc.waitFor({ state: "attached", timeout: 8000 });
      await loc.evaluate((el, [at, ms]) => new Promise((done) => {
        let p = el.parentElement;
        while (p && !(p.scrollHeight > p.clientHeight + 4 && /(auto|scroll)/.test(getComputedStyle(p).overflowY))) p = p.parentElement;
        const sc = p || document.scrollingElement;
        const target = sc.scrollTop + el.getBoundingClientRect().top - (p ? p.getBoundingClientRect().top : 0) - sc.clientHeight * at;
        const from = sc.scrollTop, start = performance.now();
        const k = (t) => (t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2);
        const step = (now) => {
          const f = Math.min(1, (now - start) / ms);
          sc.scrollTop = from + (target - from) * k(f);
          if (f < 1) requestAnimationFrame(step); else done();
        };
        requestAnimationFrame(step);
      }), [at, ms]);
    },
  };
  return ctx;
}

async function recordLive(browser, scene, dir) {
  const def = SCENES[scene.id];
  if (!def) throw new Error(`no recording for scene ${scene.id} in scenes.mjs`);
  const phone = scene.kind === "phone";
  const demo = await startDemo({ voice: !!def.voice, setup: !!scene.setup });
  const context = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 810 },
    deviceScaleFactor: phone ? 2 : 4 / 3,
    isMobile: phone, hasTouch: phone,
    colorScheme: "dark",
    permissions: phone ? ["microphone", "notifications"] : ["microphone"],
    userAgent: phone ? "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1" : undefined,
  });
  await context.addInitScript(stageScript({ root: demo.root, cursor: !phone, folded: def.folded !== false }));
  const page = await context.newPage();
  page.setDefaultTimeout(8000);
  const ctx = sceneContext(page, scene, demo, { phone });
  try {
    await def.setup?.(ctx);
    await page.waitForTimeout(600);
    if (!phone) await page.mouse.move(ctx.phone ? 0 : 720, 460);
    const cap = await startCapture(page, dir, phone ? { maxWidth: 780, maxHeight: 1688 } : { maxWidth: 1920, maxHeight: 1080 });
    ctx.restartClock(cap.t0());
    const run = def.run(ctx).catch((e) => console.error(`  ! ${scene.id}: ${e.message.split("\n")[0]}`));
    await Promise.race([run, sleep(scene.duration * 1000 + 50)]);
    await ctx.at(scene.duration + 0.1);
    const frames = await cap.stop();
    console.log(`  ${scene.id}: ${frames.length} frames over ${scene.duration.toFixed(1)} s`);
    return frames;
  } finally {
    await context.close();
    await demo.stop();
  }
}

async function main() {
  mkdirSync(join(OUT, "clips"), { recursive: true });
  const browser = await chromium.launch({ args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream", "--autoplay-policy=no-user-gesture-required"] });
  try {
    if (!captionsOnly) {
      for (const scene of timeline.scenes) {
        if (only.length && !only.includes(scene.id)) continue;
        const dir = join(OUT, "frames", scene.id);
        rmSync(dir, { recursive: true, force: true });
        const clip = join(OUT, "clips", `${scene.id}.mp4`);
        if (scene.kind === "card") {
          const files = await renderCard(browser, scene, dir, FPS);
          framesToClip(files.map((file, i) => ({ t: i / FPS, file })), scene.duration, clip);
          console.log(`  ${scene.id}: card, ${files.length} frames`);
        } else if (scene.kind === "phone") {
          const frames = await recordLive(browser, scene, dir);
          const bg = join(dir, "bg.png"), bezel = join(dir, "bezel.png");
          await renderPhoneFrame(browser, { title: scene.title ?? "", subtitle: scene.subtitle ?? "" }, bg, bezel);
          framesToClip(frames, scene.duration, clip, {
            inputs: [bg, bezel],
            filter: `scale=${PHONE.w}:${PHONE.h}:flags=lanczos[ph];[1:v][ph]overlay=${PHONE.x}:${PHONE.y}[b];[b][2:v]overlay=0:0`,
          });
        } else {
          const frames = await recordLive(browser, scene, dir);
          framesToClip(frames, scene.duration, clip);
        }
      }
    }
    // Caption plates: one PNG per chunk, and the list the assembly overlays.
    const capDir = join(OUT, "captions");
    rmSync(capDir, { recursive: true, force: true });
    mkdirSync(capDir, { recursive: true });
    const plates = [];
    for (const scene of timeline.scenes) {
      for (const line of scene.lines) {
        for (const chunk of captionChunks(line)) {
          const file = join(capDir, `${String(plates.length).padStart(3, "0")}.png`);
          await renderCaption(browser, chunk.text, file);
          plates.push({ ...chunk, file });
        }
      }
    }
    writeFileSync(join(capDir, "plates.json"), JSON.stringify(plates, null, 2) + "\n");
    console.log(`  ${plates.length} caption plates`);
  } finally {
    await browser.close();
  }
}

await main();
process.exit(0);
