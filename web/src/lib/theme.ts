// Appearance: a theme (a named light/dark pair, src/themes/) and a mode (System → Light → Dark),
// chosen separately. Both are stored per browser in localStorage under the keys
// public/theme-init.js reads before first paint, which puts them on <html> as data-theme and the
// .dark class; this module keeps them in step afterwards, including when the OS flips while the
// mode is "system". A browser that never chose a theme gets the server's default (the "theme"
// setting), which the server writes into index.html as data-theme-default.

import { DEFAULT_THEME, findTheme, themes } from "../themes";

/** The mode. (Kept under its old name, ThemeChoice, for the callers that predate themes.) */
export type ThemeMode = "system" | "light" | "dark";
export type ThemeChoice = ThemeMode;

/** localStorage key for the mode. Unchanged since before themes, so old choices carry over. */
export const THEME_KEY = "crowsnest-theme";
/** localStorage key for the theme's id. */
export const THEME_NAME_KEY = "crowsnest-theme-name";
const order: ThemeMode[] = ["system", "light", "dark"];

export function readTheme(): ThemeMode {
  try {
    const saved = localStorage.getItem(THEME_KEY);
    return saved === "light" || saved === "dark" ? saved : "system";
  } catch {
    return "system";
  }
}

export function nextTheme(mode: ThemeMode): ThemeMode {
  return order[(order.indexOf(mode) + 1) % order.length] ?? "system";
}

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches;
}

/** True when the page is dark now (the mode, resolved against the OS). */
export function isDark(): boolean {
  return document.documentElement.classList.contains("dark");
}

/** Saves the mode and applies it to the document. */
export function applyTheme(mode: ThemeMode): void {
  try {
    if (mode === "system") localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, mode);
  } catch {
    /* storage blocked: still apply for this page */
  }
  const dark = mode === "dark" || (mode === "system" && systemPrefersDark());
  document.documentElement.classList.toggle("dark", dark);
  syncThemeColor();
}

/** The window event setTheme fires, so the header toggle follows a change made elsewhere. */
export const THEME_EVENT = "crowsnest-theme";

/** Applies a mode and tells the header toggle (used by the command palette). */
export function setTheme(mode: ThemeMode): void {
  applyTheme(mode);
  window.dispatchEvent(new CustomEvent<ThemeMode>(THEME_EVENT, { detail: mode }));
}

// ---- The theme (the named pair) ---------------------------------------------------------

/** The server's default for browsers that never chose, from index.html. */
function serverDefault(): string | undefined {
  return document.documentElement.dataset.themeDefault || undefined;
}

/** The chosen theme's id, or the server's default, or the house theme; always one that exists. */
export function readThemeName(): string {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem(THEME_NAME_KEY);
  } catch {
    /* storage blocked */
  }
  return findTheme(saved)?.id ?? findTheme(serverDefault())?.id ?? DEFAULT_THEME;
}

/** True when this browser has chosen a theme itself (rather than following the server's default). */
export function themeNameChosen(): boolean {
  try {
    return localStorage.getItem(THEME_NAME_KEY) !== null;
  } catch {
    return false;
  }
}

/** The event setThemeName fires, so every picker follows a change made elsewhere. */
export const THEME_NAME_EVENT = "crowsnest-theme-name";

/** Saves the theme for this browser, applies it, and tells the pickers. */
export function setThemeName(id: string): void {
  const theme = findTheme(id)?.id ?? DEFAULT_THEME;
  try {
    localStorage.setItem(THEME_NAME_KEY, theme);
  } catch {
    /* storage blocked: still apply for this page */
  }
  document.documentElement.dataset.theme = theme;
  syncThemeColor();
  window.dispatchEvent(new CustomEvent<string>(THEME_NAME_EVENT, { detail: theme }));
}

/** Forgets this browser's choice, so it follows the server's default again. */
export function clearThemeName(): void {
  try {
    localStorage.removeItem(THEME_NAME_KEY);
  } catch {
    /* storage blocked */
  }
  setThemeNameApplied(readThemeName());
}

function setThemeNameApplied(theme: string) {
  document.documentElement.dataset.theme = theme;
  syncThemeColor();
  window.dispatchEvent(new CustomEvent<string>(THEME_NAME_EVENT, { detail: theme }));
}

/** The theme after `id` in picker order (the T shortcut and the palette's "Next theme"). */
export function nextThemeName(id: string): string {
  const index = themes.findIndex((theme) => theme.id === id);
  return themes[(index + 1) % themes.length]?.id ?? DEFAULT_THEME;
}

/** Moves to the next theme. */
export function cycleThemeName(): string {
  const next = nextThemeName(readThemeName());
  setThemeName(next);
  return next;
}

/**
 * Called once as the app boots: corrects a data-theme that names no theme (one removed since this
 * browser chose it) and points the theme-color meta at the applied chrome.
 */
export function applyStartupTheme(): void {
  const theme = readThemeName();
  if (document.documentElement.dataset.theme !== theme) document.documentElement.dataset.theme = theme;
  syncThemeColor();
}

/**
 * Points <meta name="theme-color"> (the phone's status bar and the installed app's title bar)
 * at the chrome colour of the theme and mode now applied, so the bar and the header are one.
 */
export function syncThemeColor(): void {
  const meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
  if (!meta || typeof getComputedStyle !== "function") return;
  const chrome = getComputedStyle(document.documentElement).getPropertyValue("--color-chrome").trim();
  if (chrome) meta.content = chrome;
}
