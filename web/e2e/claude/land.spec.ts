// Real Claude through the browser (`mise run e2e:claude`): a coding job from give to land, and a
// job whose done fails crowsnest's own check (DESIGN.md 6.11, 6.15, 22.2).
//
//   1. A job given from Now, for a project, as a builder: watched on Now's project row and on the
//      project's board; the agent commits and calls done; crowsnest's check (`mise run check`, the
//      scratch repository's own task) runs; a Claude reviewer (haiku) reviews it; the job is
//      Accepted; the Review tab shows the diff; Merge, with a confirmation naming the commit; the
//      base branch has the commit and the worktree is gone.
//   2. A job whose done fails the check (answer.txt must be 42; the brief says 41): the failed
//      check and its output show on the job, the output goes back to the agent, it fixes it, and
//      the job ends Accepted with the evidence pinned to its commit.
//
// The scratch repository is a project with a `check` task; its jobs run in herdr worktrees of it.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import type { Instance } from "../support/instance";
import { cheapJobs, getJob, git, listJobs, makeScratch, realClaudeOnly, removeScratch, startReal, waitForJob, type Scratch } from "./rig";

// The check: answer.txt (when there is one) must hold exactly 42; land.txt (when there is one) the
// word landed. Its last line says why it failed, as a real project's check would.
const CHECK = `#!/bin/sh
if [ -f answer.txt ]; then
  v="$(cat answer.txt)"
  [ "$v" = 42 ] || { echo "FAIL: answer.txt must contain exactly 42, but it contains: $v"; exit 1; }
fi
if [ -f land.txt ]; then
  grep -qx landed land.txt || { echo "FAIL: land.txt must contain exactly the word landed"; exit 1; }
fi
echo "check ok"
`;

let scratch: Scratch;
let instance: Instance;
let project: { id: number; slug: string; name: string };

