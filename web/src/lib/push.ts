// Web Push in the browser (DESIGN.md 8.5): can this device get notifications, and turning them
// on and off.
//
// Push needs a secure context (HTTPS, or localhost), a service worker, PushManager and the
// Notification API. iOS has all of that only for a site installed to the home screen (16.4+),
// so on iOS in a Safari tab the answer is "add to Home Screen first", not a dead button.
// pushSupport() names exactly what is missing; the Settings sheet says it in words.
//
// Permission is asked only from a click (enablePush must be called straight from the handler:
// Safari refuses a permission prompt that is not in a user gesture, and the first await counts).
//
// The browser's endpoint URL is a capability (anyone holding it can push to this device), so it
// is sent to the server only to subscribe, unsubscribe or test, never in a URL; the server lists
// devices with an endpoint hash that endpointHash() computes the same way.

/** What the server pushes, each switchable. Mirrors internal/push Kind. */
export type PushKind = "approval" | "question" | "dialog" | "job" | "done" | "orchestrator";

/** A subscribed browser as the server lists it (internal/push Device). */
export interface PushDevice {
  id: number;
  user_agent: string;
  service: string;
  created_at: string;
  last_success_at: string | null;
  last_failure_at: string | null;
  failures: number;
  endpoint_hash: string;
}

/** GET/PUT /push/settings. */
export interface PushSettings {
  kinds: Record<PushKind, boolean>;
  order: PushKind[];
  devices: PushDevice[];
  max_devices: number;
}

export interface PushTestResult {
  sent: number;
  failed: number;
  pruned: number;
}

/** PushSubscription.toJSON(). */
export interface PushSubscriptionJSON {
  endpoint?: string;
  expirationTime?: number | null;
  keys?: Record<string, string>;
}

/** Words for each kind in the Settings sheet. */
export const kindLabels: Record<PushKind, { label: string; detail: string }> = {
  approval: { label: "Approvals", detail: "A tool call waits for your approval (low-risk ones get Allow and Deny buttons where the browser shows them)" },
  question: { label: "Questions", detail: "An agent asks something (blocking or normal; low-urgency ones are not pushed)" },
  dialog: { label: "Terminal dialogs", detail: "An agent waits at a permission or question dialog" },
  job: { label: "Jobs stuck or failed", detail: "A job stopped moving, failed, or needs a login or fix" },
  done: { label: "Jobs done", detail: "A job finished" },
  orchestrator: { label: "Orchestrator", detail: "It needs the terminal (a start-up prompt) or has stopped" },
};

/**
 * Whether this device can get push notifications, and if not, why:
 *   insecure     not HTTPS (or localhost): browsers offer push only to a secure context
 *   ios-install  iOS in a browser tab: push works only once added to the Home Screen
 *   unsupported  no service worker, PushManager or Notification API
 *   denied       notifications are blocked for this site
 *   available    it can; permission may still need asking
 */
export type PushSupport = "insecure" | "ios-install" | "unsupported" | "denied" | "available";

/** The parts of the browser this module reads; tests pass fakes. */
export interface PushEnvironment {
  isSecureContext: boolean;
  userAgent: string;
  maxTouchPoints: number;
  /** Running as an installed app (display-mode standalone, or iOS's navigator.standalone). */
  standalone: boolean;
  serviceWorker?: Pick<ServiceWorkerContainer, "register" | "getRegistration">;
  pushManager: boolean;
  Notification?: { permission: NotificationPermission; requestPermission: () => Promise<NotificationPermission> };
}

export function browserPushEnvironment(): PushEnvironment {
  const nav = typeof navigator !== "undefined" ? navigator : undefined;
  const win = typeof window !== "undefined" ? window : undefined;
  return {
    isSecureContext: win?.isSecureContext ?? false,
    userAgent: nav?.userAgent ?? "",
    maxTouchPoints: nav?.maxTouchPoints ?? 0,
    standalone:
      (win?.matchMedia?.("(display-mode: standalone)").matches ?? false) || (nav as Navigator & { standalone?: boolean } | undefined)?.standalone === true,
    serviceWorker: nav && "serviceWorker" in nav ? nav.serviceWorker : undefined,
    pushManager: !!win && "PushManager" in win,
    Notification: win && "Notification" in win ? win.Notification : undefined,
  };
}

/** iPhone, iPod, or an iPad (which reports itself as a Mac with a touch screen). */
export function isIOS(env: Pick<PushEnvironment, "userAgent" | "maxTouchPoints">): boolean {
  return /iPhone|iPad|iPod/.test(env.userAgent) || (/Macintosh/.test(env.userAgent) && env.maxTouchPoints > 1);
}

export function pushSupport(env: PushEnvironment = browserPushEnvironment()): PushSupport {
  if (!env.isSecureContext) return "insecure";
  if (isIOS(env) && !env.standalone) return "ios-install";
  if (!env.serviceWorker || !env.pushManager || !env.Notification) return "unsupported";
  if (env.Notification.permission === "denied") return "denied";
  return "available";
}

const SW_URL = "/sw.js";

/** Registers the service worker when push can work here; a no-op elsewhere. Safe to call on every load. */
export async function registerServiceWorker(env: PushEnvironment = browserPushEnvironment()): Promise<ServiceWorkerRegistration | undefined> {
  if (!env.isSecureContext || !env.serviceWorker) return undefined;
  try {
    return await env.serviceWorker.register(SW_URL, { scope: "/", updateViaCache: "none" });
  } catch {
    return undefined; // a blocked or failed registration leaves the app working without push
  }
}

