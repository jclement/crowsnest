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
