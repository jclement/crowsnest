// What the mobile key bar sends (components/terminal/KeyBar.tsx), and the sticky Ctrl modifier.
// A phone keyboard has no Esc, Tab, Ctrl or arrows, and those are exactly the keys a terminal
// program needs, so the bar supplies them as the byte sequences a real keyboard would.

/** The keys on the bar that send something. */
export type BarKey = "esc" | "tab" | "enter" | "up" | "down" | "left" | "right";

const arrowFinal: Record<"up" | "down" | "left" | "right", string> = { up: "A", down: "B", right: "C", left: "D" };

/**
 * The bytes for a bar key. Arrows follow the terminal's cursor-key mode: programs that switch
 * to application mode (vim, less, most TUIs) expect ESC O A rather than ESC [ A.
 */
export function keySequence(key: BarKey, applicationCursor = false): string {
  switch (key) {
    case "esc":
      return "\x1b";
    case "tab":
      return "\t";
    case "enter":
      return "\r";
    default:
      return (applicationCursor ? "\x1bO" : "\x1b[") + arrowFinal[key];
  }
}

/**
 * Applies Ctrl to one typed character: a letter or one of @ [ \ ] ^ _ (and space, which is
 * Ctrl+@) becomes its control code, as a keyboard's Ctrl would send it. Returns null when Ctrl
 * means nothing for this input (several characters, or a character with no control form), so
 * the caller sends it unchanged.
 */
export function withCtrl(data: string): string | null {
  if (data.length !== 1) return null;
  const code = data.toUpperCase().charCodeAt(0);
  if (data === " ") return "\x00";
  if (code >= 0x40 && code <= 0x5f) return String.fromCharCode(code & 0x1f);
  if (data === "?") return "\x7f";
  return null;
}

// ---- Font size ---------------------------------------------------------------

export const FONT_SIZE_KEY = "crowsnest-terminal-font-size";
export const MIN_FONT_SIZE = 9;
export const MAX_FONT_SIZE = 24;
export const DEFAULT_FONT_SIZE = 13;

export function clampFontSize(size: number): number {
  if (!Number.isFinite(size)) return DEFAULT_FONT_SIZE;
  return Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, Math.round(size)));
}

/** The saved terminal font size, or the default. */
export function readFontSize(): number {
  try {
    const saved = localStorage.getItem(FONT_SIZE_KEY);
    return saved === null ? DEFAULT_FONT_SIZE : clampFontSize(Number(saved));
  } catch {
    return DEFAULT_FONT_SIZE;
  }
}

export function saveFontSize(size: number): void {
  try {
    localStorage.setItem(FONT_SIZE_KEY, String(clampFontSize(size)));
  } catch {
    /* storage blocked: the size lasts for this page */
  }
}
