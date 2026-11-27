// Attachments (DESIGN.md 8.6), against `crowsnest --demo`: a screenshot pasted into Chat uploads,
// goes with the prompt and shows in the conversation as a picture that opens full size; a file
// dropped on the job box is given with the job and listed in its brief; a question an agent
// attached a screenshot and a report to shows a thumbnail, a lightbox and a download chip; a long
// paste becomes a file, with "paste as text" to undo; HTML and SVG are never served inline. In
// all three projects (the phone layout at 375 px included), with screenshots of the composer
// and the question into web/screenshots.
//
// Paste and drop are synthetic: a `paste` or `drop` event whose clipboardData or dataTransfer is
// defined on the event, which is what React reads; real clipboards are not available to tests.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { Locator, Page, TestInfo } from "@playwright/test";
import { demoDir } from "../support/instance";
import { detailRegion, expect, expectNoSidewaysScroll, isPhone, smallTapTargets, test } from "../support/fixtures";

interface FakeFile {
  name: string;
  type: string;
  /** Text content, or "png" for a picture drawn in the page. */
  content: string;
}

/** Fires a paste carrying files (or text) at a text box. */
async function paste(target: Locator, data: { files?: FakeFile[]; text?: string }) {
  await target.evaluate(async (element, { files, text }) => {
    const made: File[] = [];
    for (const file of files ?? []) {
      if (file.content === "png") {
        const canvas = document.createElement("canvas");
        canvas.width = 640;
        canvas.height = 360;
        const context = canvas.getContext("2d")!;
        context.fillStyle = "#1e293b";
        context.fillRect(0, 0, 640, 360);
        context.fillStyle = "#f8fafc";
        context.font = "bold 64px sans-serif";
        context.fillText("PELICAN", 150, 200);
        const blob = await new Promise<Blob>((resolve) => canvas.toBlob((value) => resolve(value!), "image/png"));
        made.push(new File([blob], file.name, { type: "image/png" }));
      } else {
        made.push(new File([file.content], file.name, { type: file.type }));
      }
    }
    const event = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(event, "clipboardData", {
      value: { files: made, items: [], types: made.length ? ["Files"] : ["text/plain"], getData: () => text ?? "" },
    });
    element.dispatchEvent(event);
  }, data);
}

/** Drags files over a target (dragenter), and drops them unless `drop` is false. */
async function dragFiles(target: Locator, files: FakeFile[], drop = true) {
  await target.evaluate(
    (element, { files, drop }) => {
      const made = files.map((file) => new File([file.content], file.name, { type: file.type }));
      const fire = (type: string) => {
        const event = new Event(type, { bubbles: true, cancelable: true });
        Object.defineProperty(event, "dataTransfer", { value: { types: ["Files"], files: made, items: [], dropEffect: "none" } });
        element.dispatchEvent(event);
      };
      fire("dragenter");
      fire("dragover");
      if (drop) fire("drop");
    },
    { files, drop },
  );
}

async function openChat(page: Page) {
  await page.goto("/chat");
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Attach files" })).toBeVisible();
}

const composer = (page: Page) => page.getByRole("textbox", { name: "Message to the orchestrator" });

/** Where a project's photographs go: one per size, from the Chromium projects. */
function shotName(testInfo: TestInfo, what: string): string | null {
  if (testInfo.project.name === "mobile-webkit") return null;
  return `screenshots/attach-${what}-${isPhone(testInfo) ? "375x812" : "1440x900"}.png`;
}

