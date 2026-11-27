// Real Claude through the browser (`mise run e2e:claude`): a project with a lead (DESIGN.md 21).
// crowsnest runs with no orchestrator, so the lead stands on its own:
//
//   - the project is created in the UI from the scratch repository's directory;
//   - its lead is started from the project page; it stops at Claude's folder-trust prompt (a
//     folder Claude has not seen), its card names it, the prompt is answered from Needs you, and
//     the lead comes up idle (haiku);
//   - Remote Control comes up for it, and the project page offers "Open in Claude" with the
//     session's link;
//   - Chat with the lead: a prompt from the Chat box gets its reply;
//   - a job given "To the lead" from the project's board is held by lead-<slug> and finished.
import { expect, test } from "@playwright/test";
import type { Instance } from "../support/instance";
import { apiGet, cheapJobs, getJob, logSpend, makeScratch, realClaudeOnly, removeScratch, startReal, trustFolders, waitForJob, type Scratch } from "./rig";

let scratch: Scratch;
let instance: Instance;

interface ProjectView {
  id: number;
  slug: string;
  name: string;
  lead: { state: string; name: string; pane_id: string; remote_control?: string; remote_control_note?: string; remote_control_url?: string };
}

test.describe.serial("a project with a lead, with real Claude", () => {
  realClaudeOnly();

  test.beforeAll(async () => {
    scratch = makeScratch("cn-lead");
    instance = await startReal(scratch);
  });

  test.afterAll(async () => {
    await logSpend(instance, "lead");
    await instance?.stop();
    removeScratch(scratch);
  });

  test("create a project in the UI, start its lead, chat with it, Open in Claude, and a job it finishes", async ({ page }) => {
    test.setTimeout(15 * 60_000);
    await page.goto(`${instance.baseURL}/projects`);
    await page.getByRole("button", { name: "New project" }).first().click();
    const form = page.getByRole("form", { name: "New project" });
    await form.getByLabel(/Directory/).fill(scratch.repo);
    await form.getByLabel("Name", { exact: true }).fill("Orchard");
    await form.getByRole("button", { name: /Defaults for its jobs/ }).click();
    await form.getByLabel("Start the lead").selectOption("manual");
    await form.getByRole("button", { name: "Create project" }).click();
    await expect(page).toHaveURL(/\/projects\/orchard$/);
    const project = page.getByRole("article", { name: "Orchard" });
    await expect(project.getByTestId("lead-state")).toHaveText("Lead off");

    // Start the lead (haiku: the project's model is the instance's --model). The scratch
    // repository is a folder Claude has not seen, so the lead stops at the folder-trust prompt:
    // the card says whose terminal, and the person answers it from Needs you.
    await project.getByRole("button", { name: "Start lead" }).click();
    let sawPrompt = false;
    await expect
      .poll(
        async () => {
          const state = await project.getByTestId("lead-state").innerText();
          if (/needs terminal/i.test(state)) {
            sawPrompt = true;
            await expect(project).toContainText("Open lead-orchard's terminal");
            await trustFolders(page, instance);
          }
          return state;
        },
        { timeout: 3 * 60_000, intervals: [2_000] },
      )
      .toMatch(/idle/i);
    test.info().annotations.push({ type: "lead start", description: sawPrompt ? "folder-trust prompt answered from Needs you" : "no start-up prompt" });

    // Remote Control: the link is captured and offered on the project page, live.
    const link = project.getByTestId("open-in-claude-link");
    await expect(link).toBeVisible({ timeout: 2 * 60_000 });
    await expect(link).toHaveAttribute("href", /^https:\/\/claude\.ai\//);
    await expect(link).toHaveAttribute("target", "_blank");
    const view = await apiGet<ProjectView>(page, instance, "/projects/orchard");
    expect(view.lead.remote_control_note).toBe("active");
    await page.screenshot({ path: "screenshots/e2e-claude-lead-1440x900.png" });

    // Chat with the lead.
    await project.getByRole("button", { name: "Chat with lead" }).click();
    await expect(page).toHaveURL(/\/chat\?target=lead-orchard/);
    const box = page.getByRole("textbox", { name: "Message to Orchard's lead" });
    await expect(box).toBeVisible({ timeout: 15_000 });
    await box.fill("Reply with exactly one word: pong. Do not use any tools.");
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.getByRole("article", { name: "Reply" }).last()).toContainText(/pong/i, { timeout: 2 * 60_000 });

    // A job to the lead, from the project's board: held by lead-orchard, and finished by it.
    await cheapJobs(page);
    await page.goto(`${instance.baseURL}/jobs?project=orchard`);
    await expect(page.getByRole("radio", { name: "To the lead" })).toHaveAttribute("aria-checked", "true");
    await page.getByLabel("What needs doing?").fill(
      'Prove the job protocol works.\n\nDo nothing else: call crowsnest\'s done tool for this job with result_md "done-ok", verified [] and not_verified [].',
    );
    const created = page.waitForResponse((response) => response.url().endsWith("/api/v1/jobs") && response.request().method() === "POST");
    await page.getByRole("button", { name: "Give job" }).click();
    const id = ((await (await created).json()) as { data: { id: string } }).data.id;
    await expect(page.getByText("Given to Orchard's lead")).toBeVisible();
    const finished = await waitForJob(page, instance, id, (job) => ["done", "failed", "cancelled"].includes(job.state), 5 * 60_000, "the lead finishes the job");
    expect(finished.state).toBe("done");
    expect(finished.agent_name).toBe("lead-orchard");
    expect(finished.result_md).toContain("done-ok");
    await page.goto(`${instance.baseURL}/jobs?project=orchard&job=${id}`);
    await expect(page.getByRole("region", { name: "Selected job" }).getByText("Done", { exact: true }).first()).toBeVisible();
    expect((await getJob(page, instance, id)).state).toBe("done");
  });
});
