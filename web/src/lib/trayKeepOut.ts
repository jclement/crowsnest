// Where the desktop tray may sit (DESIGN.md 8.7): never over the Chat composer, the terminal's
// last rows or the bottom nav. Each such element registers itself; the tray's bottom offset
// (the CSS variable --tray-bottom on <html>) is kept above the highest of them that reaches the
// lower right of the window. A composer keeps its whole height clear; a terminal its last rows
// (the bottom 40%, at most 240 px), so a tall terminal still leaves the tray room above them.
import { useEffect, type RefObject } from "react";

export type KeepOutKind = "composer" | "terminal";

const registered = new Map<Element, KeepOutKind>();
let observer: ResizeObserver | null = null;
let scheduled = false;

/** The gap kept between the tray and what it must not cover, px. */
export const TRAY_GAP = 12;

/** How far up from the window's bottom an element must be kept clear, px (0 if nothing). */
export function keepOutFor(rect: { top: number; bottom: number; height: number; right: number }, kind: KeepOutKind, viewport: { height: number; width: number }): number {
  if (rect.height <= 0 || rect.right < viewport.width / 2) return 0; // nothing there, or not on the tray's side
  const top = kind === "composer" ? rect.top : rect.bottom - Math.min(rect.height * 0.4, 240);
  return Math.max(0, viewport.height - top);
}

function update(): void {
  scheduled = false;
  const viewport = { height: window.innerHeight, width: window.innerWidth };
  let bottom = 0;
  for (const [element, kind] of registered) bottom = Math.max(bottom, keepOutFor(element.getBoundingClientRect(), kind, viewport));
  document.documentElement.style.setProperty("--tray-bottom", `${Math.round(bottom + TRAY_GAP)}px`);
}

function schedule(): void {
  if (scheduled) return;
  scheduled = true;
  requestAnimationFrame(update);
}

/** Keeps the desktop tray clear of the element in ref for as long as it is mounted. */
export function useTrayKeepOut(ref: RefObject<Element | null>, kind: KeepOutKind): void {
  useEffect(() => {
    const element = ref.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    if (!observer) {
      observer = new ResizeObserver(schedule);
      window.addEventListener("resize", schedule);
      window.addEventListener("scroll", schedule, true);
    }
    registered.set(element, kind);
    observer.observe(element);
    schedule();
    return () => {
      registered.delete(element);
      observer?.unobserve(element);
      schedule();
    };
  }, [ref, kind]);
}
