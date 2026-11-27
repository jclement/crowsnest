// The shared rig of the real-Claude browser suite (`mise run e2e:claude`): a scratch git repository
// inside this worktree (which Claude already trusts), a real crowsnest on a throwaway
// crowsnest-test-* herdr session with real Claude on haiku, and the human's part a test plays at
// the moments crowsnest rightly leaves to a person (Claude's folder-trust question in a fresh
// worktree), played through the browser: the dialog's decision packet, as Jeff would on his phone.
//
// Safety, as in support/instance.ts: every HERDR_* variable is removed from every child's
// environment; the session is crowsnest-test-* and is stopped and deleted by instance.stop().
// Claude's login lives in the real HOME, so the real HOME is used (`realHome`): job worktrees go
// where herdr puts them (~/.herdr/worktrees/<repo>/…), and `removeWorktrees` removes the scratch
// repository's leftovers at the end (only that repository's, found through git itself).
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { scrubbedEnv, startInstance, type Instance } from "../support/instance";

export const MODEL = process.env.CROWSNEST_E2E_MODEL ?? "haiku";

/** Skips the file unless the suite was asked for and its tools are here. */
export function realClaudeOnly(tools = ["herdr", "claude", "git"]) {
  const missing = tools.filter((tool) => spawnSync("sh", ["-c", `command -v ${tool}`], { env: scrubbedEnv() }).status !== 0);
  test.skip(process.env.CROWSNEST_E2E_CLAUDE !== "1", "real-Claude suite: set CROWSNEST_E2E_CLAUDE=1 (mise run e2e:claude); it spends real tokens (haiku)");
  test.skip(missing.length > 0, `not installed: ${missing.join(", ")}`);
}

const GIT_ID = ["-c", "user.email=e2e@example.invalid", "-c", "user.name=e2e"];

export function git(repo: string, ...args: string[]): string {
  return execFileSync("git", [...GIT_ID, "-C", repo, ...args], { encoding: "utf8", env: scrubbedEnv() }).trim();
}

export interface Scratch {
  /** The scratch directory (removed at the end). */
  dir: string;
  /** A git repository on main with one commit. */
  repo: string;
}

/**
 * A scratch directory inside this worktree with a git repository named `name` (unique per run, so
 * herdr's worktrees of it never collide with another run's). `files` are committed on main.
 */
export function makeScratch(name: string, files: Record<string, string> = {}): Scratch {
  const dir = mkdtempSync(join(import.meta.dirname, ".scratch-"));
  const repo = join(dir, `${name}-${Math.random().toString(36).slice(2, 7)}`);
  mkdirSync(repo);
  execFileSync("git", ["init", "-q", "-b", "main"], { cwd: repo });
  writeFileSync(join(repo, "README.md"), "# e2e scratch repo\n");
  for (const [path, body] of Object.entries(files)) writeFileSync(join(repo, path), body);
  git(repo, "add", ".");
  git(repo, "commit", "-q", "-m", "init");
  return { dir, repo };
}

/** Removes every worktree of `repo` but its main one (herdr made them for jobs), then the scratch. */
export function removeScratch(scratch: Scratch | undefined) {
  if (!scratch) return;
  try {
    const list = git(scratch.repo, "worktree", "list", "--porcelain");
    const paths = list
      .split("\n")
      .filter((line) => line.startsWith("worktree "))
      .map((line) => line.slice("worktree ".length))
      .filter((path) => path !== scratch.repo);
    for (const path of paths) {
      spawnSync("git", ["-C", scratch.repo, "worktree", "remove", "--force", path], { env: scrubbedEnv() });
      rmSync(path, { recursive: true, force: true });
    }
  } catch {
    // the repository is already gone
  }
  rmSync(scratch.dir, { recursive: true, force: true });
  // herdr keeps a repository's worktrees under ~/.herdr/worktrees/<repo name>/ and leaves that
  // directory behind, empty, when the last one goes: remove it, only if empty (the name is this
  // run's own).
  try {
    rmdirSync(join(homedir(), ".herdr", "worktrees", basename(scratch.repo)));
  } catch {
    // not there, or not empty
  }
}

