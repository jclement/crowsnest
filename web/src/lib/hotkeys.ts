// Global keyboard shortcuts, as a pure function of the key and where focus is, so every rule is
// tested without a DOM (hotkeys.test.ts). components/palette/PaletteProvider.tsx listens on the
// window in the capture phase and acts on what this returns.
//
// The rules (DESIGN.md 12, "Command palette"):
// - The palette chord is ⌘K on a Mac and Ctrl+K elsewhere. Ctrl+K on a Mac is left alone (it is
//   kill-line in every text field there).
// - In a terminal only ⌘K opens it: Ctrl+K is the shell's kill-line and must reach the pane. The
//   terminal is told to ignore ⌘K (XTerm.tsx) so xterm never swallows it.
// - `?` opens the shortcuts sheet, and `g` then j, c, i, f or t goes to Jobs, Chat, Inbox, Fleet or
//   Terminal, and `g` then n focuses the needs-you tray; none of these fire while typing in a field
//   or in a terminal, or with a modifier.
import { viewKeys, type ViewPath } from "./palette";

export interface KeyInput {
  key: string;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  repeat?: boolean;
}

export interface FocusContext {
  /** Focus is in an input, textarea, select or editable element (a terminal's textarea counts). */
  typing: boolean;
  /** Focus is inside a terminal. */
  terminal: boolean;
  mac: boolean;
}

export type HotkeyAction = { kind: "palette" } | { kind: "help" } | { kind: "go"; to: ViewPath } | { kind: "tray" };

/** The key after `g` that focuses the needs-you tray (DESIGN.md 8.7). */
export const TRAY_KEY = "n";

export interface SequenceState {
  /** When `g` was pressed (ms), or null when no sequence is pending. */
  gAt: number | null;
}

/** How long after `g` the second key still counts. */
export const SEQUENCE_MS = 1500;

export const initialSequence: SequenceState = { gAt: null };

/** Whether this is the palette chord for this platform, wherever focus is. */
export function isPaletteChord(input: KeyInput, mac: boolean): boolean {
  if (input.key.toLowerCase() !== "k" || input.altKey || input.shiftKey) return false;
  return mac ? input.metaKey && !input.ctrlKey : input.ctrlKey && !input.metaKey;
}

/**
 * What a key press does, and the sequence state after it. A null action means the key is not
 * ours: let it through untouched.
 */
export function hotkeyFor(input: KeyInput, focus: FocusContext, state: SequenceState, now: number): { action: HotkeyAction | null; state: SequenceState } {
  if (isPaletteChord(input, focus.mac)) {
    // In a terminal only ⌘K: Ctrl+K belongs to the shell.
    if (focus.terminal && !input.metaKey) return { action: null, state: initialSequence };
    return { action: { kind: "palette" }, state: initialSequence };
  }
  if (focus.typing || focus.terminal || input.metaKey || input.ctrlKey || input.altKey || input.repeat) {
    return { action: null, state: initialSequence };
  }
  if (input.key === "?") return { action: { kind: "help" }, state: initialSequence };
  const pending = state.gAt !== null && now - state.gAt <= SEQUENCE_MS;
  if (pending) {
    const view = viewKeys.find((entry) => entry.key === input.key.toLowerCase());
    if (view && !input.shiftKey) return { action: { kind: "go", to: view.to }, state: initialSequence };
    if (input.key.toLowerCase() === TRAY_KEY && !input.shiftKey) return { action: { kind: "tray" }, state: initialSequence };
  }
  if (input.key === "g" && !input.shiftKey) return { action: null, state: { gAt: now } };
  return { action: null, state: initialSequence };
}

/** Where focus is, from the event's target. */
export function focusContext(target: EventTarget | null, mac: boolean): FocusContext {
  const element = target instanceof Element ? target : null;
  const typing =
    !!element &&
    (element instanceof HTMLElement && element.isContentEditable
      ? true
      : !!element.closest("input, textarea, select, [contenteditable=''], [contenteditable='true']"));
  const terminal = !!element?.closest("[data-terminal], .xterm");
  return { typing, terminal, mac };
}

/** True on macOS and iOS (⌘ is the command key there). */
export function isMacPlatform(nav: Pick<Navigator, "userAgent"> & { platform?: string; userAgentData?: { platform?: string } } = navigator): boolean {
  const platform = nav.userAgentData?.platform || nav.platform || nav.userAgent;
  return /mac|iphone|ipad|ipod/i.test(platform);
}

/** The chord as a user reads it on this platform. */
export function chordLabel(mac: boolean): string {
  return mac ? "⌘K" : "Ctrl K";
}
