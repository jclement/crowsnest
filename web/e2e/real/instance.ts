// Starts and stops a real crowsnest binary for the "real" Playwright suite (playwright.real.config.ts,
// `mise run e2e:real`). Each spec gets its own instance on a random localhost port with its own
// temporary data directory, so nothing touches ~/.local/share/crowsnest or another instance.
//
// herdr safety (DESIGN.md section 15): the child gets an environment with every HERDR_* variable
// removed, so a test run from inside a herdr pane cannot reach that pane's session. A real-herdr
// instance always uses a session named crowsnest-test-<random>; stopSession() stops and deletes
// only that session, and refuses any other name.
import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

/** The binary under test: built by `mise run build` before the suite runs. */
export const BINARY = resolve(import.meta.dirname, "../../../bin/crowsnest");

const TEST_SESSION_PATTERN = /^crowsnest-test-[a-z0-9-]+$/;
const READY_TIMEOUT_MS = 30_000;

export interface Instance {
  baseURL: string;
  session: string;
  stop: () => Promise<void>;
}

/** The environment for a child: ours minus every HERDR_* variable. */
export function scrubbedEnv(extra: Record<string, string> = {}): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {};
  for (const [key, value] of Object.entries(process.env)) if (!key.startsWith("HERDR_")) env[key] = value;
  return { ...env, ...extra };
}

/** A free localhost port (bind to 0, read it, release it). */
export async function freePort(): Promise<number> {
  return new Promise((resolvePort, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      server.close(() => resolvePort(port));
    });
  });
}

/** A fresh throwaway session name. */
export function testSessionName(): string {
  return `crowsnest-test-${Math.random().toString(36).slice(2, 10)}`;
}

/**
 * Starts crowsnest with --no-auth on 127.0.0.1 and waits for /api/v1/health. `demo` uses the
 * in-memory fake herdr; otherwise it drives a real herdr server for `session` (started headless
 * by crowsnest) with no orchestrator.
 */
export async function startInstance(options: { demo: boolean; session?: string }): Promise<Instance> {
  const port = await freePort();
  const dataDir = mkdtempSync(join(tmpdir(), "crowsnest-e2e-"));
  const session = options.session ?? (options.demo ? "crowsnest-demo" : testSessionName());
  if (!options.demo && !TEST_SESSION_PATTERN.test(session)) throw new Error(`refusing to use session ${session}`);
  const args = ["serve", "--no-auth", "--bind", "127.0.0.1", "--port", String(port), "--session", session, "--no-orchestrator"];
  if (options.demo) args.push("--demo");
  const child = spawn(BINARY, args, {
    env: scrubbedEnv({ CROWSNEST_DATA_DIR: dataDir }),
    stdio: ["ignore", "pipe", "pipe"],
  });
  const log: string[] = [];
  child.stdout?.on("data", (chunk: Buffer) => log.push(chunk.toString()));
  child.stderr?.on("data", (chunk: Buffer) => log.push(chunk.toString()));
  const baseURL = `http://127.0.0.1:${port}`;
  try {
    await waitForHealth(baseURL, child);
  } catch (error) {
    child.kill("SIGTERM");
    throw new Error(`${(error as Error).message}\n--- crowsnest log ---\n${log.join("")}`);
  }
  return {
    baseURL,
    session,
    stop: async () => {
      await stopChild(child);
      if (!options.demo) stopSession(session);
      rmSync(dataDir, { recursive: true, force: true });
    },
  };
}

async function waitForHealth(baseURL: string, child: ChildProcess): Promise<void> {
  const deadline = Date.now() + READY_TIMEOUT_MS;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`crowsnest exited with ${child.exitCode} before it was ready`);
    try {
      const response = await fetch(`${baseURL}/api/v1/health`);
      if (response.ok) return;
    } catch {
      // not listening yet
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 200));
  }
  throw new Error(`crowsnest did not answer ${baseURL}/api/v1/health within ${READY_TIMEOUT_MS} ms`);
}

async function stopChild(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null) return;
  const exited = new Promise<void>((resolveExit) => child.once("exit", () => resolveExit()));
  child.kill("SIGTERM");
  const timer = setTimeout(() => child.kill("SIGKILL"), 5_000);
  await exited;
  clearTimeout(timer);
}

/** Stops and deletes a throwaway herdr session. Refuses any name that is not crowsnest-test-*. */
export function stopSession(session: string): void {
  if (!TEST_SESSION_PATTERN.test(session)) throw new Error(`refusing to stop session ${session}`);
  const env = scrubbedEnv();
  spawnSync("herdr", ["session", "stop", session, "--json"], { env, stdio: "ignore", timeout: 15_000 });
  spawnSync("herdr", ["session", "delete", session, "--json"], { env, stdio: "ignore", timeout: 15_000 });
}
