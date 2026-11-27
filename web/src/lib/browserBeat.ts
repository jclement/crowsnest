// Tells crowsnest this browser is in use, for "since you left" (DESIGN.md 22.3): a beat when the
// app opens, every minute while the page is visible, and whenever it becomes visible again. The
// server opens a new window when a beat follows more than ten minutes of silence. Nothing is sent
// but this browser's random id.
import { useEffect } from "react";
import { endpoints, fixturesEnabled } from "./api";
import { browserId } from "./attention";

const BEAT_MS = 60_000;

export function useBrowserBeat(): void {
  useEffect(() => {
    if (fixturesEnabled()) return;
    const browser = browserId();
    const beat = () => {
      if (document.visibilityState === "visible") void endpoints.nowBeat(browser).catch(() => undefined);
    };
    beat();
    const timer = window.setInterval(beat, BEAT_MS);
    document.addEventListener("visibilitychange", beat);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", beat);
    };
  }, []);
}
