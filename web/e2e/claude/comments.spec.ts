// Real Claude through the browser (`mise run e2e:claude`): a line comment on the diff reaches the
// agent and is acted on (DESIGN.md 6.21). A haiku builder adds a function to calc.go in a worktree
// and finishes. On its diff the human selects the line declaring the existing function, comments
// "rename this function to sumOf", and sends it. The job is finished, so the send starts a fix job
// in its worktree; the fix job (haiku) renames it and commits, and the open diff shows sumOf, with
// the comment now outdated.
import { expect, test } from "@playwright/test";
import type { Instance } from "../support/instance";
import { listJobs, logSpend, makeScratch, MODEL, realClaudeOnly, removeScratch, startReal, waitForJob, writeHeaders, type Scratch } from "./rig";

let scratch: Scratch;
let instance: Instance;

test.describe.serial("a line comment reaches a real agent", () => {
  realClaudeOnly(["herdr", "claude", "git"]);

  test.beforeAll(async () => {
    scratch = makeScratch("cn-comments", { "calc.go": "package calc\n\n// addNumbers returns the sum of a and b.\nfunc addNumbers(a, b int) int {\n\treturn a + b\n}\n" });
    instance = await startReal(scratch, { review: "off" });
  });

  test.afterAll(async () => {
    await logSpend(instance, "comments");
    await instance?.stop();
    removeScratch(scratch);
  });

  test("comment 'rename this function' on a line, send, the agent renames it, the diff shows it", async ({ page, context, request }) => {
    test.setTimeout(12 * 60_000);
    const created = await request.post(`${instance.baseURL}/api/v1/jobs`, {
      headers: writeHeaders(instance),
      data: {
        brief:
          "In calc.go, add a function `Twice(x int) int` that returns x*2, below addNumbers, with a one-line doc comment. Change nothing else. " +
          "Commit it (git add calc.go && git commit -m 'calc: Twice'). " +
          "Then call your crowsnest done tool with a one-line result, verified as an empty list and not_verified as an empty list.",
        dir: scratch.repo,
        role: "builder",
        isolate: true,
        model: MODEL,
      },
    });
    expect(created.status()).toBe(201);
    const id = ((await created.json()) as { data: { id: string } }).data.id;
    const helper = await context.newPage();
    await waitForJob(helper, instance, id, (job) => job.state === "done", 6 * 60_000, "the builder finishes");

    // The diff of what it committed: select the line declaring addNumbers, comment, send.
    await page.goto(`${instance.baseURL}/diff?job=${id}&scope=committed`);
    const viewer = page.getByRole("region", { name: /^Changes: / });
    const table = viewer.getByRole("table", { name: "Diff of calc.go" });
    await expect(table).toContainText("Twice");
    await table.getByRole("row").filter({ hasText: "func addNumbers(" }).getByRole("button", { name: /new side/ }).first().click();
    await page.keyboard.press("c");
    const editor = viewer.getByRole("form", { name: /^Comment on line \d+ of calc\.go$/ });
    await editor.getByRole("textbox", { name: "Comment" }).fill("Rename this function to sumOf (and its doc comment). Keep everything else as it is.");
    await editor.getByRole("button", { name: "Save comment" }).click();
    await viewer.getByRole("button", { name: "Comments: 1 to send" }).click();
    const sheet = page.getByRole("dialog", { name: /^Comments · / });
    await sheet.getByRole("button", { name: "Send 1 comment to the agent" }).click();
    await expect(page.getByText(/a fix job in this worktree/)).toBeVisible();
    await page.keyboard.press("Escape");

    // The fix job makes the change; the open diff follows it.
    const fixes = (await listJobs(helper, instance)).filter((job) => (job as unknown as { fix_of?: string }).fix_of === id);
    expect(fixes).toHaveLength(1);
    const fix = fixes[0]!;
    await waitForJob(helper, instance, fix.id, (job) => job.state === "done", 6 * 60_000, "the fix job finishes");
    await expect(table).toContainText("func sumOf(", { timeout: 60_000 });
    // Against the base, the old name is now a removed line, the new one an added line.
    await expect(table.getByRole("row").filter({ hasText: "func addNumbers(" }).first()).toContainText("removed");
    await expect(table.getByRole("row").filter({ hasText: "func sumOf(" }).first()).toContainText("added");
    await expect(viewer.locator("[data-comment-status=outdated]").filter({ hasText: "Rename this function to sumOf" })).toBeVisible({ timeout: 60_000 });
    await page.screenshot({ path: "screenshots/e2e-claude-comments-1440.png" });
  });
});
