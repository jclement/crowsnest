// Starts ./bin/crowsnest --demo --no-auth on a free localhost port with every bit of its state in
// a throwaway directory, for the recorder. Nothing of the user's own crowsnest, herdr session or
// config is touched: HERDR_* and CROWSNEST_* variables are scrubbed from the child's environment.
import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, existsSync } from "node:fs";
import { createServer } from "node:net";
import { join, resolve } from "node:path";

export const REPO = resolve(import.meta.dirname, "../..");
export const BINARY = join(REPO, "bin/crowsnest");

async function freePort() {
  return new Promise((ok, fail) => {
    const s = createServer();
    s.once("error", fail);
    s.listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => ok(port));
    });
  });
}

/**
 * Starts a fake ElevenLabs (the e2e suite's, never the real API) so the demo has talk mode: the
 * mic button, the Voice sheet, spoken-summary lines. Resolves to { url, key, stop() }.
 */
async function startFakeVoice() {
  const port = await freePort();
  const key = "sk_fake_promo_key";
  const proc = spawn(process.execPath, [join(REPO, "web/e2e/support/fake-elevenlabs.mjs")], {
    env: { ...process.env, FAKE_ELEVENLABS_PORT: String(port), FAKE_ELEVENLABS_KEY: key,
      FAKE_ELEVENLABS_TRANSCRIPT: "What needs me right now?" },
    stdio: "ignore",
  });
  const url = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 100; i++) {
    try { await fetch(`${url}/__requests`); break; } catch { await new Promise((r) => setTimeout(r, 100)); }
  }
  return { url, key, stop: () => proc.kill("SIGKILL") };
}

/**
 * Starts a demo instance; resolves to { baseURL, root, log(), setupLink(), stop() }.
 * voice: talk mode against a fake ElevenLabs. setup: no --no-auth, so it starts locked in
 * first-run setup mode and logs a one-time setup link.
 */
export async function startDemo({ name = "workstation", voice = false, setup = false } = {}) {
  if (!existsSync(BINARY)) throw new Error(`${BINARY} is missing: run mise run build first`);
  const port = await freePort();
  const fake = voice ? await startFakeVoice() : null;
  const root = mkdtempSync("/tmp/cnpromo-");
  mkdirSync(join(root, "tmp"));
  const env = {};
  for (const [k, v] of Object.entries(process.env)) {
    if (k.startsWith("HERDR_") || k.startsWith("CROWSNEST_") || k.startsWith("ELEVENLABS_") || k === "SUPPRESS_AUTH") continue;
    env[k] = v;
  }
  Object.assign(env, {
    CROWSNEST_DATA_DIR: join(root, "data"),
    XDG_STATE_HOME: join(root, "state"),
    TMPDIR: join(root, "tmp"),
  });
  if (fake) Object.assign(env, { ELEVENLABS_API_KEY: fake.key, CROWSNEST_ELEVENLABS_URL: fake.url });
  const args = ["serve", "--bind", "127.0.0.1", "--port", String(port), "--demo",
    "--open", "never", "--name", name, "--protected-hosts-file", "", "--no-tailscale-detect"];
  if (!setup) args.push("--no-auth");
  const proc = spawn(BINARY, args, { env, stdio: ["ignore", "pipe", "pipe"] });
  const log = [];
  proc.stdout.on("data", (c) => log.push(c.toString()));
  proc.stderr.on("data", (c) => log.push(c.toString()));
  const baseURL = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 30000;
  for (;;) {
    try {
      const r = await fetch(`${baseURL}/api/v1/health`);
      if (r.ok) break;
    } catch {}
    if (proc.exitCode !== null || Date.now() > deadline) throw new Error(`demo did not start:\n${log.join("")}`);
    await new Promise((r) => setTimeout(r, 150));
  }
  return {
    baseURL,
    root,
    log: () => log.join(""),
    setupLink: () => [...log.join("").matchAll(/"url":"(http:\/\/[^"]+\/setup#token=[A-Za-z0-9_-]+)"/g)].at(-1)?.[1],
    agentSocket: () => [...log.join("").matchAll(/"msg":"agent API listening","socket":"([^"]+)"/g)].at(-1)?.[1],
    stop: async () => {
      fake?.stop();
      if (proc.exitCode === null) {
        const exited = new Promise((r) => proc.once("exit", r));
        proc.kill("SIGTERM");
        await Promise.race([exited, new Promise((r) => setTimeout(r, 3000))]);
        if (proc.exitCode === null) proc.kill("SIGKILL");
      }
      rmSync(root, { recursive: true, force: true });
    },
  };
}