export interface RealOptions {
  orchestrator?: boolean;
  /** The review gate (--review); the suite never leaves it on auto (Codex would review). */
  review?: "off" | "claude" | "same";
  /** Claude's permission mode for job agents (and the orchestrator). */
  permissionMode?: "auto" | "manual";
  args?: string[];
}

/**
 * The Codex home every real instance is given: a folder in the scratch directory, so a project
 * created in the UI with its trust box ticked records Codex's trust there and never in the real
 * ~/.codex/config.toml (DESIGN.md 8.2.1). Nothing in this suite runs Codex. Claude's trust is
 * recorded in the real ~/.claude.json, as answering the prompt would, for scratch folders only.
 */
export function scratchCodexHome(scratch: Scratch): string {
  const dir = join(scratch.dir, "codex-home");
  mkdirSync(dir, { recursive: true });
  return dir;
}

/** A real crowsnest for one spec file: haiku, no pokes, an explicit review gate. */
export async function startReal(scratch: Scratch, options: RealOptions = {}): Promise<Instance> {
  const args = ["--model", MODEL, "--poke-interval", "0", "--review", options.review ?? "off", "--name", "e2e"];
  if (options.permissionMode) args.push("--permission-mode", options.permissionMode);
  if (options.orchestrator) {
    const orchestratorDir = join(scratch.dir, "orchestrator");
    mkdirSync(orchestratorDir, { recursive: true });
    args.push("--dir", orchestratorDir);
  }
  return startInstance({ demo: false, orchestrator: options.orchestrator ?? false, realHome: true, env: { CODEX_HOME: scratchCodexHome(scratch) }, args: [...args, ...(options.args ?? [])] });
}

/**
 * Prints what the instance measured its agents spending (an API-equivalent estimate from the
 * transcripts), so a run's cost is in its log. Called before the instance stops.
 */
export async function logSpend(instance: Instance | undefined, label: string) {
  if (!instance) return;
  try {
    const response = await fetch(`${instance.baseURL}/api/v1/spend/summary`);
    const data = ((await response.json()) as { data: { today: { cost_usd: number | null; turns: number }; orchestrator_today: { cost_usd: number | null } } }).data;
    const usd = (value: number | null | undefined) => (value === null || value === undefined ? "unknown" : `$${value.toFixed(2)}`);
    console.log(`spend ${label}: ${usd(data.today.cost_usd)} today in ${data.today.turns} turns (orchestrator ${usd(data.orchestrator_today.cost_usd)})`);
  } catch (error) {
    console.log(`spend ${label}: not read (${(error as Error).message})`);
  }
}

/** The headers a browser's state-changing request carries. */
export function writeHeaders(instance: Instance) {
  return { Origin: instance.baseURL, "X-Crowsnest": "1", "Content-Type": "application/json" };
}

export async function apiGet<T>(page: Page, instance: Instance, path: string): Promise<T> {
  const response = await page.request.get(`${instance.baseURL}/api/v1${path}`);
  expect(response.ok(), `GET ${path}: ${response.status()}`).toBe(true);
  return ((await response.json()) as { data: T }).data;
}

export async function apiSend<T>(page: Page, instance: Instance, method: "POST" | "PUT", path: string, data?: unknown): Promise<{ status: number; data: T }> {
  const response = await page.request.fetch(`${instance.baseURL}/api/v1${path}`, { method, headers: writeHeaders(instance), data: data ?? {} });
  const body = (await response.json().catch(() => ({}))) as { data: T };
  return { status: response.status(), data: body.data };
}

export interface JobView {
  id: string;
  title: string;
  state: string;
  cause: string;
  cause_detail: string;
  pane_id: string;
  agent_name: string;
  work_dir: string;
  worktree_branch: string;
  result_md: string;
  accept_state: string;
  check_rounds: number;
  land_state: string;
  review_of: string;
  review_round: number;
  nudges: number;
  evidence?: { status: string; commit: string } | null;
  timeline: Array<{ kind: string; summary: string; detail?: Record<string, unknown> }>;
  questions: Array<{ id: string; status: string; receipt?: string }>;
}

export function getJob(page: Page, instance: Instance, id: string): Promise<JobView> {
  return apiGet<JobView>(page, instance, `/jobs/${id}`);
}

export async function listJobs(page: Page, instance: Instance): Promise<JobView[]> {
  return apiGet<JobView[]>(page, instance, "/jobs");
}