/** This browser's current push endpoint, or undefined when it is not subscribed. */
export async function currentEndpoint(env: PushEnvironment = browserPushEnvironment()): Promise<string | undefined> {
  if (!env.serviceWorker || !env.pushManager) return undefined;
  const registration = await env.serviceWorker.getRegistration("/");
  const subscription = await registration?.pushManager.getSubscription();
  return subscription?.endpoint ?? undefined;
}

/** The server's endpoint hash: SHA-256 over the endpoint, first 16 bytes, hex. */
export async function endpointHash(endpoint: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(endpoint));
  return [...new Uint8Array(digest).slice(0, 16)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** base64url (the server's VAPID key) to the bytes pushManager.subscribe wants. */
export function base64UrlToBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const padded = base64url.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (base64url.length % 4)) % 4);
  const raw = atob(padded);
  const bytes = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

/** What enablePush needs from the server. */
export interface PushServer {
  pushKey: () => Promise<{ public_key: string }>;
  pushSubscribe: (subscription: PushSubscriptionJSON) => Promise<PushDevice>;
  pushUnsubscribe: (endpoint: string) => Promise<{ removed: boolean }>;
}

export type EnableOutcome = { ok: true; endpoint: string } | { ok: false; reason: "denied" | "dismissed" | "unsupported" };

/**
 * Turns notifications on for this browser. Call it straight from a click: the permission prompt
 * comes first, before any await, because Safari drops a prompt outside the user gesture.
 */
export async function enablePush(server: PushServer, env: PushEnvironment = browserPushEnvironment()): Promise<EnableOutcome> {
  if (pushSupport(env) !== "available" || !env.Notification || !env.serviceWorker) return { ok: false, reason: "unsupported" };
  const permission = await env.Notification.requestPermission();
  if (permission === "denied") return { ok: false, reason: "denied" };
  if (permission !== "granted") return { ok: false, reason: "dismissed" };
  const registration = (await registerServiceWorker(env)) ?? (await env.serviceWorker.getRegistration("/"));
  if (!registration) return { ok: false, reason: "unsupported" };
  const ready = registration.active ? registration : await waitUntilActive(registration);
  const { public_key: publicKey } = await server.pushKey();
  let subscription = await ready.pushManager.getSubscription();
  // A subscription made with another server key (a reinstalled crowsnest) cannot receive pushes.
  if (subscription && !sameKey(subscription, publicKey)) {
    await subscription.unsubscribe();
    subscription = null;
  }
  subscription ??= await ready.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: base64UrlToBytes(publicKey) });
  await server.pushSubscribe(subscription.toJSON());
  return { ok: true, endpoint: subscription.endpoint };
}

/** Turns notifications off for this browser: the server forgets it, and the browser drops it. */
export async function disablePush(server: PushServer, env: PushEnvironment = browserPushEnvironment()): Promise<void> {
  if (!env.serviceWorker) return;
  const registration = await env.serviceWorker.getRegistration("/");
  const subscription = await registration?.pushManager.getSubscription();
  if (!subscription) return;
  await server.pushUnsubscribe(subscription.endpoint);
  await subscription.unsubscribe();
}

function sameKey(subscription: PushSubscription, publicKey: string): boolean {
  const key = subscription.options?.applicationServerKey;
  if (!key) return true; // the browser does not say; assume it is ours
  const bytes = new Uint8Array(key);
  const want = base64UrlToBytes(publicKey);
  return bytes.length === want.length && bytes.every((byte, i) => byte === want[i]);
}

function waitUntilActive(registration: ServiceWorkerRegistration): Promise<ServiceWorkerRegistration> {
  return new Promise((resolve) => {
    const worker = registration.installing ?? registration.waiting;
    if (!worker) return resolve(registration);
    worker.addEventListener("statechange", () => {
      if (worker.state === "activated") resolve(registration);
    });
  });
}

/**
 * Follows the service worker's "open this" message (a tapped notification while a tab is open):
 * the SPA navigates without a reload. Returns an unsubscribe function.
 */
export function listenForNavigation(navigate: (path: string) => void): () => void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return () => {};
  const onMessage = (event: MessageEvent) => {
    const data = event.data as { type?: string; url?: string } | null;
    if (data?.type === "crowsnest:navigate" && typeof data.url === "string" && data.url.startsWith("/") && !data.url.startsWith("//")) {
      navigate(data.url);
    }
  };
  navigator.serviceWorker.addEventListener("message", onMessage);
  return () => navigator.serviceWorker.removeEventListener("message", onMessage);
}

/** A short name for a user agent, for the device list: browser on platform. */
export function describeAgent(userAgent: string): string {
  if (!userAgent) return "Unknown browser";
  const platform = /iPhone/.test(userAgent) ? "iPhone" : /iPad/.test(userAgent) ? "iPad" : /Android/.test(userAgent) ? "Android" : /Mac OS X|Macintosh/.test(userAgent) ? "Mac" : /Windows/.test(userAgent) ? "Windows" : /Linux/.test(userAgent) ? "Linux" : "";
  const browser = /Edg\//.test(userAgent) ? "Edge" : /Firefox\//.test(userAgent) ? "Firefox" : /Chrome\//.test(userAgent) ? "Chrome" : /Safari\//.test(userAgent) ? "Safari" : "Browser";
  return platform ? `${browser} on ${platform}` : browser;
}
