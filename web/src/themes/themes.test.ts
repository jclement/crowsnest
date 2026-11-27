// The theme files are data, and this is their schema check (DESIGN.md "Design language"):
//
// - completeness: every theme defines every token index.css declares in @theme (and every
//   terminal colour) in both modes, and nothing else but the optional tokens (the UI face);
// - contrast: every text token meets its floor on every surface it is drawn on, chips' tinted
//   backgrounds included; the high-contrast theme (@aaa) is held to WCAG AAA (7:1), the rest to
//   AA (4.5:1); state markers and the chrome's figures are checked too.
//
// It reads the CSS with node's fs rather than through Vite, so it checks exactly what ships.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { contrast, mix, optionalThemeTokens, parseColor, parseThemeFile, themeTokenNames } from "./tokens";

const dir = import.meta.dirname;
const files = readdirSync(dir).filter((name) => name.endsWith(".css"));
const indexCSS = readFileSync(join(dir, "..", "index.css"), "utf8");
const required = themeTokenNames(indexCSS);

const surfaces = ["surface", "surface-raised", "surface-sunken", "hover", "selected"];
const inks = ["heading", "body", "muted", "accent", "danger", "warning", "success"];
const tinted = ["accent", "danger", "warning", "success"];
const markers = ["status-working", "status-blocked", "status-stuck", "status-done", "status-held"];
const chromeInks = ["chrome-fg", "chrome-muted", "chrome-warning", "chrome-accent"];
const codeInks = ["code-keyword", "code-string", "code-number", "code-comment", "code-title", "code-attr"];

describe("theme files", () => {
  it("there are at least five, and the house theme is one", () => {
    expect(files.length).toBeGreaterThanOrEqual(5);
    expect(files).toContain("crowsnest.css");
  });

  it("index.css declares the token set", () => {
    expect(required.length).toBeGreaterThan(50);
    expect(required).toContain("--color-surface");
    expect(required).toContain("--term-bright-white");
  });
});

for (const file of files) {
  const theme = parseThemeFile(file.replace(/\.css$/, ""), readFileSync(join(dir, file), "utf8"));
  const floor = theme.aaa ? 7 : 4.5;

  describe(`${theme.id}`, () => {
    it("has a name and a description", () => {
      expect(theme.name).not.toBe("");
      expect(theme.description.length).toBeGreaterThan(20);
    });

    for (const mode of ["light", "dark"] as const) {
      const tokens = theme[mode];
      const color = (name: string) => {
        const value = tokens[`--color-${name}`] ?? tokens[`--${name}`];
        if (!value) throw new Error(`${theme.id} ${mode}: no ${name}`);
        return parseColor(value);
      };

      it(`${mode}: defines every token, and only those`, () => {
        expect(
          Object.keys(tokens)
            .filter((name) => !optionalThemeTokens.includes(name))
            .sort(),
        ).toEqual([...required].sort());
      });

      it(`${mode}: text meets ${floor}:1 on every surface and in its own chip`, () => {
        const failures: string[] = [];
        const check = (what: string, ratio: number, min = floor) => {
          if (ratio < min) failures.push(`${what}: ${ratio.toFixed(2)} < ${min}`);
        };
        for (const ink of inks) for (const surface of surfaces) check(`${ink} on ${surface}`, contrast(color(ink), color(surface)));
        for (const tone of tinted)
          for (const surface of ["surface", "surface-raised"]) {
            const chip = mix(color(tone), 0.1, color(surface));
            check(`${tone} in its chip on ${surface}`, contrast(color(tone), chip));
          }
        check("accent-fg on accent", contrast(color("accent-fg"), color("accent")));
        check("status-fg on status-blocked (the count badge)", contrast(color("status-fg"), color("status-blocked")));
        check("status-fg on danger (a destructive button)", contrast(color("status-fg"), color("danger")));
        for (const ink of chromeInks) for (const ground of ["chrome", "chrome-hover"]) check(`${ink} on ${ground}`, contrast(color(ink), color(ground)));
        for (const ink of codeInks) check(`${ink} on surface-sunken`, contrast(color(ink), color("surface-sunken")));
        check("terminal foreground on background", contrast(color("term-foreground"), color("term-background")));
        expect(failures).toEqual([]);
      });

      it(`${mode}: state markers and the focus ring stand out from the page (3:1, WCAG 1.4.11)`, () => {
        const failures: string[] = [];
        for (const marker of [...markers, "focus"])
          for (const surface of ["surface", "surface-raised", "selected"]) {
            const ratio = contrast(color(marker), color(surface));
            if (ratio < 3) failures.push(`${marker} on ${surface}: ${ratio.toFixed(2)}`);
          }
        if (theme.aaa) {
          const ratio = contrast(color("border"), color("surface"));
          if (ratio < 3) failures.push(`border on surface: ${ratio.toFixed(2)}`);
        }
        expect(failures).toEqual([]);
      });
    }
  });
}
