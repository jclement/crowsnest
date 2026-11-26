import { afterEach, describe, expect, it, vi } from "vitest";
import { clampFontSize, DEFAULT_FONT_SIZE, defaultFontSize, FONT_SIZE_KEY, keySequence, readFontSize, saveFontSize, TOUCH_FONT_SIZE, withCtrl } from "./terminalKeys";
import { fittedHeight } from "./viewport";

describe("keySequence", () => {
  it("sends what a keyboard sends", () => {
    expect(keySequence("esc")).toBe("\x1b");
    expect(keySequence("tab")).toBe("\t");
    expect(keySequence("enter")).toBe("\r");
    expect(["up", "down", "right", "left"].map((key) => keySequence(key as "up"))).toEqual(["\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D"]);
  });

  it("follows application cursor mode for arrows", () => {
    expect(keySequence("up", true)).toBe("\x1bOA");
    expect(keySequence("left", true)).toBe("\x1bOD");
    expect(keySequence("esc", true)).toBe("\x1b");
  });
});

describe("withCtrl", () => {
  it("turns letters and the C0 punctuation into control codes", () => {
    expect(withCtrl("c")).toBe("\x03");
    expect(withCtrl("C")).toBe("\x03");
    expect(withCtrl("a")).toBe("\x01");
    expect(withCtrl("z")).toBe("\x1a");
    expect(withCtrl("[")).toBe("\x1b");
    expect(withCtrl("\\")).toBe("\x1c");
    expect(withCtrl("@")).toBe("\x00");
    expect(withCtrl(" ")).toBe("\x00");
    expect(withCtrl("?")).toBe("\x7f");
  });

  it("leaves alone what has no control form", () => {
    expect(withCtrl("1")).toBeNull();
    expect(withCtrl("ab")).toBeNull();
    expect(withCtrl("")).toBeNull();
    expect(withCtrl("é")).toBeNull();
  });
});

describe("font size", () => {
  afterEach(() => localStorage.clear());

  /** Makes matchMedia answer as a desktop (fine pointer, wide) or a phone would. */
  const device = (kind: "desktop" | "phone") =>
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: kind === "desktop" ? query === "(min-width: 768px)" : query === "(pointer: coarse)",
      addEventListener: () => {},
      removeEventListener: () => {},
    }));

  it("defaults to Ghostty's 12 on a desktop and a size up on a phone", () => {
    device("desktop");
    expect(defaultFontSize()).toBe(DEFAULT_FONT_SIZE);
    expect(DEFAULT_FONT_SIZE).toBe(12);
    device("phone");
    expect(defaultFontSize()).toBe(TOUCH_FONT_SIZE);
    expect(readFontSize()).toBe(TOUCH_FONT_SIZE);
  });

  it("clamps to a readable range", () => {
    device("desktop");
    expect(clampFontSize(3)).toBe(9);
    expect(clampFontSize(99)).toBe(24);
    expect(clampFontSize(13.6)).toBe(14);
    expect(clampFontSize(Number.NaN)).toBe(DEFAULT_FONT_SIZE);
  });

  it("is remembered", () => {
    device("desktop");
    expect(readFontSize()).toBe(DEFAULT_FONT_SIZE);
    saveFontSize(17);
    expect(localStorage.getItem(FONT_SIZE_KEY)).toBe("17");
    expect(readFontSize()).toBe(17);
    localStorage.setItem(FONT_SIZE_KEY, "garbage");
    expect(readFontSize()).toBe(DEFAULT_FONT_SIZE);
  });
});

describe("fittedHeight", () => {
  it("ends at the visible bottom when the keyboard is up", () => {
    // 812 px phone, keyboard takes 336: the visual viewport ends at 476, above the nav at 750.
    expect(fittedHeight(96, 476, 750)).toBe(380);
  });

  it("ends at the container's bottom (the nav) when the keyboard is down", () => {
    expect(fittedHeight(96, 812, 750)).toBe(654);
  });

  it("never collapses", () => {
    expect(fittedHeight(700, 476, 750)).toBe(160);
  });
});