/** Makes every POST /jobs from the page ask for the cheap model (the form has no model field). */
export async function cheapJobs(page: Page) {
  await page.route("**/api/v1/jobs", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    const body = { ...(route.request().postDataJSON() as Record<string, unknown>), model: MODEL };
    await route.continue({ postData: JSON.stringify(body) });
  });
}

interface AttentionItem {
  id: string;
  kind: string;
  title: string;
  job_id?: string;
  pane_id?: string;
}

export async function attention(page: Page, instance: Instance): Promise<AttentionItem[]> {
  return (await apiGet<{ items: AttentionItem[] }>(page, instance, "/attention")).items;
}

/**
 * The human's part at a terminal dialog, through the browser: Claude's folder-trust question in a
 * fresh worktree (an arrow-key menu with "No, exit" preselected, which crowsnest never answers by
 * itself) is answered from its decision packet with "Trust this folder", as a person would
 * on the phone. Any other dialog is left alone (a journey that expects one answers it itself).
 * Returns the number answered. The page is left wherever it was.
 */
export async function trustFolders(page: Page, instance: Instance, jobIds?: string[]): Promise<number> {
  const items = (await attention(page, instance)).filter((item) => item.kind === "dialog" && (!jobIds || (item.job_id && jobIds.includes(item.job_id))));
  if (items.length === 0) return 0;
  const back = page.url();
  let answered = 0;
  for (const item of items) {
    await page.goto(`${instance.baseURL}/decide?item=${encodeURIComponent(item.id)}`);
    const trust = page.getByTestId("answer-bar").getByRole("button", { name: /^Trust this folder$/ });
    if (!(await trust.isVisible({ timeout: 5_000 }).catch(() => false))) {
      test.info().annotations.push({ type: "dialog left alone", description: `${item.id}: ${item.title}` });
      continue;
    }
    await trust.click();
    answered++;
    test.info().annotations.push({ type: "trusted a folder", description: item.id });
  }
  if (back.startsWith("http")) await page.goto(back);
  return answered;
}

/**
 * Polls `check` until it returns a value (not undefined), answering any folder-trust question on
 * the way (a job's own, or one of a job it started: a reviewer in a new worktree). On a timeout
 * it explains `jobId`.
 */
export async function until<T>(page: Page, instance: Instance, jobId: string, check: () => Promise<T | undefined>, timeout: number, what: string): Promise<T> {
  let found: T | undefined;
  await expect
    .poll(
      async () => {
        found = await check();
        if (found !== undefined) return true;
        await trustFolders(page, instance);
        return false;
      },
      { timeout, intervals: [3_000], message: what },
    )
    .toBe(true)
    .catch(async (error: unknown) => {
      await explainJob(page, instance, jobId);
      throw error;
    });
  return found as T;
}

/** Polls a job until `done` holds, answering any folder-trust question on the way. */
export async function waitForJob(page: Page, instance: Instance, id: string, done: (job: JobView) => boolean, timeout: number, what: string): Promise<JobView> {
  return until(
    page,
    instance,
    id,
    async () => {
      const job = await getJob(page, instance, id);
      return done(job) ? job : undefined;
    },
    timeout,
    what,
  );
}

/** On a failure, what a job was doing: its record, its timeline and its pane's screen. */
export async function explainJob(page: Page, instance: Instance, id: string) {
  const job = await getJob(page, instance, id).catch(() => undefined);
  let screen = "(no pane)";
  if (job?.pane_id) {
    const view = await page.request.get(`${instance.baseURL}/api/v1/agents/${encodeURIComponent(job.pane_id)}/screen`).catch(() => undefined);
    screen = view ? (((await view.json()) as { data?: { text?: string } }).data?.text ?? "") : "(screen unavailable)";
  }
  const timeline = (job?.timeline ?? []).map((event) => `${event.kind}: ${event.summary}`).join("\n");
  const body = `--- ${id}: ${job?.state}/${job?.cause} ${job?.cause_detail ?? ""} accept=${job?.accept_state}\n--- timeline\n${timeline}\n--- screen\n${screen}`;
  await test.info().attach(`${id}.txt`, { body, contentType: "text/plain" });
  console.log(body.slice(-5000));
}
