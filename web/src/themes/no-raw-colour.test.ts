// No component may hard-code a colour (DESIGN.md "Design language"): colours come only from the
// theme tokens, so every theme and mode reaches every pixel. This walks src/ and fails on
//
// - a hex, rgb(), hsl() or oklch() colour outside the token files (src/themes/*.css, and
//   index.css, whose only colours are the sentinel the themes override);
// - a Tailwind palette colour (text-white, bg-red-500, …) or an arbitrary [#…] colour;
// - Tailwind's dark: variant, which keys off <html> and so would ignore a theme preview's own mode.
//
// Tests and fixtures are skipped: they hold data, not styles.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";

const src = join(import.meta.dirname, "..");

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return name === "fonts" ? [] : walk(path);
    return /\.(ts|tsx|css)$/.test(name) && !/\.test\.|ixture/.test(name) ? [path] : [];
  });
}

const rawColour = /#[0-9a-f]{3}(?:[0-9a-f]{3})?(?:[0-9a-f]{2})?\b|\b(?:rgba?|hsla?|oklch)\(/gi;
const paletteUtility =
  /\b(?:bg|text|border|fill|stroke|ring|outline|from|via|to|divide|decoration|shadow|caret|accent)-(?:white|black|(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d{2,3})\b|-\[#[0-9a-f]+\]/gi;
const darkVariant = /\bdark:[a-z[]/g;

function lineOf(text: string, index: number) {
  return text.slice(0, index).split("\n").length;
}

describe("no raw colours outside the token files", () => {
  const files = walk(src);

  it("finds the source", () => {
    expect(files.length).toBeGreaterThan(100);
  });

  it("components use tokens only", () => {
    const failures: string[] = [];
    for (const file of files) {
      const name = relative(src, file);
      const text = readFileSync(file, "utf8");
      // The token files, and the colour parser that reads them.
      const tokenFile = (name.startsWith("themes/") && name.endsWith(".css")) || name === "themes/tokens.ts";
      if (!tokenFile) {
        for (const match of text.matchAll(rawColour)) {
          if (name === "index.css" && match[0].toLowerCase() === "#f0f") continue; // the sentinel
          failures.push(`${name}:${lineOf(text, match.index)} raw colour ${match[0]}`);
        }
      }
      for (const match of text.matchAll(paletteUtility)) failures.push(`${name}:${lineOf(text, match.index)} palette colour ${match[0]}`);
      if (name.endsWith(".tsx")) for (const match of text.matchAll(darkVariant)) failures.push(`${name}:${lineOf(text, match.index)} dark: variant`);
    }
    expect(failures).toEqual([]);
  });
});
