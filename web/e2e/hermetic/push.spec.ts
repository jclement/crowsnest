// Web Push in the browser (DESIGN.md 8.5), against the real binary in --demo:
//
//   1. Turning notifications on from the Notifications sheet registers the service worker,
//      subscribes with the server's VAPID key (PushManager mocked: headless Chromium has no FCM
//      connection, see support/fake-push.ts) and stores the subscription.
//   2. "Send a test" makes the server encrypt and POST to the fake push service; the test decrypts
//      the body with the subscription's private key and finds the payload.
//   3. That payload, delivered to the real service worker through CDP
//      (ServiceWorker.deliverPushMessage), shows a notification.
//   4. Clicking it (a notificationclick dispatched in the worker) focuses the open tab and the app
//      navigates to the item's deep link.
//
// What this cannot prove: delivery through a real push service (FCM, Mozilla, Apple) and the
// operating system's notification UI. REPORT.md says what was checked by hand.
//
// Chromium only: Playwright's WebKit has no service-worker push and no CDP. And full Chromium in
// its new headless mode (channel "chromium", which `playwright install chromium` also installs),
// not the default headless shell: the shell reports Notification.permission "denied" whatever
// grantPermissions says, and showNotification refuses (checked 2026-11-26).
import type { BrowserContext, CDPSession, Page } from "@playwright/test";
import { mockPushManagerScript, startFakePush, type FakePush } from "../support/fake-push";
import { expect, expectAccessible, isPhone, smallTapTargets, test } from "../support/fixtures";

test.skip(({ browserName }) => browserName !== "chromium", "service-worker push needs Chromium (CDP)");
test.use({ channel: "chromium" });

let fake: FakePush;
test.beforeEach(async ({ context, page, baseURL }) => {
  fake = await startFakePush();
  await context.grantPermissions(["notifications"], { origin: baseURL });
  await page.addInitScript(mockPushManagerScript(fake.endpoint, fake.keys));
});
test.afterEach(async () => fake.close());

/** Opens the Notifications sheet from the account menu. */
async function openNotifications(page: Page) {
  await page.goto("/");
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Notifications" }).click();
  return page.getByRole("dialog", { name: "Notifications" });
}

/** The service worker's registration id, from CDP. */
async function registrationId(cdp: CDPSession, scopeURL: string): Promise<string> {
  return new Promise((resolve) => {
    cdp.on("ServiceWorker.workerRegistrationUpdated", ({ registrations }) => {
      const match = registrations.find((registration) => registration.scopeURL === scopeURL && !registration.isDeleted);
      if (match) resolve(match.registrationId);
    });
    void cdp.send("ServiceWorker.enable");
  });
}

