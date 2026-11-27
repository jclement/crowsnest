// Real Claude through the browser (`mise run e2e:claude`): the diff view follows a real job live
// (DESIGN.md 6.21). A haiku builder in a worktree of a scratch repository waits, then edits a
// file, while this page has the job's diff open; the change appears in the open page without a
// reload, carried by the job's activity → a debounced git reading → the `changes` event.
//
// The page under test is never navigated after it opens the diff: a folder-trust question in the
// fresh worktree is answered from a second page.
import { expect, test } from "@playwright/test";
import type { Instance } from "../support/instance";
import { getJob, logSpend, makeScratch, MODEL, realClaudeOnly, removeScratch, startReal, trustFolders, writeHeaders, type Scratch } from "./rig";

let scratch: Scratch;
let instance: Instance;

test.describe.serial("the diff view follows a real job", () => {
  realClaudeOnly(["herdr", "claude", "git"]);

  test.beforeAll(async () => {
    scratch = makeScratch("cn-diff", { "notes.txt": "first line\n" });
    instance = await startReal(scratch, { review: "off" });
  });

  test.afterAll(async () => {
    await logSpend(instance, "diff");
    await instance?.stop();
    removeScratch(scratch);
  });

  test("a running job edits a file while its diff is open, and the change appears without a reload", async ({ page, context, request }) => {
    test.setTimeout(10 * 60_000);
    const created = await request.post(`${instance.baseURL}/api/v1/jobs`, {
      headers: writeHeaders(instance),
      data: {
        brief:
          "First run the shell command `sleep 45` with your Bash tool and wait for it to finish (someone is watching your diff). " +
          "Then append one line, exactly `live-change-ok`, to the file notes.txt. Then commit it with git (git add notes.txt && git commit -m 'notes: live'). " +
          "Then call your crowsnest done tool with a one-line result, verified as an empty list and not_verified as an empty list.",
        dir: scratch.repo,
        role: "builder",
        isolate: true,
        model: MODEL,
      },
    });
    expect(created.status()).toBe(201);
    const id = ((await created.json()) as { data: { id: string } }).data.id;

    // Wait for its worktree (answering a folder-trust question from a second page).
    const helper = await context.newPage();
    await expect
      .poll(
        async () => {
          await trustFolders(helper, instance, [id]);
          return (await getJob(helper, instance, id)).work_dir;
        },
        { timeout: 3 * 60_000, intervals: [3_000], message: "the job's worktree" },
      )
      .not.toBe("");

    await page.goto(`${instance.baseURL}/diff?job=${id}`);
    const viewer = page.getByRole("region", { name: /^Changes: / });
    await expect(viewer.getByText("0 of 0 viewed")).toBeVisible();
    // A marker that a reload would wipe.
    await page.evaluate(() => {
      (window as unknown as { __diffMarker: number }).__diffMarker = 42;
    });

    // The agent's edit arrives in the open page: no reload, no click.
    await expect
      .poll(
        async () => {
          await trustFolders(helper, instance, [id]);
          return viewer.getByRole("region", { name: "notes.txt" }).isVisible();
        },
        { timeout: 6 * 60_000, intervals: [3_000], message: "notes.txt in the open diff" },
      )
      .toBe(true);
    await expect(viewer.getByRole("table", { name: "Diff of notes.txt" })).toContainText("live-change-ok");
    expect(await page.evaluate(() => (window as unknown as { __diffMarker?: number }).__diffMarker), "the page was not reloaded").toBe(42);
    await page.screenshot({ path: "screenshots/e2e-claude-diff-live-1440.png" });
  });
});
