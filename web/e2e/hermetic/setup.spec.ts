// First-run setup (DESIGN.md 4.14), against `crowsnest --demo` started with neither a login nor
// --no-auth, so it comes up locked: the API refuses everything but health and setup, every page
// goes to /setup, and the log holds a link with a one-time token. The test reads that link as a
// person would, opens it, completes the form, lands signed in, and sees the orchestrator start.
// Then the Account sheet's password change signs a second browser out.
import { expect, expectAccessible, expectNoSidewaysScroll, sameOriginHeaders, smallTapTargets, test, isPhone } from "../support/fixtures";
import { completeSetup, finishChecklist, OWNER } from "../support/setup";

test.use({ appOptions: { demo: true, setup: true } });

test("a fresh instance is locked: the API refuses, every page goes to /setup", async ({ page, app }) => {
  const health = await (await page.request.get("/api/v1/health")).json();
  expect(health.data.setup_required).toBe(true);
  for (const path of ["/api/v1/auth/me", "/api/v1/fleet", "/api/v1/jobs", "/api/v1/orchestrator"]) {
    const response = await page.request.get(path);
    expect(response.status(), path).toBe(403);
    expect((await response.json()).error.code, path).toBe("SETUP_REQUIRED");
  }
  const login = await page.request.post("/api/v1/auth/login", { headers: sameOriginHeaders(app), data: { username: "x", password: "y" } });
  expect(login.status()).toBe(403);

  await page.goto("/chat");
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole("heading", { name: "Set up crowsnest" })).toBeVisible();
  // Opened without the link: the token field is empty and says where the token is.
  await expect(page.getByLabel("Setup token")).toHaveValue("");
  await expect(page.getByText("after #token=")).toBeVisible();
});

test("a wrong token is refused with a message, and the form keeps its fields", async ({ page }) => {
  await page.goto("/setup#token=not-the-token");
  await expect(page).toHaveURL(/\/setup$/);
  await page.getByLabel("Username").fill(OWNER.username);
  await page.getByLabel("Password", { exact: true }).fill(OWNER.password);
  await page.getByLabel("Password again").fill(OWNER.password);
  await page.getByRole("button", { name: "Set up crowsnest" }).click();
  await expect(page.getByRole("alert")).toContainText("setup token is not right");
  await expect(page.getByLabel("Username")).toHaveValue(OWNER.username);
});

test("setup from the link in the log signs in, names the instance and starts the orchestrator", async ({ page, app }) => {
  await completeSetup(page, app);
  // The setup server hands over to the full one; the page waits, then opens the first-run
  // checklist, whose Start starts the orchestrator and opens Chat.
  await finishChecklist(page);
  await page.goto("/");
  await expect(page.getByRole("textbox", { name: /What needs doing/i })).toBeVisible();
  await expect(page).toHaveTitle(/e2e-box/);

  const me = await (await page.request.get("/api/v1/auth/me")).json();
  expect(me.data.username).toBe(OWNER.username);
  expect(me.data.features.name).toBe("e2e-box");
  await expect
    .poll(async () => (await (await page.request.get("/api/v1/orchestrator")).json()).data.state, { timeout: 15_000 })
    .not.toMatch(/disabled|starting/);
  const health = await (await page.request.get("/api/v1/health")).json();
  expect(health.data.setup_required).toBe(false);

  // Setup cannot run twice, and the token never reached a request log line.
  const token = new URL(app.setupLink()).hash.replace("#token=", "");
  const again = await page.request.post("/api/v1/setup", { headers: sameOriginHeaders(app), data: { token, username: "x", password: "another long password", name: "" } });
  expect(again.status()).toBe(410);
  for (const line of app.log().split("\n")) {
    if (line.includes('"msg":"request"')) expect(line).not.toContain(token);
  }

  // The chosen password signs in after a sign-out.
  await page.context().clearCookies();
  await page.goto("/");
  await expect(page).toHaveURL(/\/login/);
  await page.getByLabel("Username").fill(OWNER.username);
  await page.getByLabel("Password").fill(OWNER.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("textbox", { name: /What needs doing/i })).toBeVisible();
});

test("the setup page fits a phone and passes axe", async ({ page, app }, testInfo) => {
  await page.goto(app.setupLink());
  await expect(page.getByRole("heading", { name: "Set up crowsnest" })).toBeVisible();
  await expectNoSidewaysScroll(page);
  if (isPhone(testInfo)) expect(await smallTapTargets(page, "main")).toEqual([]);
  await expectAccessible(page, testInfo, "setup");
});

test("changing the password in the Account sheet signs a second browser out", async ({ page, app, browser }) => {
  await completeSetup(page, app);
  await finishChecklist(page);

  const other = await browser.newContext({ baseURL: app.baseURL });
  const signIn = await other.request.post("/api/v1/auth/login", { headers: sameOriginHeaders(app), data: OWNER });
  expect(signIn.status()).toBe(200);
  expect((await other.request.get("/api/v1/auth/me")).status()).toBe(200);

  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Settings" }).click();
  // Settings → Account and name (R8: one Settings view).
  const sheet = page.getByRole("region", { name: "Account and name" });
  await expect(sheet.getByText("Chosen at setup")).toBeVisible();
  await sheet.getByLabel("Current password").fill(OWNER.password);
  await sheet.getByLabel("New password", { exact: true }).fill("e2e setup password two");
  await sheet.getByLabel("New password again").fill("e2e setup password two");
  await sheet.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByText("Password changed")).toBeVisible();

  expect((await other.request.get("/api/v1/auth/me")).status()).toBe(401);
  expect((await page.request.get("/api/v1/auth/me")).status()).toBe(200);
  const oldPassword = await other.request.post("/api/v1/auth/login", { headers: sameOriginHeaders(app), data: OWNER });
  expect(oldPassword.status()).toBe(401);
  await other.close();
});
