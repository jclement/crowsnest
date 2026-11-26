// The Notifications sheet on every kind of device: what it says when push cannot work here, and
// turning it on, testing, switching kinds and removing devices when it can.
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PushDevice, PushEnvironment, PushSettings } from "../../lib/push";
import { describeAgent, endpointHash } from "../../lib/push";
import { renderWithClient, stubApi } from "../../test/render";
import { NotificationSettings } from "./NotificationSettings";

const SERVER_KEY = "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM";
const ENDPOINT = "https://fcm.googleapis.com/fcm/send/this-device";

/** A fake browser. `subscribed` starts it with a push subscription for ENDPOINT. */
function fakeEnv(overrides: Partial<PushEnvironment> = {}, subscribed = false) {
  let subscription: PushSubscription | null = null;
  const makeSubscription = () =>
    ({
      endpoint: ENDPOINT,
      options: { applicationServerKey: null },
      toJSON: () => ({ endpoint: ENDPOINT, expirationTime: null, keys: { p256dh: "p", auth: "a" } }),
      unsubscribe: vi.fn(async () => {
        subscription = null;
        return true;
      }),
    }) as unknown as PushSubscription;
  if (subscribed) subscription = makeSubscription();
  const subscribe = vi.fn(async (_options: PushSubscriptionOptionsInit) => (subscription = makeSubscription()));
  const registration = {
    active: {},
    pushManager: { getSubscription: vi.fn(async () => subscription), subscribe },
  } as unknown as ServiceWorkerRegistration;
  const requestPermission = vi.fn(async () => "granted" as NotificationPermission);
  const env: PushEnvironment = {
    isSecureContext: true,
    userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/140.0 Safari/537.36",
    maxTouchPoints: 0,
    standalone: false,
    serviceWorker: { register: vi.fn(async () => registration), getRegistration: vi.fn(async () => registration) } as unknown as PushEnvironment["serviceWorker"],
    pushManager: true,
    Notification: { permission: "default", requestPermission },
    ...overrides,
  };
  return { env, subscribe, requestPermission };
}

function settingsWith(devices: PushDevice[]): PushSettings {
  return {
    kinds: { question: true, dialog: true, job: true, done: true, orchestrator: true },
    order: ["question", "dialog", "job", "done", "orchestrator"],
    devices,
    max_devices: 20,
  };
}

async function device(id: number, endpoint: string, userAgent: string): Promise<PushDevice> {
  return {
    id, user_agent: userAgent, service: "fcm.googleapis.com", created_at: new Date().toISOString(),
    last_success_at: null, last_failure_at: null, failures: 0, endpoint_hash: await endpointHash(endpoint),
  };
}

function stubPush(settings: PushSettings) {
  return stubApi((call) => {
    if (call.path === "/push/settings" && call.method === "GET") return { body: { data: settings } };
    if (call.path === "/push/settings" && call.method === "PUT") {
      const kinds = { ...settings.kinds, ...(call.body as { kinds: PushSettings["kinds"] }).kinds };
      return { body: { data: { ...settings, kinds } } };
    }
    if (call.path === "/push/key") return { body: { data: { public_key: SERVER_KEY } } };
    if (call.path === "/push/subscriptions" && call.method === "POST") return { status: 201, body: { data: settings.devices[0] } };
    if (call.path === "/push/test") return { body: { data: { sent: 1, failed: 0, pruned: 0 } } };
    if (call.path.startsWith("/push/subscriptions/") && call.method === "DELETE") return { body: { data: { removed: true } } };
    return undefined;
  });
}

