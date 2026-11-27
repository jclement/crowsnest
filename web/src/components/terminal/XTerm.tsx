// XTerm: an xterm.js terminal wired to a terminal WebSocket (lib/termSocket.ts). It fits its
// container (a ResizeObserver refits and tells the server the new size), follows the app's
// light and dark theme, opens links in a new tab without an opener, and reconnects by itself
// (the socket's rules).
//
// Fonts: the terminal font chain of lib/terminalFonts.ts (Jeff's Ghostty chain, self-hosted:
// the CSP forbids font CDNs). The text faces are loaded before the terminal opens, since xterm
// sizes its cells from the font then, and it measures again whenever a font arrives later (a
// fallback chunk is fetched when its first glyph is drawn), so cells stay on the grid. Widths
// follow Unicode 11 (the unicode11 addon): emoji and CJK take two cells, as herdr, Claude Code
// and Ghostty count them; xterm's default Unicode 6 table counts emoji as one and the rest of
// the line would drift.
//
// The parent drives it through a handle (send, paste, focus, reconnect) so the mobile key bar
// can type into it, and may transform typed input (the key bar's sticky Ctrl).
import "@xterm/xterm/css/xterm.css";
import "../../fonts/terminal/terminal-fonts.css";
import { FitAddon } from "@xterm/addon-fit";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal, type ITheme } from "@xterm/xterm";
import { useEffect, useRef } from "react";
import { cn } from "../../lib/cn";
import { loadTerminalFonts, otherSpelling, TERMINAL_FONT_FAMILY, watchFontLoads } from "../../lib/terminalFonts";
import { TerminalSocket, type TerminalState } from "../../lib/termSocket";

/** What the parent can do to a mounted terminal. */
export interface XTermHandle {
  /** Sends bytes to the process as if typed. */
  send: (data: string) => void;
  /** Pastes text the way the terminal would (bracketed when the program asked for it). */
  paste: (text: string) => void;
  focus: () => void;
  reconnect: () => void;
  /** True when the program switched arrows to application mode (ESC O A). */
  applicationCursor: () => boolean;
  /** The selected text, or the visible screen's lines when nothing is selected (the phone's Copy). */
  copyText: () => string;
}

interface XTermProps {
  /** API path of the terminal socket. */
  path: string;
  /** Accessible name of the terminal region. */
  label: string;
  fontSize: number;
  onState?: (state: TerminalState) => void;
  /** Rewrites typed input before it is sent; the key bar uses it for Ctrl. */
  transformInput?: (data: string) => string;
  /** Receives the handle on mount and null on unmount. */
  handleRef?: (handle: XTermHandle | null) => void;
  className?: string;
}

/** The parent's callbacks, read through a ref: they change on every render. */
interface Callbacks {
  onState?: (state: TerminalState) => void;
  transformInput?: (data: string) => string;
  handleRef?: (handle: XTermHandle | null) => void;
}

/** A terminal opened in a container, with what the component needs to drive and close it. */
interface OpenedTerminal {
  terminal: Terminal;
  refit: () => void;
  dispose: () => void;
}

const SCROLLBACK_LINES = 5000;

export function XTerm({ path, label, fontSize, onState, transformInput, handleRef, className }: XTermProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const openedRef = useRef<OpenedTerminal | null>(null);
  // Callbacks change on every render; the terminal is built once per path and reads them here.
  const latest = useRef<Callbacks>({ onState, transformInput, handleRef });
  const fontSizeRef = useRef(fontSize);
  useEffect(() => {
    latest.current = { onState, transformInput, handleRef };
    fontSizeRef.current = fontSize;
  });

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let cancelled = false;
    // Open only once the text font is in (or has had its chance): see the file comment.
    void loadTerminalFonts(fontSizeRef.current).then(() => {
      if (cancelled) return;
      openedRef.current = openTerminal(container, path, fontSizeRef.current, latest);
    });
    return () => {
      cancelled = true;
      openedRef.current?.dispose();
      openedRef.current = null;
    };
  }, [path]);

  useEffect(() => {
    const opened = openedRef.current;
    if (!opened || opened.terminal.options.fontSize === fontSize) return;
    opened.terminal.options.fontSize = fontSize;
    opened.refit();
  }, [fontSize]);

  return (
    <div
      ref={containerRef}
      role="region"
      aria-label={label}
      data-terminal
      className={cn("min-h-0 min-w-0 overflow-hidden bg-surface-sunken p-1", className)}
    />
  );
}

