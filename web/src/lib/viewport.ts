// useViewportFit: sizes an element from its top edge down to the bottom of what the user can
// actually see. On a phone the on-screen keyboard shrinks the *visual* viewport but not the
// layout viewport, so a terminal sized with CSS alone ends up with its bottom rows, the
// cursor line included, behind the keyboard. visualViewport says where the visible area ends.
import { useEffect, type RefObject } from "react";

/** The smallest height worth giving a terminal, in CSS pixels. */
const MIN_HEIGHT_PX = 160;

/**
 * The height that makes an element starting at `top` end at the visible bottom: the lower of
 * the visual viewport's bottom edge and `limit` (its scroll container's bottom, which is where
 * the bottom nav begins), never below MIN_HEIGHT_PX.
 */
export function fittedHeight(top: number, visualBottom: number, limit: number): number {
  return Math.max(MIN_HEIGHT_PX, Math.floor(Math.min(visualBottom, limit) - top));
}

/** Keeps ref's height fitted while mounted and enabled. */
export function useViewportFit(ref: RefObject<HTMLElement | null>, enabled: boolean): void {
  useEffect(() => {
    const element = ref.current;
    if (!enabled || !element) return;
    const viewport = window.visualViewport;
    const container = element.closest("main") ?? document.documentElement;
    let frame = 0;
    const update = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const visualBottom = viewport ? viewport.offsetTop + viewport.height : window.innerHeight;
        const limit = container.getBoundingClientRect().bottom;
        element.style.height = `${fittedHeight(element.getBoundingClientRect().top, visualBottom, limit)}px`;
      });
    };
    update();
    viewport?.addEventListener("resize", update);
    viewport?.addEventListener("scroll", update);
    window.addEventListener("resize", update);
    const observer = new ResizeObserver(update);
    observer.observe(container);
    return () => {
      cancelAnimationFrame(frame);
      viewport?.removeEventListener("resize", update);
      viewport?.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
      observer.disconnect();
      element.style.height = "";
    };
  }, [ref, enabled]);
}
