// The theme registry. Every src/themes/*.css file is a theme (DESIGN.md "Design language"): the
// eager glob below bundles its CSS into the app's stylesheet (so it is there before first
// paint), and the raw glob reads its header for the picker. Adding a theme is adding one file.
import { parseThemeMeta, type ThemeFile } from "./tokens";

import.meta.glob("./*.css", { eager: true });
const sources = import.meta.glob<string>("./*.css", { query: "?raw", import: "default", eager: true });

/** What the picker shows for one theme. */
export type ThemeInfo = Omit<ThemeFile, "light" | "dark">;

/** The house theme: used when nothing is chosen, or the choice names a theme that is gone. */
export const DEFAULT_THEME = "crowsnest";

/** Every theme, in picker order. */
export const themes: ThemeInfo[] = Object.entries(sources)
  .map(([path, css]) => parseThemeMeta(path.replace(/^\.\/(.*)\.css$/, "$1"), css))
  .sort((a, b) => a.order - b.order || a.id.localeCompare(b.id));

/** The theme with this id, if there is one. */
export function findTheme(id: string | null | undefined): ThemeInfo | undefined {
  return themes.find((theme) => theme.id === id);
}
