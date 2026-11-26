// The terminal font chain's helpers: the family order (Ghostty's), the wait before the terminal
// opens (bounded, so a font that never arrives cannot keep it closed), the font-load watcher, and
// the two spellings that make xterm.js measure again.
import { afterEach, describe, expect, it, vi } from "vitest";
import { FONT_WAIT_MS, loadTerminalFonts, otherSpelling, TERMINAL_FONT_FAMILY, watchFontLoads } from "./terminalFonts";

/** A stand-in for document.fonts whose load never settles unless told to. */
function fakeFonts() {
  const listeners = new Set<() => void>();
  const loads: string[] = [];
  let settle: () => void = () => {};
  const pending = new Promise<FontFace[]>((resolve) => {
    settle = () => resolve([]);
  });
  const fonts = {
    load: (font: string) => {
      loads.push(font);
      return pending;
    },
    addEventListener: (_type: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_type: string, listener: () => void) => listeners.delete(listener),
  };
  Object.defineProperty(document, "fonts", { value: fonts, configurable: true });
  return { loads, listeners, settle };
}

afterEach(() => {
  vi.useRealTimers();
  Reflect.deleteProperty(document, "fonts");
});

describe("terminal fonts", () => {
  it("lists Ghostty's chain in order, then the platform monospace", () => {
    expect(TERMINAL_FONT_FAMILY).toBe(
      '"JetBrainsMono Nerd Font Mono", "JetBrains Mono Variable", "Symbols Nerd Font Mono", "DejaVu Sans Mono", ' +
        '"Noto Sans Math", "Noto Sans Symbols 2", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
    );
  });

  it("loads the text faces, regular and bold, at the terminal's size", async () => {
    const fonts = fakeFonts();
    const ready = loadTerminalFonts(12);
    fonts.settle();
    await ready;
    expect(fonts.loads).toEqual([
      '12px "JetBrainsMono Nerd Font Mono"',
      'bold 12px "JetBrainsMono Nerd Font Mono"',
      '12px "JetBrains Mono Variable"',
      'bold 12px "JetBrains Mono Variable"',
    ]);
  });

  it("gives up waiting after the timeout rather than keeping the terminal closed", async () => {
    vi.useFakeTimers();
    fakeFonts(); // never settles
    let done = false;
    void loadTerminalFonts(12).then(() => (done = true));
    await vi.advanceTimersByTimeAsync(FONT_WAIT_MS - 1);
    expect(done).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(done).toBe(true);
  });

  it("calls back once a frame when fonts finish loading, until unsubscribed", () => {
    const fonts = fakeFonts();
    const frames: FrameRequestCallback[] = [];
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => frames.push(callback));
    vi.stubGlobal("cancelAnimationFrame", () => frames.splice(0));
    const onLoaded = vi.fn();
    const stop = watchFontLoads(onLoaded);
    for (const listener of fonts.listeners) listener();
    for (const listener of fonts.listeners) listener();
    expect(frames).toHaveLength(1); // two events in one frame: one callback
    frames[0]?.(0);
    expect(onLoaded).toHaveBeenCalledTimes(1);
    stop();
    expect(fonts.listeners.size).toBe(0);
  });

  it("alternates between two spellings of the same family", () => {
    const other = otherSpelling(TERMINAL_FONT_FAMILY);
    expect(other).not.toBe(TERMINAL_FONT_FAMILY);
    expect(other.trim()).toBe(TERMINAL_FONT_FAMILY);
    expect(otherSpelling(other)).toBe(TERMINAL_FONT_FAMILY);
  });
});
