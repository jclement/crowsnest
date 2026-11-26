// crowsnest's service worker: Web Push only (DESIGN.md 8.5).
//
// It shows a notification when the server pushes one, opens the item it links to when the
// notification is tapped, and resubscribes when the browser rotates the subscription.
//
// It deliberately has NO fetch handler and caches nothing: crowsnest has no offline mode, and a
// worker that served the app shell or the API from a cache would show a stale UI after every
// deploy. The server sends this file with Cache-Control: no-cache from the origin root, so its
// scope is the whole app and a new version is picked up on the next visit.
//
// The payload (internal/push/send.go) is JSON {title, body, tag, url, kind}: no secrets, one line.
"use strict";

const API = "/api/v1";

self.addEventListener("install", () => {
  // Nothing to precache; take over at once so pushes reach the newest code.
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

/** Parses a push payload; anything unreadable still shows a generic notice rather than nothing. */
function readPayload(data) {
  const fallback = { title: "crowsnest", body: "Something needs you.", tag: "crowsnest", url: "/", kind: "" };
  if (!data) return fallback;
  try {
    const parsed = data.json();
    return {
      title: typeof parsed.title === "string" && parsed.title ? parsed.title : fallback.title,
      body: typeof parsed.body === "string" ? parsed.body : "",
      tag: typeof parsed.tag === "string" && parsed.tag ? parsed.tag : fallback.tag,
      url: safePath(parsed.url),
      kind: typeof parsed.kind === "string" ? parsed.kind : "",
    };
  } catch {
    return fallback;
  }
}

/** Only a path on this origin may be opened: a pushed URL can never send the app elsewhere. */
function safePath(url) {
  if (typeof url !== "string" || !url.startsWith("/") || url.startsWith("//") || url.includes("\\")) return "/";
  return url;
}

/** Shows the notification for a payload. Exposed on self for the hermetic browser suite. */
function showNotice(notice) {
  return self.registration.showNotification(notice.title, {
    body: notice.body,
    tag: notice.tag,
    // A new notice about the same thing (same tag) replaces the old one and buzzes again.
    renotify: true,
    icon: "/icons/icon-192.png",
    badge: "/icons/icon-192.png",
    data: { url: notice.url, kind: notice.kind },
  });
}
self.crowsnestShowNotice = showNotice;

self.addEventListener("push", (event) => {
  event.waitUntil(showNotice(readPayload(event.data)));
});

/**
 * Opens url in the app: an open crowsnest tab is focused and told to navigate (the SPA routes it
 * without a reload); with none, a new window opens on it.
 */
async function openInApp(url) {
  const target = new URL(safePath(url), self.location.origin).href;
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  const sameOrigin = windows.filter((client) => new URL(client.url).origin === self.location.origin);
  const client = sameOrigin.find((candidate) => candidate.focused) ?? sameOrigin[0];
  if (client) {
    client.postMessage({ type: "crowsnest:navigate", url: safePath(url) });
    return client.focus();
  }
  return self.clients.openWindow(target);
}
self.crowsnestOpenInApp = openInApp;

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = event.notification.data && event.notification.data.url;
  event.waitUntil(openInApp(url));
});

/** Converts the server's base64url VAPID key for pushManager.subscribe. */
function keyBytes(base64url) {
  const padded = base64url.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (base64url.length % 4)) % 4);
  const raw = atob(padded);
  return Uint8Array.from(raw, (character) => character.charCodeAt(0));
}

function call(method, path, body) {
  return fetch(API + path, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-Crowsnest": "1", Accept: "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

// The browser rotated or dropped the subscription (an expiry, a push service change). Subscribe
// again with the server's key and swap the stored subscription. This needs the session cookie:
// if the login has lapsed the POST fails and the Settings sheet will show notifications as off.
self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil(
    (async () => {
      let subscription = event.newSubscription;
      if (!subscription) {
        const response = await fetch(API + "/push/key", { credentials: "same-origin", headers: { Accept: "application/json" } });
        if (!response.ok) return;
        const { data } = await response.json();
        subscription = await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(data.public_key) });
      }
      await call("POST", "/push/subscriptions", subscription.toJSON());
      if (event.oldSubscription && event.oldSubscription.endpoint !== subscription.endpoint) {
        await call("DELETE", "/push/subscriptions", { endpoint: event.oldSubscription.endpoint });
      }
    })(),
  );
});
