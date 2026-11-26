// useMediaQuery: follows a CSS media query, for the few layouts that differ structurally between
// mobile and desktop (the agent detail is a full-screen sheet on mobile, a side panel from md).
// Pure styling differences use Tailwind breakpoints instead.
import { useSyncExternalStore } from "react";

/** Tailwind's md breakpoint. */
export const DESKTOP_QUERY = "(min-width: 768px)";

export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      if (typeof window.matchMedia !== "function") return () => {};
      const media = window.matchMedia(query);
      media.addEventListener("change", onChange);
      return () => media.removeEventListener("change", onChange);
    },
    () => typeof window.matchMedia === "function" && window.matchMedia(query).matches,
    () => false,
  );
}

/** Tailwind's lg breakpoint: diffs go side by side from here. */
export const LARGE_QUERY = "(min-width: 1024px)";

/** A touch screen as the main pointer (a phone or tablet): the composer adds a camera button. */
export const COARSE_POINTER_QUERY = "(pointer: coarse)";
