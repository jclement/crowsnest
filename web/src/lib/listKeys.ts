// j and k in a list (R10 F-F10): on the Jobs board and in the Inbox, j moves focus to the next
// row and k to the previous one; Enter then opens it, as a click does. Rows say they are rows with
// `data-nav-row` (a JobCard, an Inbox row). The diff viewer keeps j and k for its files: it
// listens in the capture phase and prevents the default, so a key it took never reaches here.
import { useEffect } from "react";
import { focusContext, isMacPlatform } from "./hotkeys";

/** The paths whose rows j and k move through. */
const LIST_PATHS = ["/jobs", "/inbox"];

/** The row to focus after a j or k from current (-1: none focused), or null to do nothing. */
export function nextRowIndex(count: number, current: number, key: "j" | "k"): number | null {
  if (count === 0) return null;
  if (current < 0) return key === "j" ? 0 : count - 1;
  const next = current + (key === "j" ? 1 : -1);
  return next < 0 || next >= count ? null : next;
}

export function useListKeys(pathname: string) {
  useEffect(() => {
    if (!LIST_PATHS.includes(pathname)) return;
    const mac = isMacPlatform();
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || (event.key !== "j" && event.key !== "k")) return;
      if (event.metaKey || event.ctrlKey || event.altKey || event.repeat) return;
      const focus = focusContext(event.target, mac);
      if (focus.typing || focus.terminal || document.querySelector("[role=dialog]")) return;
      const rows = [...document.querySelectorAll<HTMLElement>("[data-nav-row]")].filter((row) => row.offsetParent !== null);
      const current = rows.findIndex((row) => row === document.activeElement || row.contains(document.activeElement));
      const index = nextRowIndex(rows.length, current, event.key);
      if (index === null) return;
      event.preventDefault();
      rows[index]?.focus();
      rows[index]?.scrollIntoView?.({ block: "nearest" });
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pathname]);
}
