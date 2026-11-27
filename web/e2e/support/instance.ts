// Starts and stops a real crowsnest binary for the Playwright suites (`mise run e2e`, `e2e:real`,
// `e2e:claude`). Each caller gets its own instance on a random localhost port with its own
// temporary directories, so nothing touches ~/.local/share/crowsnest or another instance, and
// tests can run in parallel.
//
// herdr safety (DESIGN.md section 15): the child gets an environment with every HERDR_* variable
// removed, so a test run from inside a herdr pane cannot reach that pane's session. A real-herdr
// instance always uses a session named crowsnest-test-<random>; stopSession() stops and deletes
// only that session, and refuses any other name.
//
// A real-herdr instance also gets its own throwaway HOME (unless `realHome` is set, which only
// the real-Claude suite does: Claude's login lives in the real HOME). herdr keeps its config and
// session sockets under ~/.config/herdr, and its TUI shows every machine that config names, so
// with the real HOME the Terminal view and its screenshots would show the user's own machines.
// The fresh HOME has only a minimal shell rc and a herdr config that skips herdr's first-run
// screen. Every temporary directory lives under /tmp rather than os.tmpdir() because a unix
// socket path is limited to 104 bytes on macOS, and herdr's socket sits four directories below
// HOME, crowsnest's agent socket a few below its state directory.
//
// A --demo instance keeps all its state in a directory it creates under TMPDIR; the helper points
// TMPDIR at its own scratch directory so tests can find the demo's files (the orchestrator's
// transcript, to append messages to it) and its agent socket (to ask questions as an agent would).
import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { join, resolve } from "node:path";

/** The binary under test: built by `mise run build` before the suite runs. */
export const BINARY = resolve(import.meta.dirname, "../../../bin/crowsnest");

const TEST_SESSION_PATTERN = /^crowsnest-test-[a-z0-9-]+$/;
const READY_TIMEOUT_MS = 30_000;

export interface Credentials {
  username: string;
  password: string;
}

export interface InstanceOptions {
  /** The in-memory fake herdr and the scripted orchestrator; otherwise a real herdr session. */
  demo: boolean;
  /** A real-herdr session name (crowsnest-test-* only); a fresh one when omitted. */
  session?: string;
  /** Turn the login on with these credentials (passed through the environment, never argv). */
  auth?: Credentials;
  /**
   * Neither credentials nor --no-auth: the instance starts locked in first-run setup mode and
   * prints a setup link with a one-time token to its log (`setupLink`).
   */
  setup?: boolean;
  /** Start the orchestrator on real herdr (only the real-Claude suite does). Default off. */
  orchestrator?: boolean;
  /** Use the caller's real HOME (real Claude needs its login). Default: a throwaway HOME. */
  realHome?: boolean;
  /** Extra environment for the child (e.g. the fake ElevenLabs URL and key). */
  env?: Record<string, string>;
  /** Extra arguments after `serve` (e.g. --model haiku). */
  args?: string[];
}

export interface Instance {
  baseURL: string;
  port: number;
  session: string;
  /** The environment for herdr commands aimed at this instance's session (its HOME). */
  env: NodeJS.ProcessEnv;
  /** The scratch directory everything of this instance lives under. */
  root: string;
  /** Everything the process has logged so far (stdout and stderr). */
  log: () => string;
  /** The agent API's unix socket, read from the start-up log. */
  agentSocket: () => string;
  /** The newest first-run setup link (with its #token=) from the log; a `setup` instance only. */
  setupLink: () => string;
  /** Kills the process (SIGKILL: the server dies without a goodbye) and leaves the port free. */
  kill: () => Promise<void>;
  /** Starts the process again on the same port. A demo instance starts from fresh seeds. */
  start: () => Promise<void>;
  /** Stops the process and removes every temporary directory (and a real-herdr session). */
  stop: () => Promise<void>;
}

/** A throwaway HOME for a real-herdr instance (see the file comment). */
function throwawayHome(root: string): string {
  const home = join(root, "home");
  mkdirSync(join(home, ".config", "herdr"), { recursive: true });
  writeFileSync(join(home, ".zshrc"), "PS1='%1~ %# '\n");
  writeFileSync(join(home, ".bashrc"), "PS1='\\W \\$ '\n");
  // Without this herdr's TUI opens on its first-run settings screen instead of the session.
  writeFileSync(join(home, ".config", "herdr", "config.toml"), "onboarding = false\n");
  return home;
}

/** The environment for a child: ours minus every HERDR_* variable (and crowsnest's own secrets). */
export function scrubbedEnv(extra: Record<string, string> = {}): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (key.startsWith("HERDR_") || key.startsWith("CROWSNEST_") || key === "SUPPRESS_AUTH" || key.startsWith("ELEVENLABS_")) continue;
    env[key] = value;
  }
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
 * Starts crowsnest on 127.0.0.1 and waits for /api/v1/health. `demo` uses the in-memory fake
 * herdr; otherwise it drives a real herdr server for `session` (started headless by crowsnest),
 * with no orchestrator unless asked for.
 */
