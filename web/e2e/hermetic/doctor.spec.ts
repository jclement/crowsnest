// The guided first run, the System page, Settings and the machine strip (DESIGN.md 3.2, 3.4,
// 4.14.1, 12.x), against `crowsnest --demo` with a scripted machine (CROWSNEST_DEMO_DOCTOR): all
// green, herdr missing and then installed, Claude not signed in, Codex only, and both agents.
// Each first-run test starts locked, completes setup from the log's link, and lands on the
// checklist. Screenshots at 375 and 1440 go to web/screenshots.
import type { Page, TestInfo } from "@playwright/test";
import { expect, expectAccessible, expectNoSidewaysScroll, isPhone, sameOriginHeaders, test } from "../support/fixtures";
import { completeSetup } from "../support/setup";
import type { Instance } from "../support/instance";

const sizes = [
  { name: "375x812", width: 375, height: 812 },
  { name: "1440x900", width: 1440, height: 900 },
] as const;

async function shoot(page: Page, name: string) {
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: `screenshots/${name}.png`, animations: "disabled", caret: "hide" });
}

function onDesktopOnly(testInfo: TestInfo) {
  test.skip(testInfo.project.name !== "desktop-chromium", "photographed once, from desktop Chromium at both sizes");
}

async function toChecklist(page: Page, app: Instance) {
  await completeSetup(page, app);
  await expect(page).toHaveURL(/\/welcome$/, { timeout: 20_000 });
  await expect(page.getByRole("heading", { name: "Almost there" })).toBeVisible();
}

test.describe("all green", () => {
  test.use({ appOptions: { demo: true, setup: true } });

  test("setup lands on the checklist, already green; Start opens Chat with the orchestrator up", async ({ page, app }, testInfo) => {
    await toChecklist(page, app);
    const checks = page.getByRole("region", { name: "Checks" });
    for (const id of ["herdr.installed", "herdr.session", "agent.claude"]) {
      await expect(checks.locator(`[data-check="${id}"] [data-status="ok"]`)).toBeVisible();
    }
    await expect(page.getByText("Everything it needs is here")).toBeVisible();
    // The orchestrator waits for Start.
    const before = await (await page.request.get("/api/v1/orchestrator")).json();
    expect(before.data.state).toBe("waiting");
    await expectNoSidewaysScroll(page);
    await expectAccessible(page, testInfo, "welcome");
    await page.getByRole("button", { name: "Start" }).click();
    await expect(page).toHaveURL(/\/chat$/, { timeout: 20_000 });
    await expect.poll(async () => (await (await page.request.get("/api/v1/orchestrator")).json()).data.state).toMatch(/idle|working/);
    // Reloading the app no longer goes to the checklist.
    await page.goto("/");
    await expect(page.getByRole("textbox", { name: /What needs doing/i })).toBeVisible();
  });

  for (const size of sizes) {
    test(`checklist screenshot at ${size.name}`, async ({ page, app }, testInfo) => {
      onDesktopOnly(testInfo);
      await page.addInitScript(() => localStorage.setItem("crowsnest-theme", "light"));
      await page.setViewportSize({ width: size.width, height: size.height });
      await toChecklist(page, app);
      await page.getByText("Optional, any time later").click();
      await shoot(page, `welcome-green-${size.name}`);
    });
  }
});