test("a screenshot pasted into Chat uploads, goes with the prompt and opens full size", async ({ page }, testInfo) => {
  await openChat(page);
  await paste(composer(page), { files: [{ name: "image.png", type: "image/png", content: "png" }] });
  const tray = page.getByRole("list", { name: "Attachments" });
  const chip = tray.getByRole("listitem");
  await expect(chip).toHaveCount(1);
  await expect(chip).toContainText(/pasted-\d{8}-\d{6}\.png/);
  await expect(chip).toHaveAttribute("data-status", "done");
  await expect(chip.locator("img")).toBeVisible(); // the preview from the file itself (decorative: alt="")
  await composer(page).fill("What word is in this screenshot?");

  if (isPhone(testInfo)) {
    await expectNoSidewaysScroll(page);
    expect(await smallTapTargets(page, `form[aria-label="Message the orchestrator"]`)).toEqual([]);
    await expect(page.getByRole("button", { name: "Take a photo" })).toBeVisible();
  }
  const shot = shotName(testInfo, "chat");
  if (shot) await page.screenshot({ path: shot, animations: "disabled", caret: "hide" });

  await page.getByRole("button", { name: "Send" }).click();
  await expect(composer(page)).toHaveValue("");
  await expect(tray).toHaveCount(0);
  const conversation = page.getByRole("list", { name: "Conversation" });
  await expect(conversation.getByText("What word is in this screenshot?", { exact: true })).toBeVisible();
  // The typed "Attached: …" line is shown as the picture, not as a path.
  await expect(conversation.getByText(/^Attached: /)).toHaveCount(0);
  const thumbnail = conversation.getByRole("button", { name: /^Open pasted-.*\.png full size$/ });
  await expect(thumbnail).toBeVisible();
  await expect.poll(() => thumbnail.getByRole("img").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);

  await thumbnail.click();
  const lightbox = page.getByRole("dialog");
  await expect(lightbox.getByRole("img")).toBeVisible();
  await expect.poll(() => lightbox.getByRole("img").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(640);
  await page.getByRole("button", { name: "Close" }).click();
  await expect(lightbox).toHaveCount(0);
});

test("a long paste becomes a file by default, and goes back to text on request", async ({ page }) => {
  await openChat(page);
  const log = Array.from({ length: 80 }, (_, i) => `2026-11-26T10:${String(i % 60).padStart(2, "0")}:00Z error: line ${i}`).join("\n");
  await paste(composer(page), { text: log });
  await expect(composer(page)).toHaveValue("");
  const chip = page.getByRole("list", { name: "Attachments" }).getByRole("listitem");
  await expect(chip).toContainText(/pasted-.*\.txt/);
  await expect(page.getByText(/Long paste attached as pasted-/)).toBeVisible();
  await page.getByRole("button", { name: "Paste as text instead" }).click();
  await expect(composer(page)).toHaveValue(log);
  await expect(chip).toHaveCount(0);

  // Again, and this time send it as the file.
  await composer(page).fill("");
  await paste(composer(page), { text: log });
  await expect(chip).toHaveAttribute("data-status", "done");
  await page.getByRole("button", { name: "Send" }).click();
  const conversation = page.getByRole("list", { name: "Conversation" });
  await expect(conversation.getByRole("link", { name: /pasted-.*\.txt/ })).toBeVisible();
  await expect(conversation.getByText("line 79")).toHaveCount(0);
});

test("a file dropped on the job box is given with the job and listed in its brief", async ({ page, app }, testInfo) => {
  await page.goto("/jobs");
  const form = page.getByRole("form", { name: "New job" });
  await expect(form.getByRole("button", { name: "Attach files" })).toBeVisible();
  const notes: FakeFile = { name: "repro steps.md", type: "text/markdown", content: "# Repro\n\n1. Open the board\n2. It is blank\n" };
  await dragFiles(form, [notes], false);
  await expect(page.getByTestId("drop-overlay")).toContainText("Drop files to give with the job");
  await dragFiles(form, [notes]);
  await expect(page.getByTestId("drop-overlay")).toHaveCount(0);
  await expect(form.getByRole("list", { name: "Attachments" }).getByRole("listitem")).toHaveAttribute("data-status", "done");

  await page.getByLabel("What needs doing?").fill("Fix the blank board\n\nThe steps are attached.");
  await page.getByRole("button", { name: /To the orchestrator/ }).click();
  await page.getByLabel(/^Where/).fill(app.root);
  await page.getByRole("button", { name: "Give job" }).click();
  await expect(page.getByText("Job j_24 given")).toBeVisible();
  const detail = detailRegion(page, testInfo, "Selected job");
  const files = detail.getByRole("list", { name: "Given with the brief" });
  await expect(files.getByRole("link", { name: /repro steps\.md/ })).toBeVisible({ timeout: 15_000 });
  await expect(detail.getByRole("list", { name: "Timeline" })).toContainText("Brief sent to job-24", { timeout: 15_000 });
  // The agent reads it from its inbox: the brief file names the copy.
  const brief = readFileSync(join(demoDir(app), "state", "jobs", "j_24", "brief.md"), "utf8");
  expect(brief).toMatch(/Attached: \S+\/jobs\/j_24\/attachments\/a_[0-9a-f]{24}-repro-steps\.md \(text\/markdown, \d+ B\)/);
});

test("a question with an image attachment shows a thumbnail that opens full size, and a report to download", async ({ page }, testInfo) => {
  const response = await page.request.get("/api/v1/questions?status=open");
  const open = ((await response.json()) as { data: Array<{ id: string; title: string }> }).data;
  const question = open.find((item) => item.title === "Which findings should the builder fix before merge?");
  expect(question, "the seeded question with attachments").toBeDefined();
  await page.goto(`/inbox?q=${question!.id}`);
  const detail = detailRegion(page, testInfo, "Selected item");
  const files = detail.getByRole("list", { name: "Files the agent attached" });
  const thumbnail = files.getByRole("button", { name: "Open review-screenshot.png full size" });
  await expect(thumbnail).toBeVisible();
  await expect.poll(() => thumbnail.getByRole("img").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(320);
  const report = files.getByRole("link", { name: /findings\.md/ });
  await expect(report).toHaveAttribute("href", /\/api\/v1\/attachments\/a_[0-9a-f]{24}\?download=1$/);
  if (isPhone(testInfo)) await expectNoSidewaysScroll(page);
  const shot = shotName(testInfo, "question");
  if (shot) await page.screenshot({ path: shot, animations: "disabled", caret: "hide" });

  await thumbnail.click();
  const lightbox = page.getByRole("dialog", { name: "review-screenshot.png" });
  await expect.poll(() => lightbox.getByRole("img").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(960);
  const lightShot = shotName(testInfo, "lightbox");
  if (lightShot) await page.screenshot({ path: lightShot, animations: "disabled", caret: "hide" });
  await page.keyboard.press("Escape");
  await expect(lightbox).toHaveCount(0);
});

test("HTML and SVG uploads are never served inline", async ({ page, baseURL }) => {
  const headers = { Origin: baseURL!, "X-Crowsnest": "1" };
  for (const [name, body, type] of [
    ["page.png", "<!doctype html><script>document.title='pwned'</script>", "image/png"],
    ["logo.svg", '<svg xmlns="http://www.w3.org/2000/svg"><script>document.title="pwned"</script></svg>', "image/svg+xml"],
  ] as const) {
    const upload = await page.request.post("/api/v1/attachments", { headers: { ...headers, "Content-Type": type, "X-Filename": name }, data: Buffer.from(body) });
    expect(upload.status()).toBe(201);
    const { data } = (await upload.json()) as { data: { id: string; inline: boolean; image: boolean } };
    expect(data.inline).toBe(false);
    expect(data.image).toBe(false);
    const served = await page.request.get(`/api/v1/attachments/${data.id}`);
    expect(served.headers()["content-disposition"]).toMatch(/^attachment;/);
    expect(served.headers()["x-content-type-options"]).toBe("nosniff");
    expect(served.headers()["content-security-policy"]).toMatch(/^sandbox;/);
  }
  // Without the X-Crowsnest header a cross-site form cannot upload.
  const forged = await page.request.post("/api/v1/attachments", { headers: { Origin: baseURL!, "Content-Type": "text/plain", "X-Filename": "a.txt" }, data: "hi" });
  expect(forged.status()).toBe(403);
});