export async function startInstance(options: InstanceOptions): Promise<Instance> {
  if (!existsSync(BINARY)) throw new Error(`${BINARY} is missing: run mise run build first`);
  const port = await freePort();
  const root = mkdtempSync("/tmp/cne-");
  const session = options.session ?? (options.demo ? "crowsnest-demo" : testSessionName());
  if (!options.demo && !TEST_SESSION_PATTERN.test(session)) throw new Error(`refusing to use session ${session}`);

  const args = ["serve", "--bind", "127.0.0.1", "--port", String(port), "--session", session];
  if (!options.auth && !options.setup) args.push("--no-auth");
  if (options.demo) args.push("--demo");
  else if (!options.orchestrator) args.push("--no-orchestrator");
  args.push(...(options.args ?? []));

  const home = options.demo || options.realHome ? undefined : throwawayHome(root);
  const tmp = join(root, "tmp");
  mkdirSync(tmp);
  const env = scrubbedEnv(home ? { HOME: home, XDG_CONFIG_HOME: join(home, ".config") } : {});
  const childEnv: NodeJS.ProcessEnv = {
    ...env,
    ...options.env,
    // State (logs, the orchestrator's files) goes to the scratch directory too.
    CROWSNEST_DATA_DIR: join(root, "data"),
    XDG_STATE_HOME: join(root, "state"),
    TMPDIR: tmp,
  };
  if (options.auth) {
    childEnv.CROWSNEST_USERNAME = options.auth.username;
    childEnv.CROWSNEST_PASSWORD = options.auth.password;
  }

  let child: ChildProcess | null = null;
  const log: string[] = [];
  const baseURL = `http://127.0.0.1:${port}`;

  const start = async () => {
    const proc = spawn(BINARY, args, { env: childEnv, stdio: ["ignore", "pipe", "pipe"] });
    child = proc;
    proc.stdout?.on("data", (chunk: Buffer) => log.push(chunk.toString()));
    proc.stderr?.on("data", (chunk: Buffer) => log.push(chunk.toString()));
    try {
      await waitForHealth(baseURL, proc);
      // Health answers as soon as the port is bound, which is now before the herdr session is
      // connected (the server binds early so a port in use is reported at once). A real-herdr spec
      // that runs `herdr workspace create` straight away then found "no herdr server is running".
      // The start-up log says when the session is there.
      // An instance with no login starts in setup mode and connects herdr only once setup is done,
      // so that milestone counts as ready too.
      if (!options.demo) await waitForLog(log, proc, ["connected to herdr", "LOCKED in setup mode"]);
    } catch (error) {
      proc.kill("SIGKILL");
      throw new Error(`${(error as Error).message}\n--- crowsnest log ---\n${log.join("")}`, { cause: error });
    }
  };
  const kill = async () => {
    if (child) await stopChild(child, "SIGKILL");
    child = null;
  };

  await start();
  return {
    baseURL,
    port,
    session,
    env,
    root,
    log: () => log.join(""),
    agentSocket: () => {
      const matches = [...log.join("").matchAll(/"msg":"agent API listening","socket":"([^"]+)"/g)];
      const last = matches.at(-1)?.[1];
      if (!last) throw new Error("the agent socket is not in the log yet");
      return last;
    },
    setupLink: () => {
      const matches = [...log.join("").matchAll(/"url":"(http:\/\/[^"]+\/setup#token=[A-Za-z0-9_-]+)"/g)];
      const last = matches.at(-1)?.[1];
      if (!last) throw new Error("no setup link in the log");
      return last;
    },
    kill,
    start,
    stop: async () => {
      if (child) await stopChild(child, "SIGTERM");
      child = null;
      if (!options.demo) stopSession(session, env);
      rmSync(root, { recursive: true, force: true });
    },
  };
}

/** The directory a --demo instance keeps its state in (under the helper's TMPDIR). */
export function demoDir(instance: Instance): string {
  const tmp = join(instance.root, "tmp");
  const dirs = readdirSync(tmp).filter((name) => name.startsWith("crowsnest-demo-"));
  if (dirs.length === 0) throw new Error("no demo state directory yet");
  // After a restart there are two (the killed one is never cleaned up); the newest wins.
  return join(tmp, dirs.sort().at(-1) as string);
}

/** Waits until the instance's own log contains one of `needles` (its start-up milestones are logged). */
async function waitForLog(log: string[], child: ChildProcess, needles: string[]): Promise<void> {
  const deadline = Date.now() + READY_TIMEOUT_MS;
  while (Date.now() < deadline) {
    const text = log.join("");
    if (needles.some((needle) => text.includes(needle))) return;
    if (child.exitCode !== null) throw new Error(`crowsnest exited with ${child.exitCode} before logging any of ${JSON.stringify(needles)}`);
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  throw new Error(`crowsnest logged none of ${JSON.stringify(needles)} within ${READY_TIMEOUT_MS} ms`);
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
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  throw new Error(`crowsnest did not answer ${baseURL}/api/v1/health within ${READY_TIMEOUT_MS} ms`);
}

async function stopChild(child: ChildProcess, signal: NodeJS.Signals): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = new Promise<void>((resolveExit) => child.once("exit", () => resolveExit()));
  child.kill(signal);
  const timer = setTimeout(() => child.kill("SIGKILL"), 5_000);
  await exited;
  clearTimeout(timer);
}

/** Stops and deletes a throwaway herdr session. Refuses any name that is not crowsnest-test-*. */
export function stopSession(session: string, env: NodeJS.ProcessEnv): void {
  if (!TEST_SESSION_PATTERN.test(session)) throw new Error(`refusing to stop session ${session}`);
  spawnSync("herdr", ["session", "stop", session, "--json"], { env, stdio: "ignore", timeout: 15_000 });
  spawnSync("herdr", ["session", "delete", session, "--json"], { env, stdio: "ignore", timeout: 15_000 });
}