test.describe("herdr missing", () => {
  test.use({ appOptions: { demo: true, setup: true, env: { CROWSNEST_DEMO_DOCTOR: "herdr-missing" } } });

  test("says how to install herdr, holds Start, and notices when it appears", async ({ page, app }, testInfo) => {
    await toChecklist(page, app);
    const herdr = page.locator('[data-check="herdr.installed"]');
    await expect(herdr.locator('[data-status="fail"]')).toBeVisible();
    await expect(herdr.getByText("brew install herdr", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Start" })).toBeDisabled();
    if (testInfo.project.name === "desktop-chromium") {
      for (const size of sizes) {
        await page.setViewportSize({ width: size.width, height: size.height });
        await shoot(page, `welcome-herdr-missing-${size.name}`);
      }
      await page.setViewportSize({ width: 1440, height: 900 });
    }
    // "Install" herdr: the scripted machine now has it. Re-check finds it.
    const fixed = await page.request.post("/api/v1/system/demo-state", { headers: sameOriginHeaders(app), data: { state: "ok" } });
    expect(fixed.status()).toBe(200);
    await page.getByRole("button", { name: "Re-check" }).click();
    await expect(herdr.locator('[data-status="ok"]')).toBeVisible();
    await expect(page.getByRole("button", { name: "Start" })).toBeEnabled();
    await page.getByRole("button", { name: "Start" }).click();
    await expect(page).toHaveURL(/\/chat$/, { timeout: 20_000 });
  });
});

test.describe("Claude not signed in", () => {
  test.use({ appOptions: { demo: true, setup: true, env: { CROWSNEST_DEMO_DOCTOR: "claude-signed-out" } } });

  test("offers a terminal with the sign-in command", async ({ page, app }, testInfo) => {
    await toChecklist(page, app);
    const claude = page.locator('[data-check="agent.claude"]');
    await expect(claude.locator('[data-status="fail"]')).toBeVisible();
    await expect(claude.getByText("not signed in")).toBeVisible();
    await expect(claude.getByRole("button", { name: "Open a terminal to sign in" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Start" })).toBeEnabled(); // Claude is installed: the orchestrator can start and show its login
    if (testInfo.project.name === "desktop-chromium") {
      for (const size of sizes) {
        await page.setViewportSize({ width: size.width, height: size.height });
        await shoot(page, `welcome-claude-signed-out-${size.name}`);
      }
    }
    // The demo has no real shell: the button says so instead of opening one.
    await claude.getByRole("button", { name: "Open a terminal to sign in" }).click();
    await expect(page.getByRole("alert")).toContainText("Could not open a shell");
  });
});

test.describe("Codex only", () => {
  test.use({ appOptions: { demo: true, setup: true, env: { CROWSNEST_DEMO_DOCTOR: "codex-only" } } });

  test("says plainly what Codex mode lacks, and starts on Codex", async ({ page, app }, testInfo) => {
    await toChecklist(page, app);
    await expect(page.locator('[data-check="agent.codex"] [data-status="ok"]')).toBeVisible();
    await expect(page.getByText("What Codex mode lacks")).toBeVisible();
    await expect(page.getByText(/No stop-hook guarantee/)).toBeVisible();
    if (testInfo.project.name === "desktop-chromium") {
      for (const size of sizes) {
        await page.setViewportSize({ width: size.width, height: size.height });
        await shoot(page, `welcome-codex-only-${size.name}`);
      }
    }
    await page.getByRole("button", { name: "Start" }).click();
    await expect(page).toHaveURL(/\/chat$/, { timeout: 20_000 });
    const system = await (await page.request.get("/api/v1/system")).json();
    expect(system.data.selection.orchestrator).toBe("codex");
  });
});

test.describe("both agents", () => {
  test.use({ appOptions: { demo: true, setup: true, env: { CROWSNEST_DEMO_DOCTOR: "both" } } });

  test("asks which agent runs the orchestrator and saves the choice", async ({ page, app }) => {
    await toChecklist(page, app);
    const choice = page.getByRole("group", { name: "Which agent runs the orchestrator?" });
    await expect(choice.getByRole("radio", { name: /Claude Code/ })).toBeChecked();
    await choice.getByRole("radio", { name: /Codex/ }).check();
    await expect(page.getByText("What Codex mode lacks")).toBeVisible();
    await page.getByRole("button", { name: "Start" }).click();
    await expect(page).toHaveURL(/\/chat$/, { timeout: 20_000 });
    const settings = await (await page.request.get("/api/v1/settings")).json();
    const engine = settings.data.settings.find((setting: { name: string }) => setting.name === "engine");
    expect(engine.value).toBe("codex");
    expect(engine.source).toBe("database");
  });
});

// ---- System, Settings and the machine strip (no first run: --no-auth) -------------------------

test.describe("system page", () => {
  test.use({ appOptions: { demo: true, env: { CROWSNEST_POKE_INTERVAL: "7m" } } });

  test("account menu → System shows the checks, versions and agents; Re-check runs them again", async ({ page }, testInfo) => {
    await page.goto("/");
    await page.getByRole("button", { name: "Account" }).click();
    await page.getByRole("menuitem", { name: "System" }).click();
    await expect(page).toHaveURL(/\/system$/);
    await expect(page.getByRole("heading", { name: "System" })).toBeVisible();
    await expect(page.getByText("Ready", { exact: true })).toBeVisible();
    await expect(page.getByText("herdr 0.9.5 ✓")).toBeVisible();
    await expect(page.getByText("2.1.332", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Re-check" }).click();
    await expect(page.getByRole("button", { name: "Re-check" })).toBeEnabled();
    await expectNoSidewaysScroll(page);
    if (isPhone(testInfo)) return;
    await expectAccessible(page, testInfo, "system");
  });

  test("diagnostics are redacted text", async ({ page }) => {
    const response = await page.request.get("/api/v1/system/diagnostics");
    expect(response.status()).toBe(200);
    expect(response.headers()["content-type"]).toContain("text/plain");
    const text = await response.text();
    expect(text).toContain("crowsnest doctor");
    expect(text).toContain("poke-interval = 7m  (env: CROWSNEST_POKE_INTERVAL)");
    expect(text).not.toMatch(/cnt_[A-Za-z0-9]{20,}/);
  });

  test("the palette opens System and Settings", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("button", { name: "Search or run…" }).click();
    const input = page.getByRole("combobox", { name: "Search or run a command" });
    await input.fill("system");
    await page.getByRole("option", { name: /^System/ }).first().click();
    await expect(page).toHaveURL(/\/system$/);
    await page.getByRole("button", { name: "Search or run…" }).click();
    await input.fill("settings");
    await page.getByRole("option", { name: /^Settings/ }).first().click();
    // The Settings view (R8: one door to configuration). R10: the palette's Settings is the view
    // itself, as More, the rail and g s are; Settings: <section> rows go to a section.
    await expect(page).toHaveURL(/\/settings$/);
    await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
  });

  test("Settings: a change is saved, shown as set here and journalled; an env-set value is read-only", async ({ page, app }) => {
    await page.goto("/");
    await page.getByRole("button", { name: "Account" }).click();
    await page.getByRole("menuitem", { name: "Settings" }).click();
    await expect(page).toHaveURL(/\/settings$/);
    const sheet = page.getByRole("main");
    // Set by the environment: read-only, says where.
    const poke = sheet.getByLabel("Poke interval");
    await expect(poke).toBeDisabled();
    await expect(sheet.getByText("Set by the environment (CROWSNEST_POKE_INTERVAL): change it there")).toBeVisible();
    // Editable: the job model.
    const jobModel = sheet.getByLabel("Job model");
    await jobModel.fill("haiku");
    await jobModel.locator("xpath=ancestor::form").getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Job model saved")).toBeVisible();
    await expect(jobModel.locator("xpath=ancestor::form").getByText("set here")).toBeVisible();
    const journal = await (await page.request.get("/api/v1/journal?kinds=settings")).json();
    expect(JSON.stringify(journal.data)).toContain("Job model (none) → haiku");
    // An invalid value is refused with the loader's message, nothing saved.
    const bad = await page.request.put("/api/v1/settings/permission-mode", { headers: sameOriginHeaders(app), data: { value: "yolo" } });
    expect(bad.status()).toBe(400);
    expect((await bad.json()).error.message).toContain("permission-mode");
  });

  test("the bind, the login and the session cannot be changed from a browser", async ({ page, app }) => {
    for (const name of ["bind", "no-auth", "session", "data-dir", "password"]) {
      const response = await page.request.put(`/api/v1/settings/${name}`, { headers: sameOriginHeaders(app), data: { value: "x" } });
      expect(response.status(), name).toBe(409);
    }
  });

  test("the machine strip shows CPU, memory and load without overflowing", async ({ page }, testInfo) => {
    await page.goto("/");
    const strip = page.getByTestId("machine-strip");
    await expect(strip).toBeVisible();
    if (isPhone(testInfo)) {
      await strip.getByRole("button", { name: /Machine:/ }).click();
      await expect(strip.getByRole("dialog", { name: "Machine" })).toBeVisible();
    } else {
      await expect(strip.getByLabel(/^CPU:/).filter({ visible: true })).toBeVisible();
      await expect(strip.getByLabel(/^Memory:/).filter({ visible: true })).toBeVisible();
      await expect(strip.getByLabel(/^Load:/).filter({ visible: true })).toBeVisible();
    }
    await expectNoSidewaysScroll(page);
  });

  for (const size of sizes) {
    test(`system and header screenshots at ${size.name}`, async ({ page }, testInfo) => {
      onDesktopOnly(testInfo);
      await page.addInitScript(() => localStorage.setItem("crowsnest-theme", "light"));
      await page.setViewportSize({ width: size.width, height: size.height });
      await page.goto("/system");
      await expect(page.getByText("Ready", { exact: true })).toBeVisible();
      // Let a few samples arrive for the sparkline.
      await page.waitForTimeout(5_000);
      await expectNoSidewaysScroll(page);
      await shoot(page, `system-${size.name}`);
      await page.goto("/");
      await expect(page.getByTestId("machine-strip")).toBeVisible();
      await page.waitForTimeout(4_000);
      if (size.width < 768) await page.getByTestId("machine-strip").getByRole("button", { name: /Machine:/ }).click();
      await page.screenshot({ path: `screenshots/header-machine-${size.name}.png`, clip: { x: 0, y: 0, width: size.width, height: size.width < 768 ? 220 : 60 }, animations: "disabled" });
      await page.goto("/");
      await page.getByRole("button", { name: "Account" }).click();
      await page.getByRole("menuitem", { name: "Settings" }).click();
      await expect(page.getByRole("main").getByRole("region", { name: "Agent settings" })).toBeVisible();
      await shoot(page, `settings-${size.name}`);
    });
  }
});