test("turn on, test, receive a push and open its deep link", async ({ page, context, baseURL }, testInfo) => {
  const sheet = await openNotifications(page);
  await sheet.getByRole("button", { name: "Turn on for this device" }).click();
  await expect(page.getByText("Notifications are on for this device")).toBeVisible();
  await expect(sheet.getByRole("list", { name: "Devices" }).getByText("This device")).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __pushSubscribeOptions: unknown }).__pushSubscribeOptions)).toEqual({ userVisibleOnly: true, keyLength: 65 });

  // The service worker is registered at the root and has no fetch handler (no cache, no offline).
  const worker = context.serviceWorkers()[0] ?? (await context.waitForEvent("serviceworker"));
  expect(new URL(worker.url()).pathname).toBe("/sw.js");
  expect(await page.evaluate(async () => (await navigator.serviceWorker.getRegistration("/"))?.scope)).toBe(`${baseURL}/`);

  // A test notification reaches the (fake) push service, encrypted for this subscription.
  await sheet.getByRole("button", { name: "Send a test" }).click();
  await expect(page.getByText("Test sent")).toBeVisible();
  expect(fake.requests).toHaveLength(1);
  const pushed = fake.requests[0]!;
  expect(pushed.path).toBe("/push/e2e-device");
  expect(pushed.headers["content-encoding"]).toBe("aes128gcm");
  expect(pushed.headers["urgency"]).toBe("normal");
  expect(String(pushed.headers["authorization"])).toMatch(/^vapid t=[\w-]+\.[\w-]+\.[\w-]+, k=[\w-]{87}$/);
  const payload = JSON.parse(fake.decrypt(pushed.body)) as { title: string; body: string; url: string; tag: string };
  expect(payload).toMatchObject({ title: "crowsnest", body: "Notifications work on this device.", url: "/", tag: "test" });

  // The service worker shows a pushed notice. A real push service would carry the encrypted body
  // and the browser would decrypt it; here CDP hands the worker the plaintext directly.
  const cdp = await context.newCDPSession(page);
  const registration = await registrationId(cdp, `${baseURL}/`);
  const notice = { title: "An agent is blocked on you", body: "Ship the migration tonight?", tag: "q_1", url: "/inbox?q=q_1", kind: "question" };
  await cdp.send("ServiceWorker.deliverPushMessage", { origin: baseURL!, registrationId: registration, data: JSON.stringify(notice) });
  await expect
    .poll(() => page.evaluate(async () => (await (await navigator.serviceWorker.ready).getNotifications()).map((n) => ({ title: n.title, body: n.body, tag: n.tag, data: n.data as unknown }))))
    .toEqual([{ title: notice.title, body: notice.body, tag: "q_1", data: { url: "/inbox?q=q_1", kind: "question" } }]);

  // Tapping it: the worker's notificationclick focuses this tab and the app routes to the item.
  await page.getByRole("button", { name: "Close" }).click();
  await worker.evaluate(async () => {
    // The worker's globals, typed by hand: this project's TypeScript lib is the DOM, not WebWorker.
    const scope = self as unknown as {
      registration: ServiceWorkerRegistration;
      dispatchEvent: (event: Event) => boolean;
      NotificationEvent: new (type: string, init: { notification: Notification }) => Event;
    };
    const [shown] = await scope.registration.getNotifications();
    scope.dispatchEvent(new scope.NotificationEvent("notificationclick", { notification: shown! }));
  });
  await expect(page).toHaveURL(/\/inbox\?q=q_1$/);
  const question = (await (await page.request.get("/api/v1/questions/q_1")).json()) as { data: { title: string } };
  await expect(page.getByRole("heading", { name: question.data.title }).first()).toBeVisible();
  expect(await page.evaluate(async () => (await (await navigator.serviceWorker.ready).getNotifications()).length)).toBe(0);

  if (!isPhone(testInfo)) return;
  expect(await smallTapTargets(page)).toEqual([]);
});

/** Delivers a raw push to the service worker and returns the notifications it then shows. */
async function deliver(page: Page, context: BrowserContext, baseURL: string, data: string) {
  await openNotifications(page);
  const cdp = await context.newCDPSession(page);
  const registration = await registrationId(cdp, `${baseURL}/`);
  await cdp.send("ServiceWorker.deliverPushMessage", { origin: baseURL, registrationId: registration, data });
}

const shownNotifications = (page: Page) =>
  page.evaluate(async () => (await (await navigator.serviceWorker.ready).getNotifications()).map((n) => ({ title: n.title, url: (n.data as { url: string }).url })));

test("a pushed URL can never leave the app", async ({ page, context, baseURL }) => {
  await deliver(page, context, baseURL!, JSON.stringify({ title: "x", body: "y", tag: "evil", url: "https://evil.example/" }));
  await expect.poll(() => shownNotifications(page)).toEqual([{ title: "x", url: "/" }]);
});

test("a push that is not JSON still shows a notice", async ({ page, context, baseURL }) => {
  // A push with no notification is punished by browsers (and says nothing to Jeff).
  await deliver(page, context, baseURL!, "not json");
  await expect.poll(() => shownNotifications(page)).toEqual([{ title: "crowsnest", url: "/" }]);
});

test("the sheet passes axe and keeps tap targets at 44 px", async ({ page }, testInfo) => {
  const sheet = await openNotifications(page);
  await expect(sheet.getByRole("switch", { name: /Questions/ })).toBeVisible();
  await expectAccessible(page, testInfo, "notifications sheet");
  if (isPhone(testInfo)) expect(await smallTapTargets(page, "[role=dialog]")).toEqual([]);
});