test.describe.serial("a coding job from Now to landed", () => {
  realClaudeOnly(["herdr", "claude", "git", "mise"]);

  test.beforeAll(async ({ request }) => {
    scratch = makeScratch("cn-land", { "check.sh": CHECK, "mise.toml": '[tasks.check]\nrun = "sh ./check.sh"\n' });
    instance = await startReal(scratch, { review: "claude" });
    const response = await request.post(`${instance.baseURL}/api/v1/projects`, {
      headers: { Origin: instance.baseURL, "X-Crowsnest": "1" },
      data: { name: "Landing", dir: scratch.repo, lead_start: "manual" },
    });
    expect(response.status()).toBe(201);
    project = ((await response.json()) as { data: typeof project }).data;
  });

  test.afterAll(async () => {
    await instance?.stop();
    removeScratch(scratch);
  });

  test("give from Now for a project → checked → reviewed → accepted → Review tab → Merge → landed", async ({ page }) => {
    test.setTimeout(20 * 60_000);
    await cheapJobs(page);
    await page.goto(`${instance.baseURL}/`);
    await expect(page.getByRole("heading", { name: "Now", level: 1 })).toBeVisible();
    const give = page.getByRole("region", { name: "Give a job" });
    await give.getByLabel("Who gets the job").selectOption(project.slug);
    await give.getByRole("radio", { name: "As a job" }).click();
    await give.getByLabel("What needs doing?").fill(
      "Create a file named land.txt containing exactly the word landed. Commit it on your branch with git " +
        "(git add land.txt && git commit -m 'Add land.txt'). Then call your crowsnest done tool with a one-line result, verified listing what you checked, " +
        "not_verified as an empty list, and walkthrough_md: one short paragraph naming land.txt and why it exists. If a [crowsnest review …] or " +
        "[crowsnest check] message asks for changes, make them, commit, and call done again.",
    );
    await give.getByRole("button", { name: /^To / }).click();
    await give.getByLabel("Role").selectOption("builder");
    await give.getByLabel("Done when").fill("land.txt is committed with the word landed");
    const created = page.waitForResponse((response) => response.url().endsWith("/api/v1/jobs") && response.request().method() === "POST");
    await give.getByRole("button", { name: "Give job" }).click();
    const response = await created;
    expect(response.status()).toBe(201);
    const id = ((await response.json()) as { data: { id: string } }).data.id;
    await expect(page.getByText(`Job ${id} given`)).toBeVisible();

    // On Now: the project's row counts it running (live, no reload).
    const row = page.locator(`[data-now-row="${project.slug}"]`);
    await expect(row).toContainText("1 running", { timeout: 60_000 });

    // On the project's board: its card, in progress.
    await waitForJob(page, instance, id, (job) => job.state === "running" && !!job.pane_id, 4 * 60_000, "the builder starts");
    await page.goto(`${instance.baseURL}/jobs?project=${project.slug}`);
    await expect(page.getByRole("region", { name: "In progress" }).getByRole("button", { name: /Create a file named land\.txt/ })).toBeVisible();

    // The agent's done goes to crowsnest's check and the reviewer; it ends accepted and done.
    const finished = await waitForJob(page, instance, id, (job) => ["done", "failed", "cancelled"].includes(job.state) || (job.state === "needs_you" && job.cause !== "dialog"), 14 * 60_000, "the job is checked, reviewed and done");
    expect(finished.state, `${finished.state}/${finished.cause}: ${finished.cause_detail}`).toBe("done");
    expect(finished.accept_state).toBe("accepted");
    expect(finished.timeline.some((event) => /check/i.test(event.summary) && /pass/i.test(event.summary)), "a passing check is on the timeline").toBe(true);
    const reviewer = (await listJobs(page, instance)).find((job) => job.review_of === id);
    expect(reviewer, "a reviewer job was started").toBeDefined();
    const head = git(finished.work_dir, "rev-parse", "HEAD");
    expect(finished.evidence?.commit).toBe(head);

    // The job, as the board shows it: Accepted, ready to land.
    await page.goto(`${instance.baseURL}/jobs?project=${project.slug}&job=${id}`);
    const detail = page.getByRole("region", { name: "Selected job" });
    await expect(detail.getByRole("region", { name: "crowsnest's check" })).toContainText("Accepted");
    await detail.getByRole("button", { name: "Review and land" }).click();

    // The Review tab: one commit ahead of main, land.txt in the diff.
    await expect(detail.getByText(/1 commit ahead of main/)).toBeVisible();
    const files = detail.getByRole("list", { name: "Changed files" });
    await expect(files).toContainText("land.txt");
    await files.getByRole("button", { name: /land\.txt/ }).first().click();
    await expect(detail.getByText("landed", { exact: true }).first()).toBeVisible();
    await page.screenshot({ path: "screenshots/e2e-claude-review-1440x900.png" });

    // Merge: the confirmation names the commit that was reviewed.
    await detail.getByRole("group", { name: "Land actions" }).getByRole("button", { name: "Merge", exact: true }).click();
    const confirm = detail.getByRole("alertdialog", { name: "Confirm Merge" });
    await expect(confirm).toContainText(head.slice(0, 7));
    await expect(confirm).toContainText("into main");
    await confirm.getByRole("button", { name: "Yes, merge" }).click();
    await expect(page.getByText("Merge: done").first()).toBeVisible({ timeout: 60_000 });
    await page.waitForTimeout(3_000);
    await page.screenshot({ path: "screenshots/e2e-claude-landed-1440x900.png" });
    await expect(detail.getByText(/Merged: /), await detail.innerText()).toBeVisible({ timeout: 30_000 });

    // The base branch has the commit; the worktree is gone.
    expect(readFileSync(join(scratch.repo, "land.txt"), "utf8").trim()).toBe("landed");
    expect(git(scratch.repo, "merge-base", "--is-ancestor", head, "main") === "").toBe(true);
    await expect.poll(async () => (await getJob(page, instance, id)).land_state).toBe("merged");
    await expect.poll(() => git(scratch.repo, "worktree", "list").includes(finished.work_dir), { timeout: 30_000 }).toBe(false);
    await detail.getByRole("tab", { name: "Overview" }).click();
    await expect(detail.getByRole("list", { name: "Timeline" })).toContainText("Teardown finished");
  });

  test("a done that fails crowsnest's check gets the output back, is fixed, and ends accepted", async ({ page }) => {
    test.setTimeout(15 * 60_000);
    await cheapJobs(page);
    // This job has no reviewer (the instance's gate is on for the first journey): its subject is
    // the check, and a reviewer round would only add time.
    await page.route("**/api/v1/jobs", async (route) => {
      if (route.request().method() !== "POST") return route.fallback();
      await route.fallback({ postData: JSON.stringify({ ...(route.request().postDataJSON() as Record<string, unknown>), reviewer: "off" }) });
    });
    await page.goto(`${instance.baseURL}/jobs?project=${project.slug}`);
    await page.getByRole("radio", { name: "As a job" }).click();
    await page.getByLabel("What needs doing?").fill(
      "Create a file named answer.txt containing exactly the number 41 (and nothing else). Commit it on your branch " +
        "(git add answer.txt && git commit -m 'Add answer.txt'). Then call your crowsnest done tool with a one-line result, verified as an empty list " +
        "and not_verified as an empty list. If a [crowsnest check] message says the check failed, read the output file it names, fix what it says, " +
        "commit, and call done again: the project's check is the authority on the right value, so do what it says without asking.",
    );
    await page.getByRole("button", { name: /^To / }).click();
    await page.getByLabel("Role").selectOption("builder");
    const created = page.waitForResponse((response) => response.url().endsWith("/api/v1/jobs") && response.request().method() === "POST");
    await page.getByRole("button", { name: "Give job" }).click();
    const id = ((await (await created).json()) as { data: { id: string } }).data.id;
    await expect(page).toHaveURL(new RegExp(`job=${id}`));
    const detail = page.getByRole("region", { name: "Selected job" });

    // The first check fails: the job view says so live, with the check's own output.
    await waitForJob(page, instance, id, (job) => job.check_rounds >= 1 || ["done", "failed", "cancelled"].includes(job.state), 8 * 60_000, "the first check fails");
    await expect(detail.getByRole("list", { name: "Timeline" })).toContainText("Not accepted", { timeout: 30_000 });
    await expect(detail.getByRole("list", { name: "Timeline" })).toContainText(/check/i);

    // The agent reads it, fixes it, and the job ends accepted with a pass at its HEAD.
    const finished = await waitForJob(page, instance, id, (job) => ["done", "failed", "cancelled"].includes(job.state) || (job.state === "needs_you" && job.cause !== "dialog"), 8 * 60_000, "the fixed job is accepted");
    expect(finished.state, `${finished.state}/${finished.cause}: ${finished.cause_detail}`).toBe("done");
    expect(finished.accept_state).toBe("accepted");
    expect(readFileSync(join(finished.work_dir, "answer.txt"), "utf8").trim()).toBe("42");
    expect(finished.evidence?.commit).toBe(git(finished.work_dir, "rev-parse", "HEAD"));
    const evidence = detail.getByRole("region", { name: "crowsnest's check" });
    await expect(evidence).toContainText("Accepted", { timeout: 30_000 });
    await expect(evidence).toContainText(/passed · exit 0/);
    const card = page.getByRole("listitem").filter({ has: page.getByText(/Create a file named answer\.txt/) }).first();
    await expect(card).toContainText("Accepted");
    await page.screenshot({ path: "screenshots/e2e-claude-check-fixed-1440x900.png" });
  });
});