/** Opens a terminal in container on the socket at path, and hands the parent its handle. */
function openTerminal(container: HTMLElement, path: string, fontSize: number, latest: { current: Callbacks }): OpenedTerminal {
  const terminal = new Terminal({
    fontFamily: TERMINAL_FONT_FAMILY,
    fontSize,
    cursorBlink: true,
    scrollback: SCROLLBACK_LINES,
    theme: themeFromCSS(),
    // The unicode11 addon switches widths through terminal.unicode, which xterm still calls proposed.
    allowProposedApi: true,
  });
  const fit = new FitAddon();
  terminal.loadAddon(fit);
  terminal.loadAddon(new Unicode11Addon());
  terminal.unicode.activeVersion = "11";
  terminal.loadAddon(new WebLinksAddon((_event, uri) => window.open(uri, "_blank", "noopener,noreferrer")));
  terminal.open(container);
  // ⌘K opens the command palette even from a terminal (lib/hotkeys.ts): xterm must not take it.
  // Ctrl+K is left alone, since that is the shell's kill-line.
  terminal.attachCustomKeyEventHandler((event) => !(event.metaKey && !event.ctrlKey && !event.altKey && event.key.toLowerCase() === "k"));

  const socket = new TerminalSocket({
    path,
    size: () => ({ cols: terminal.cols, rows: terminal.rows }),
    onData: (data) => terminal.write(data),
    onOpen: () => terminal.reset(),
    onState: (state) => latest.current.onState?.(state),
  });
  const refit = () => {
    if (container.clientWidth === 0 || container.clientHeight === 0) return; // hidden: keep the old size
    try {
      fit.fit();
    } catch {
      return; // not laid out yet
    }
    socket.resize({ cols: terminal.cols, rows: terminal.rows });
  };
  refit();

  const typed = terminal.onData((data) => socket.send(latest.current.transformInput?.(data) ?? data));
  const binary = terminal.onBinary((data) => socket.send(Uint8Array.from(data, (char) => char.charCodeAt(0) & 0xff)));
  socket.connect();

  let frame = 0;
  const observer = new ResizeObserver(() => {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(refit);
  });
  observer.observe(container);

  // A font arrived (the text face after a slow start, or a fallback chunk for a glyph just
  // drawn): measure the cell and the cached glyph widths again, then refit to the new cell.
  const stopWatchingFonts = watchFontLoads(() => {
    terminal.options.fontFamily = otherSpelling(terminal.options.fontFamily ?? TERMINAL_FONT_FAMILY);
    refit();
  });

  const themeWatcher = new MutationObserver(() => {
    terminal.options.theme = themeFromCSS();
  });
  themeWatcher.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });

  latest.current.handleRef?.({
    send: (data) => socket.send(data),
    paste: (text) => terminal.paste(text),
    focus: () => terminal.focus(),
    reconnect: () => socket.reconnect(),
    applicationCursor: () => terminal.modes.applicationCursorKeysMode,
    copyText: () => terminal.getSelection() || visibleText(terminal),
  });

  const dispose = () => {
    latest.current.handleRef?.(null);
    cancelAnimationFrame(frame);
    observer.disconnect();
    stopWatchingFonts();
    themeWatcher.disconnect();
    typed.dispose();
    binary.dispose();
    socket.dispose();
    terminal.dispose();
  };
  return { terminal, refit, dispose };
}

/** Builds the xterm theme from the app's CSS tokens, so it follows light and dark. */
function themeFromCSS(): ITheme {
  const style = getComputedStyle(document.documentElement);
  const token = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback;
  const dark = document.documentElement.classList.contains("dark");
  return {
    background: token("--color-surface-sunken", dark ? "#090d12" : "#f1f3f6"),
    foreground: token("--color-heading", dark ? "#e8edf3" : "#0f172a"),
    cursor: token("--color-accent", "#0b63c1"),
    cursorAccent: token("--color-surface-sunken", "#000000"),
    selectionBackground: dark ? "#2a4b6e" : "#b6d4f2",
    ...(dark ? darkPalette : lightPalette),
  };
}

// ANSI colours tuned for each background: xterm's defaults assume a black one, which leaves
// yellow and white unreadable on the light theme.
const lightPalette: ITheme = {
  black: "#1f2328",
  red: "#c22a2a",
  green: "#1a7f37",
  yellow: "#9a6700",
  blue: "#0b63c1",
  magenta: "#8b3fd1",
  cyan: "#1b7c83",
  white: "#6e7781",
  brightBlack: "#57606a",
  brightRed: "#d1242f",
  brightGreen: "#2da44e",
  brightYellow: "#bf8700",
  brightBlue: "#218bff",
  brightMagenta: "#a475f9",
  brightCyan: "#3192aa",
  brightWhite: "#8c959f",
};

const darkPalette: ITheme = {
  black: "#484f58",
  red: "#ff7b72",
  green: "#3fb950",
  yellow: "#d29922",
  blue: "#58a6ff",
  magenta: "#bc8cff",
  cyan: "#39c5cf",
  white: "#b1bac4",
  brightBlack: "#6e7681",
  brightRed: "#ffa198",
  brightGreen: "#56d364",
  brightYellow: "#e3b341",
  brightBlue: "#79c0ff",
  brightMagenta: "#d2a8ff",
  brightCyan: "#56d4dd",
  brightWhite: "#f0f6fc",
};

/** The lines on screen now, trailing spaces trimmed: what a long-press would struggle to select. */
function visibleText(terminal: Terminal): string {
  const buffer = terminal.buffer.active;
  const lines: string[] = [];
  for (let row = 0; row < terminal.rows; row++) {
    lines.push(buffer.getLine(buffer.viewportY + row)?.translateToString(true) ?? "");
  }
  return lines.join("\n").replace(/\n+$/, "");
}
