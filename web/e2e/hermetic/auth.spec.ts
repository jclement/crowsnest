// Signing in and the walls around it (DESIGN.md section 4), against `crowsnest --demo` with the
// login ON: the form, a wrong password, the lockout after five failures with its Retry-After,
// a deep link that survives the detour through /login, a session that survives a reload, and
// signing out, which ends the other tabs' event streams and terminals within a heartbeat.
// Then the refusals, driven from the test rather than the page: a cross-origin POST, a POST
// without X-Crowsnest, cross-origin and unauthenticated WebSockets, and a forged Host header.
import type { BrowserContext, Page } from "@playwright/test";
import { rawRequest } from "../support/agent";
import { expect, isPhone, sameOriginHeaders, test, TEST_LOGIN } from "../support/fixtures";
import type { Instance } from "../support/instance";

test.use({ appOptions: { demo: true, auth: TEST_LOGIN } });

async function fillLogin(page: Page, username: string, password: string) {
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

/** Signs in through the API; the cookie lands in the page's context. */
async function signInByAPI(page: Page, app: Instance) {
  const response = await page.request.post("/api/v1/auth/login", { headers: sameOriginHeaders(app), data: TEST_LOGIN });
  expect(response.status()).toBe(200);
}

async function sessionCookie(context: BrowserContext): Promise<string> {
  const cookie = (await context.cookies()).find((entry) => entry.name === "crowsnest_session");
  if (!cookie) throw new Error("no session cookie");
  return `${cookie.name}=${cookie.value}`;
}

/** Opens a WebSocket from Node with exactly these headers; resolves "open" or the refusal. */
function tryWebSocket(url: string, headers: Record<string, string>): Promise<string> {
  return new Promise((resolve) => {
    // Node's WebSocket (undici) takes headers as a non-standard option; the browser's cannot
    // forge Origin, which is the point of driving this from the test.
    const socket = new WebSocket(url, { headers } as unknown as string[]);
    const timer = setTimeout(() => resolve("timeout"), 5_000);
    socket.addEventListener("open", () => {
      clearTimeout(timer);
      socket.close();
      resolve("open");
    });
    socket.addEventListener("error", () => {
      clearTimeout(timer);
      resolve("refused");
    });
  });
}

test("signing in lands on the page the link asked for, and the session survives a reload", async ({ page }) => {
  await page.goto("/inbox?q=q_3");
  await expect(page).toHaveURL(/\/login\?redirect=/);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await fillLogin(page, TEST_LOGIN.username, TEST_LOGIN.password);
  await expect(page).toHaveURL(/\/inbox\?q=q_3$/);
  await expect(page.getByRole("heading", { name: "Where should the release notes live?" })).toBeVisible();

  await page.reload();
  await expect(page).toHaveURL(/\/inbox\?q=q_3$/);
  await expect(page.getByRole("heading", { name: "Where should the release notes live?" })).toBeVisible();
  // The account menu says who is signed in (from the board: on a phone the question is a modal sheet).
  await page.goto("/");
  await page.getByRole("button", { name: "Account" }).click();
  await expect(page.getByRole("menu")).toContainText(TEST_LOGIN.username);
});

test("a wrong password is refused with a message and the password is cleared", async ({ page }) => {
  await page.goto("/");
  await fillLogin(page, TEST_LOGIN.username, "not-the-password");
  await expect(page.getByRole("alert")).toHaveText("Wrong username or password.");
  await expect(page.getByLabel("Password")).toHaveValue("");
  await expect(page).toHaveURL(/\/login/);
});

test("five failures lock the login out with Retry-After, and the form says how long", async ({ page, app }) => {
  await page.goto("/login");
  for (let attempt = 1; attempt <= 5; attempt++) {
    await fillLogin(page, TEST_LOGIN.username, `wrong-${attempt}`);
    await expect(page.getByRole("alert")).toHaveText("Wrong username or password.");
  }
  // The sixth is refused before the password is even compared: even the right one.
  await fillLogin(page, TEST_LOGIN.username, TEST_LOGIN.password);
  await expect(page.getByRole("alert")).toHaveText(/^Too many failed sign-ins\. Try again in \d+ seconds?\.$/);
  await expect(page).toHaveURL(/\/login/);

  const response = await page.request.post("/api/v1/auth/login", { headers: sameOriginHeaders(app), data: TEST_LOGIN });
  expect(response.status()).toBe(429);
  const retryAfter = Number(response.headers()["retry-after"]);
  expect(retryAfter).toBeGreaterThan(0);
  expect(retryAfter).toBeLessThanOrEqual(60);
});

test("signing out ends the other tabs' event stream and terminal", async ({ page, context, app }, testInfo) => {
  test.skip(isPhone(testInfo), "one run is enough: this waits out a 20 s heartbeat");
  test.slow();
  await signInByAPI(page, app);
  // The second tab holds a terminal and the event stream.
  const other = await context.newPage();
  const socketClosed = new Promise<void>((resolve) => other.on("websocket", (ws) => ws.on("close", () => resolve())));
  await other.goto("/terminal");
  await expect(other.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
  await expect(other.getByRole("status").filter({ hasText: "Live" })).toBeAttached();

  await page.goto("/");
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login/);

  // Within one heartbeat (20 s) the server closes the other tab's terminal and stream; the
  // stream's reconnect finds no session and goes to the login page.
  await socketClosed;
  await expect(other).toHaveURL(/\/login/, { timeout: 35_000 });
  // And the cookie is dead on the server, not only in this browser.
  const me = await page.request.get("/api/v1/auth/me");
  expect(me.status()).toBe(401);
});

test.describe("refusals", () => {
  test.skip(({ isMobile }) => isMobile, "no browser involved: one project is enough");

  test("a cross-origin POST, and a POST without X-Crowsnest, are refused even with a session", async ({ page, app, playwright }) => {
    await signInByAPI(page, app);
    const cookie = await sessionCookie(page.context());
    const evil = await playwright.request.newContext({ extraHTTPHeaders: { Cookie: cookie } });
    try {
      const crossOrigin = await evil.post(`${app.baseURL}/api/v1/jobs`, {
        headers: { Origin: "http://evil.example", "X-Crowsnest": "1", "Content-Type": "application/json" },
        data: { brief: "rm -rf /" },
      });
      expect(crossOrigin.status()).toBe(403);
      const noOrigin = await evil.post(`${app.baseURL}/api/v1/jobs`, { headers: { "X-Crowsnest": "1" }, data: { brief: "x" } });
      expect(noOrigin.status()).toBe(403);
      const nullOrigin = await evil.post(`${app.baseURL}/api/v1/jobs`, { headers: { Origin: "null", "X-Crowsnest": "1" }, data: { brief: "x" } });
      expect(nullOrigin.status()).toBe(403);
      const noHeader = await evil.post(`${app.baseURL}/api/v1/jobs`, { headers: { Origin: app.baseURL, "Content-Type": "application/json" }, data: { brief: "x" } });
      expect(noHeader.status()).toBe(403);
      // The same call done properly works, so the refusals above are the checks, not a broken route.
      const fine = await evil.post(`${app.baseURL}/api/v1/jobs`, { headers: sameOriginHeaders(app), data: { brief: "A harmless job" } });
      expect(fine.status()).toBe(201);
    } finally {
      await evil.dispose();
    }
    // No job was created by the refused calls.
    const jobs = (await (await page.request.get("/api/v1/jobs")).json()) as { data: Array<{ title: string }> };
    expect(jobs.data.filter((job) => job.title === "rm -rf /" || job.title === "x")).toEqual([]);
  });

  test("a cross-origin or unauthenticated terminal WebSocket is refused", async ({ page, app }) => {
    await signInByAPI(page, app);
    const cookie = await sessionCookie(page.context());
    const url = `ws://127.0.0.1:${app.port}/api/v1/term/tui`;
    expect(await tryWebSocket(url, { Cookie: cookie, Origin: "http://evil.example" })).toBe("refused");
    expect(await tryWebSocket(url, { Cookie: cookie })).toBe("refused");
    expect(await tryWebSocket(url, { Origin: app.baseURL })).toBe("refused");
    // The control: the same socket with the session and the right Origin opens.
    expect(await tryWebSocket(url, { Cookie: cookie, Origin: app.baseURL })).toBe("open");
  });

  test("a forged Host header is refused before anything else runs", async ({ app }) => {
    const forged = await rawRequest({ port: app.port, method: "GET", path: "/", headers: { Host: `evil.example:${app.port}` } });
    expect(forged.status).toBe(403);
    expect(forged.body).toContain("--allowed-host");
    const api = await rawRequest({ port: app.port, method: "GET", path: "/api/v1/auth/me", headers: { Host: "evil.example" } });
    expect(api.status).toBe(403);
    // DNS rebinding's other shape: a name that resolves to us but is not ours.
    const rebound = await rawRequest({ port: app.port, method: "POST", path: "/api/v1/auth/login", headers: { Host: `attacker.test:${app.port}`, Origin: `http://attacker.test:${app.port}`, "X-Crowsnest": "1", "Content-Type": "application/json" }, body: JSON.stringify(TEST_LOGIN) });
    expect(rebound.status).toBe(403);
    const fine = await rawRequest({ port: app.port, method: "GET", path: "/api/v1/health", headers: { Host: `127.0.0.1:${app.port}` } });
    expect(fine.status).toBe(200);
  });
});
