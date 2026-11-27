// Reading theme files as data: the metadata in a theme's header comment, its token blocks, and
// the colour arithmetic (WCAG relative luminance and contrast, alpha blending) the theme tests
// and the Appearance gallery use. Pure: no DOM, so Vitest and the browser share it.

/** One theme as its file declares it. */
export interface ThemeFile {
  /** The file name without .css; also the value of <html data-theme>. */
  id: string;
  /** "@name" in the header comment. */
  name: string;
  /** "@description" in the header comment. */
  description: string;
  /** "@order": the picker's order (lower first); themes without one sort last, by id. */
  order: number;
  /** "@aaa": text is held to WCAG AAA (7:1) rather than AA (4.5:1). */
  aaa: boolean;
  /** Token name (with its leading --) to value, for each mode. */
  light: Record<string, string>;
  dark: Record<string, string>;
}

const headerField = (css: string, field: string) => css.match(new RegExp(`@${field}[ \\t]+([^\\n]+)`))?.[1]?.trim() ?? "";

/** The metadata only, for the picker: cheap, no token parsing. */
export function parseThemeMeta(id: string, css: string): Omit<ThemeFile, "light" | "dark"> {
  const order = Number.parseInt(headerField(css, "order"), 10);
  return {
    id,
    name: headerField(css, "name") || id,
    description: headerField(css, "description"),
    order: Number.isFinite(order) ? order : 1000,
    aaa: /@aaa\b/.test(css),
  };
}

function declarations(body: string): Record<string, string> {
  const tokens: Record<string, string> = {};
  for (const match of body.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/g)) tokens[match[1]!] = match[2]!.trim();
  return tokens;
}

/** Parses a theme file: its header and its light and dark blocks. */
export function parseThemeFile(id: string, css: string): ThemeFile {
  const withoutComments = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const block = (selector: string) => {
    const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const match = withoutComments.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`));
    return match ? declarations(match[1]!) : {};
  };
  return {
    ...parseThemeMeta(id, css),
    light: block(`[data-theme="${id}"]`),
    dark: block(`[data-theme="${id}"].dark`),
  };
}

/**
 * Tokens a theme may set but need not: the UI face (Phosphor draws everything in mono). A theme
 * that leaves one out keeps index.css's value.
 */
export const optionalThemeTokens = ["--font-sans"];

/**
 * The tokens a theme must define: the colours index.css declares in @theme, plus the terminal
 * palette (--term-*), which is not a Tailwind colour and is declared in index.css's :root block.
 */
export function themeTokenNames(indexCSS: string): string[] {
  const theme = indexCSS.match(/@theme\s*\{([\s\S]*?)\n\}/)?.[1] ?? "";
  const colours = Object.keys(declarations(theme)).filter((name) => name.startsWith("--color-"));
  const terminal = Object.keys(declarations(indexCSS)).filter((name) => name.startsWith("--term-"));
  return [...new Set([...colours, ...terminal])];
}

// ---- Colour arithmetic -------------------------------------------------------------------

/** An sRGB colour, channels 0-255, alpha 0-1. */
export interface RGBA {
  r: number;
  g: number;
  b: number;
  a: number;
}

/** Parses #rgb, #rrggbb, #rrggbbaa and rgb(r g b / a) (the forms the theme files use). */
export function parseColor(value: string): RGBA {
  const text = value.trim();
  const hex = text.match(/^#([0-9a-f]{3,8})$/i)?.[1];
  if (hex) {
    const full = hex.length === 3 ? [...hex].map((digit) => digit + digit).join("") : hex;
    const channel = (index: number) => Number.parseInt(full.slice(index, index + 2), 16);
    return { r: channel(0), g: channel(2), b: channel(4), a: full.length === 8 ? channel(6) / 255 : 1 };
  }
  const rgb = text.match(/^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:\s*[/,]\s*([\d.]+%?))?\s*\)$/i);
  if (rgb) {
    const alpha = rgb[4] === undefined ? 1 : rgb[4].endsWith("%") ? Number.parseFloat(rgb[4]) / 100 : Number.parseFloat(rgb[4]);
    return { r: Number(rgb[1]), g: Number(rgb[2]), b: Number(rgb[3]), a: alpha };
  }
  throw new Error(`not a colour this parser reads: ${value}`);
}

/** `top` at `share` (0-1) over an opaque `under`, as color-mix(in srgb) and Tailwind's /10 do. */
export function mix(top: RGBA, share: number, under: RGBA): RGBA {
  const blend = (a: number, b: number) => a * share + b * (1 - share);
  return { r: blend(top.r, under.r), g: blend(top.g, under.g), b: blend(top.b, under.b), a: 1 };
}

function luminance({ r, g, b }: RGBA): number {
  const linear = (channel: number) => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

/** The WCAG 2 contrast ratio of two opaque colours (1 to 21). */
export function contrast(a: RGBA, b: RGBA): number {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (light + 0.05) / (dark + 0.05);
}
