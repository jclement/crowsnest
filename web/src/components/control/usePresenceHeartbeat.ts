// usePresenceHeartbeat: tells crowsnest whether this browser tab is visible and focused and how
// long ago it saw input (DESIGN.md 6.14). Every 30 s and on every visibility or focus change;
// a closing tab says it is gone. Nothing else is sent: no URLs, no keys, no content.
import { useEffect } from "react";
import { endpoints, fixturesEnabled } from "../../lib/api";

const BEAT_MS = 30_000;
const CLIENT_KEY = "crowsnest.presence.client";

function clientId(): string {
  try {
    let id = sessionStorage.getItem(CLIENT_KEY);
    if (!id) {
      id = Math.random().toString(36).slice(2, 12);
      sessionStorage.setItem(CLIENT_KEY, id);
    }
    return id;
  } catch {
    return "anonymous";
  }
}

export function usePresenceHeartbeat() {
  useEffect(() => {
    if (fixturesEnabled()) return;
    const client = clientId();
    let lastInput = Date.now();
    const onInput = () => {
      lastInput = Date.now();
    };
    const beat = () => {
      void endpoints
        .heartbeat({
          client,
          visible: document.visibilityState === "visible",
          focused: document.hasFocus(),
          idle_seconds: Math.round((Date.now() - lastInput) / 1000),
        })
        .catch(() => undefined);
    };
    const gone = () => {
      try {
        void fetch("/api/v1/presence/gone", {
          method: "POST",
          keepalive: true,
          credentials: "same-origin",
          headers: { "Content-Type": "application/json", "X-Crowsnest": "1" },
          body: JSON.stringify({ client }),
        });
      } catch {
        // best effort: the server forgets a silent browser after 75 s anyway
      }
    };
    const events = ["keydown", "pointerdown", "touchstart", "wheel"] as const;
    for (const name of events) window.addEventListener(name, onInput, { passive: true });
    document.addEventListener("visibilitychange", beat);
    window.addEventListener("focus", beat);
    window.addEventListener("blur", beat);
    window.addEventListener("pagehide", gone);
    beat();
    const timer = window.setInterval(beat, BEAT_MS);
    return () => {
      window.clearInterval(timer);
      for (const name of events) window.removeEventListener(name, onInput);
      document.removeEventListener("visibilitychange", beat);
      window.removeEventListener("focus", beat);
      window.removeEventListener("blur", beat);
      window.removeEventListener("pagehide", gone);
    };
  }, []);
}
