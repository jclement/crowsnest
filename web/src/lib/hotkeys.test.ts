import { describe, expect, it } from "vitest";
import { chordLabel, focusContext, hotkeyFor, initialSequence, isMacPlatform, isPaletteChord, SEQUENCE_MS, type KeyInput } from "./hotkeys";

const key = (k: string, mods: Partial<KeyInput> = {}): KeyInput => ({ key: k, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...mods });
const page = { typing: false, terminal: false, mac: true };

describe("isPaletteChord", () => {
  it("is ⌘K on a Mac and Ctrl+K elsewhere", () => {
    expect(isPaletteChord(key("k", { metaKey: true }), true)).toBe(true);
    expect(isPaletteChord(key("K", { metaKey: true }), true)).toBe(true);
    expect(isPaletteChord(key("k", { ctrlKey: true }), true)).toBe(false);
    expect(isPaletteChord(key("k", { ctrlKey: true }), false)).toBe(true);
    expect(isPaletteChord(key("k", { metaKey: true }), false)).toBe(false);
  });

  it("is not a plain k, or k with other modifiers", () => {
    expect(isPaletteChord(key("k"), true)).toBe(false);
    expect(isPaletteChord(key("k", { metaKey: true, shiftKey: true }), true)).toBe(false);
    expect(isPaletteChord(key("k", { metaKey: true, altKey: true }), true)).toBe(false);
    expect(isPaletteChord(key("j", { metaKey: true }), true)).toBe(false);
  });
});

describe("hotkeyFor", () => {
  it("opens the palette from anywhere with the chord, even while typing", () => {
    expect(hotkeyFor(key("k", { metaKey: true }), page, initialSequence, 0).action).toEqual({ kind: "palette" });
    expect(hotkeyFor(key("k", { metaKey: true }), { ...page, typing: true }, initialSequence, 0).action).toEqual({ kind: "palette" });
    expect(hotkeyFor(key("k", { ctrlKey: true }), { ...page, mac: false, typing: true }, initialSequence, 0).action).toEqual({ kind: "palette" });
  });

  it("in a terminal takes ⌘K but leaves Ctrl+K to the shell", () => {
    const terminal = { typing: true, terminal: true, mac: true };
    expect(hotkeyFor(key("k", { metaKey: true }), terminal, initialSequence, 0).action).toEqual({ kind: "palette" });
    expect(hotkeyFor(key("k", { ctrlKey: true }), terminal, initialSequence, 0).action).toBeNull();
    expect(hotkeyFor(key("k", { ctrlKey: true }), { ...terminal, mac: false }, initialSequence, 0).action).toBeNull();
  });

  it("never takes a plain k", () => {
    expect(hotkeyFor(key("k"), page, initialSequence, 0).action).toBeNull();
    expect(hotkeyFor(key("k"), page, { gAt: 0 }, 10).action).toBeNull();
  });

  it("opens help with ?, but not while typing or in a terminal", () => {
    expect(hotkeyFor(key("?", { shiftKey: true }), page, initialSequence, 0).action).toEqual({ kind: "help" });
    expect(hotkeyFor(key("?", { shiftKey: true }), { ...page, typing: true }, initialSequence, 0).action).toBeNull();
    expect(hotkeyFor(key("?", { shiftKey: true }), { ...page, terminal: true, typing: true }, initialSequence, 0).action).toBeNull();
  });

  it("navigates with g then a view key, within the time limit", () => {
    const first = hotkeyFor(key("g"), page, initialSequence, 1000);
    expect(first.action).toBeNull();
    expect(first.state.gAt).toBe(1000);
    expect(hotkeyFor(key("i"), page, first.state, 1500).action).toEqual({ kind: "go", to: "/inbox" });
    expect(hotkeyFor(key("j"), page, first.state, 1500).action).toEqual({ kind: "go", to: "/" });
    expect(hotkeyFor(key("c"), page, first.state, 1500).action).toEqual({ kind: "go", to: "/chat" });
    expect(hotkeyFor(key("f"), page, first.state, 1500).action).toEqual({ kind: "go", to: "/fleet" });
    expect(hotkeyFor(key("t"), page, first.state, 1500).action).toEqual({ kind: "go", to: "/terminal" });
    expect(hotkeyFor(key("i"), page, first.state, 1000 + SEQUENCE_MS + 1).action).toBeNull();
  });

  it("drops the sequence on any other key, a modifier, or typing", () => {
    const g = hotkeyFor(key("g"), page, initialSequence, 0).state;
    expect(hotkeyFor(key("x"), page, g, 10).state).toEqual(initialSequence);
    expect(hotkeyFor(key("i", { metaKey: true }), page, g, 10).action).toBeNull();
    expect(hotkeyFor(key("i"), { ...page, typing: true }, g, 10).action).toBeNull();
    expect(hotkeyFor(key("g"), { ...page, typing: true }, initialSequence, 0).state).toEqual(initialSequence);
    expect(hotkeyFor(key("G", { shiftKey: true }), page, initialSequence, 0).state).toEqual(initialSequence);
  });

  it("ignores auto-repeat", () => {
    expect(hotkeyFor(key("?", { repeat: true }), page, initialSequence, 0).action).toBeNull();
  });
});

describe("focusContext", () => {
  it("sees fields, editable elements and terminals", () => {
    document.body.innerHTML = `
      <input id="i"><textarea id="t"></textarea><select id="s"></select><div id="e" contenteditable="true"><span id="es">x</span></div>
      <div data-terminal><textarea id="xt"></textarea></div><button id="b">b</button>`;
    const at = (id: string) => focusContext(document.getElementById(id), true);
    expect(at("i")).toEqual({ typing: true, terminal: false, mac: true });
    expect(at("t").typing).toBe(true);
    expect(at("s").typing).toBe(true);
    expect(at("es").typing).toBe(true);
    expect(at("xt")).toEqual({ typing: true, terminal: true, mac: true });
    expect(at("b")).toEqual({ typing: false, terminal: false, mac: true });
    expect(focusContext(null, false)).toEqual({ typing: false, terminal: false, mac: false });
  });
});

describe("platform", () => {
  it("knows a Mac and an iPhone from Linux", () => {
    expect(isMacPlatform({ userAgent: "x", platform: "MacIntel" })).toBe(true);
    expect(isMacPlatform({ userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0)", platform: "" })).toBe(true);
    expect(isMacPlatform({ userAgent: "x", platform: "Linux x86_64" })).toBe(false);
    expect(isMacPlatform({ userAgent: "x", userAgentData: { platform: "Windows" } })).toBe(false);
  });

  it("labels the chord", () => {
    expect(chordLabel(true)).toBe("⌘K");
    expect(chordLabel(false)).toBe("Ctrl K");
  });
});
