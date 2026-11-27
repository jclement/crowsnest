// Real Claude through the browser (`mise run e2e:claude`), on a phone (390x844): folder trust never
// dead-ends (DESIGN.md 8.2.1). Both projects are brand-new git repositories Claude has never seen
// (scratch repositories nested in this worktree are their own folders to Claude's trust):
//
//   - created in the UI with "Agents may work in this folder without asking" left ticked: a
//     builder job (in its own worktree, which inherits its checkout's trust) reaches "running"
//     with no trust question at any point and no terminal;
//   - created with the box unticked: the job's agent stops at the trust question, which reaches
//     the decision screen named for the agent and the folder, with Trust this folder and Don't
//     trust; Trust is pressed there and the job reaches "running", still with no terminal.
import { expect, test, type Page } from "@playwright/test";
import type { Instance } from "../support/instance";
import { apiGet, apiSend, attention, getJob, logSpend, makeScratch, MODEL, realClaudeOnly, removeScratch, startReal, type JobView, type Scratch } from "./rig";

let first: Scratch;
let second: Scratch;
let instance: Instance;

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

interface ProjectView {
  id: number;
  slug: string;
  folder_trusted?: boolean;
}

async function createProject(page: Page, dir: string, name: string, trust: boolean): Promise<ProjectView> {
  await page.goto(`${instance.baseURL}/projects`);
  await page.getByRole("button", { name: "New project" }).first().click();
  const form = page.getByRole("form", { name: "New project" });
  await form.getByLabel(/Directory/).fill(dir);
  await form.getByLabel("Name", { exact: true }).fill(name);
  const box = form.getByRole("checkbox", { name: /Agents may work in this folder without asking/ });
  await expect(box).toBeChecked(); // on by default
  if (!trust) await box.uncheck();
  await page.screenshot({ path: `screenshots/trust-project-form-${trust ? "ticked" : "unticked"}-390.png` });
  await form.getByRole("button", { name: "Create project" }).click();
  const slug = name.toLowerCase();
  await expect(page).toHaveURL(new RegExp(`/projects/${slug}$`));
  return apiGet<ProjectView>(page, instance, `/projects/${slug}`);
}

async function giveJob(page: Page, project: ProjectView, dir: string, word: string): Promise<string> {
  const created = await apiSend<{ id: string }>(page, instance, "POST", "/jobs", {
    brief: `Create a file named ${word}.txt containing the single word ${word}, then call your crowsnest done tool with a one-line result.`,
    dir,
    project_id: project.id,
    role: "builder",
    model: MODEL,
  });
  expect(created.status, "creating the job").toBe(201);
  return created.data.id;
}

/** Polls the job until it is running (or further), recording whether a trust dialog appeared for it. */
async function untilRunning(page: Page, id: string, onDialog?: (itemId: string) => Promise<void>): Promise<{ job: JobView; sawDialog: boolean }> {
  let sawDialog = false;
  let job: JobView | undefined;
  await expect
    .poll(
      async () => {
        job = await getJob(page, instance, id);
        const dialog = (await attention(page, instance)).find((item) => item.kind === "dialog" && item.job_id === id);
        if (dialog) {
          sawDialog = true;
          if (onDialog) await onDialog(dialog.id);
        }
        return ["running", "in_review", "done"].includes(job.state) || job.timeline.some((event) => event.kind === "launch" && /Brief sent/.test(event.summary));
      },
      { timeout: 4 * 60_000, intervals: [2_000], message: `job ${id} reaching running` },
    )
    .toBe(true);
  return { job: job as JobView, sawDialog };
}

test.describe.serial("folder trust on a phone, with real Claude", () => {
  realClaudeOnly();

  test.beforeAll(async () => {
    first = makeScratch("cn-trust-a");
    second = makeScratch("cn-trust-b");
    instance = await startReal(first);
  });

  test.afterAll(async () => {
    await logSpend(instance, "trust");
    // Stop the agents before their worktrees go.
    for (const job of await fetch(`${instance.baseURL}/api/v1/jobs`).then((r) => r.json()).then((b: { data: JobView[] }) => b.data).catch(() => [] as JobView[])) {
      await fetch(`${instance.baseURL}/api/v1/jobs/${job.id}/cancel`, { method: "POST", headers: { Origin: instance.baseURL, "X-Crowsnest": "1" } }).catch(() => undefined);
    }
    await instance?.stop();
    removeScratch(first);
    removeScratch(second);
  });

  test("a project created with its box ticked: its job's worktree starts trusted, no prompt, no terminal", async ({ page }) => {
    test.setTimeout(8 * 60_000);
    const project = await createProject(page, first.repo, "Trusted", true);
    expect(project.folder_trusted, "the project's folder should be trusted").toBe(true);
    const id = await giveJob(page, project, first.repo, "alpha");
    const { job, sawDialog } = await untilRunning(page, id);
    test.info().annotations.push({ type: "worktree", description: job.work_dir });
    expect(job.work_dir, "a builder job runs in its own worktree").not.toBe(first.repo);
    expect(sawDialog, "no trust question may appear for a job in a trusted project").toBe(false);
    expect(job.timeline.some((event) => event.kind === "stuck" && /dialog/i.test(event.summary)), "the job never stopped at a dialog").toBe(false);
    await page.goto(`${instance.baseURL}/jobs?job=${id}`);
    await page.screenshot({ path: "screenshots/trust-job-running-390.png" });
  });

  test("a project created with its box unticked: the trust card on the decision screen, Trust pressed, the job runs", async ({ page }) => {
    test.setTimeout(8 * 60_000);
    const project = await createProject(page, second.repo, "Asking", false);
    expect(project.folder_trusted).toBe(false);
    const id = await giveJob(page, project, second.repo, "beta");
    let answered = false;
    const { sawDialog } = await untilRunning(page, id, async (itemId) => {
      if (answered) return;
      await page.goto(`${instance.baseURL}/decide?item=${encodeURIComponent(itemId)}`);
      await expect(page.getByRole("heading", { name: /asks to trust/ }).or(page.getByText(/asks to trust/).first())).toBeVisible({ timeout: 15_000 });
      const bar = page.getByTestId("answer-bar");
      await expect(bar.getByRole("button", { name: "Don't trust" })).toBeVisible();
      await page.screenshot({ path: "screenshots/trust-decision-390.png" });
      await bar.getByRole("button", { name: /^Trust this folder$/ }).click();
      answered = true;
      await expect(page.getByText(/may work in/).first()).toBeVisible({ timeout: 15_000 });
    });
    expect(sawDialog, "the unticked project's job should meet the trust question").toBe(true);
    expect(answered).toBe(true);
  });
});
