// Three-state theme: system → light → dark. The choice is stored in localStorage under the same
// key public/theme-init.js reads before first paint; this module keeps <html class="dark"> in
// step afterwards, including when the OS flips while "system" is selected.

export type ThemeChoice = "system" | "light" | "dark";

export const THEME_KEY = "crowsnest-theme";
const order: ThemeChoice[] = ["system", "light", "dark"];

export function readTheme(): ThemeChoice {
  try {
    const saved = localStorage.getItem(THEME_KEY);
    return saved === "light" || saved === "dark" ? saved : "system";
  } catch {
    return "system";
  }
}

export function nextTheme(choice: ThemeChoice): ThemeChoice {
  return order[(order.indexOf(choice) + 1) % order.length] ?? "system";
}

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches;
}

/** Saves the choice and applies it to the document. */
export function applyTheme(choice: ThemeChoice): void {
  try {
    if (choice === "system") localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, choice);
  } catch {
    /* storage blocked: still apply for this page */
  }
  const dark = choice === "dark" || (choice === "system" && systemPrefersDark());
  document.documentElement.classList.toggle("dark", dark);
}

/** The window event setTheme fires, so the header toggle follows a change made elsewhere. */
export const THEME_EVENT = "crowsnest-theme";

/** Applies a choice and tells the header toggle (used by the command palette). */
export function setTheme(choice: ThemeChoice): void {
  applyTheme(choice);
  window.dispatchEvent(new CustomEvent<ThemeChoice>(THEME_EVENT, { detail: choice }));
}