describe("NotificationSettings: when this device cannot get notifications", () => {
  const cases: [string, Partial<PushEnvironment>, RegExp][] = [
    ["plain HTTP on a tailnet", { isSecureContext: false }, /Not a secure connection/],
    ["iOS in a Safari tab", { userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1", maxTouchPoints: 5 }, /Add crowsnest to your Home Screen/],
    ["an iPad that says it is a Mac", { userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15", maxTouchPoints: 5 }, /Add crowsnest to your Home Screen/],
    ["no service worker", { serviceWorker: undefined }, /cannot receive push notifications/],
    ["no Push API", { pushManager: false }, /cannot receive push notifications/],
    ["notifications blocked", { Notification: { permission: "denied", requestPermission: async () => "denied" } }, /blocked for this site/],
  ];
  for (const [name, overrides, message] of cases) {
    it(`says what is missing: ${name}`, async () => {
      stubPush(settingsWith([]));
      renderWithClient(<NotificationSettings open onClose={() => {}} env={fakeEnv(overrides).env} />);
      expect(await screen.findByText(message)).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /Turn on/ })).toBeNull();
      // The kinds and devices still show: another device may be getting them.
      expect(await screen.findByRole("switch", { name: /Questions/ })).toBeInTheDocument();
    });
  }

  it("iOS installed to the Home Screen can turn them on", async () => {
    stubPush(settingsWith([]));
    const { env } = fakeEnv({ userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)", maxTouchPoints: 5, standalone: true });
    renderWithClient(<NotificationSettings open onClose={() => {}} env={env} />);
    expect(await screen.findByRole("button", { name: "Turn on for this device" })).toBeInTheDocument();
  });
});

describe("NotificationSettings: turning them on", () => {
  it("asks permission on the click, subscribes with the server key, and stores the subscription", async () => {
    const calls = stubPush(settingsWith([]));
    const { env, subscribe, requestPermission } = fakeEnv();
    renderWithClient(<NotificationSettings open onClose={() => {}} env={env} />);
    expect(requestPermission).not.toHaveBeenCalled();
    await userEvent.click(await screen.findByRole("button", { name: "Turn on for this device" }));
    await waitFor(() => expect(calls.some((call) => call.method === "POST" && call.path === "/push/subscriptions")).toBe(true));
    expect(requestPermission).toHaveBeenCalledOnce();
    expect(subscribe).toHaveBeenCalledOnce();
    const options = subscribe.mock.calls[0]![0];
    expect(options.userVisibleOnly).toBe(true);
    expect((options.applicationServerKey as Uint8Array).length).toBe(65);
    expect(calls.find((call) => call.path === "/push/subscriptions")!.body).toMatchObject({ endpoint: ENDPOINT });
    expect(await screen.findByText("Notifications are on for this device")).toBeInTheDocument();
  });

  it("a refused prompt subscribes nothing and says so", async () => {
    const calls = stubPush(settingsWith([]));
    const { env, subscribe } = fakeEnv({ Notification: { permission: "default", requestPermission: async () => "denied" } });
    renderWithClient(<NotificationSettings open onClose={() => {}} env={env} />);
    await userEvent.click(await screen.findByRole("button", { name: "Turn on for this device" }));
    expect(await screen.findByText("Notifications were blocked")).toBeInTheDocument();
    expect(subscribe).not.toHaveBeenCalled();
    expect(calls.some((call) => call.path === "/push/subscriptions")).toBe(false);
  });
});

describe("NotificationSettings: when this device is on", () => {
  it("marks this device, sends a test to it, and removes another device", async () => {
    const here = await device(1, ENDPOINT, "Mozilla/5.0 (Macintosh) Chrome/140.0 Safari/537.36");
    const phone = await device(2, "https://web.push.apple.com/other", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0) Safari/604.1");
    const calls = stubPush(settingsWith([here, phone]));
    renderWithClient(<NotificationSettings open onClose={() => {}} env={fakeEnv({}, true).env} />);
    const list = await screen.findByRole("list", { name: "Devices" });
    await waitFor(() => expect(within(list).getByText("This device")).toBeInTheDocument());
    expect(within(list).getByText("Safari on iPhone")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Send a test" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/push/test")).toBe(true));
    expect(calls.find((call) => call.path === "/push/test")!.body).toEqual({ endpoint: ENDPOINT });

    await userEvent.click(screen.getByRole("button", { name: "Remove Safari on iPhone" }));
    await waitFor(() => expect(calls.some((call) => call.method === "DELETE" && call.path === "/push/subscriptions/2")).toBe(true));
  });

  it("switches a kind off", async () => {
    const calls = stubPush(settingsWith([]));
    renderWithClient(<NotificationSettings open onClose={() => {}} env={fakeEnv().env} />);
    const done = await screen.findByRole("switch", { name: /Jobs done/ });
    expect(done).toHaveAttribute("aria-checked", "true");
    await userEvent.click(done);
    await waitFor(() => expect(screen.getByRole("switch", { name: /Jobs done/ })).toHaveAttribute("aria-checked", "false"));
    expect(calls.find((call) => call.method === "PUT")!.body).toEqual({ kinds: { done: false } });
  });
});

describe("describeAgent", () => {
  it("names the browser and platform", () => {
    expect(describeAgent("Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/140.0 Safari/537.36 Edg/140.0")).toBe("Edge on Windows");
    expect(describeAgent("Mozilla/5.0 (Android 15) Firefox/140.0")).toBe("Firefox on Android");
    expect(describeAgent("")).toBe("Unknown browser");
  });
});
