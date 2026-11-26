// XTerm: an xterm.js terminal wired to a terminal WebSocket (lib/termSocket.ts). It fits its
// container (a ResizeObserver refits and tells the server the new size), follows the app's
// light and dark theme, opens links in a new tab without an opener, and reconnects by itself
// (the socket's rules). Fonts are the self-hosted JetBrains Mono: the CSP forbids font CDNs.
//
// The parent drives it through a handle (send, paste, focus, reconnect) so the mobile key bar
// can type into it, and may transform typed input (the key bar's sticky Ctrl).
import "@xterm/xterm/css/xterm.css";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal, type ITheme } from "@xterm/xterm";
import { useEffect, useRef } from "react";
import { cn } from "../../lib/cn";
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

const FONT_FAMILY = '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';
const SCROLLBACK_LINES = 5000;

export function XTerm({ path, label, fontSize, onState, transformInput, handleRef, className }: XTermProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const refitRef = useRef<() => void>(() => {});
  // Callbacks change on every render; the terminal is built once per path and reads them here.
  const latest = useRef({ onState, transformInput, handleRef });
  useEffect(() => {
    latest.current = { onState, transformInput, handleRef };
  });

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const terminal = new Terminal({
      fontFamily: FONT_FAMILY,
      fontSize,
      cursorBlink: true,
      scrollback: SCROLLBACK_LINES,
      theme: themeFromCSS(),
      allowProposedApi: false,
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.loadAddon(new WebLinksAddon((_event, uri) => window.open(uri, "_blank", "noopener,noreferrer")));
    terminal.open(container);
    terminalRef.current = terminal;

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
    refitRef.current = refit;
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

    // Measure again once the web font is in: xterm sizes its cells from the font on open.
    void document.fonts?.load(`${fontSize}px "JetBrains Mono Variable"`).then(() => {
      if (terminalRef.current !== terminal) return;
      terminal.options.fontFamily = FONT_FAMILY;
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
    });

    return () => {
      latest.current.handleRef?.(null);
      cancelAnimationFrame(frame);
      observer.disconnect();
      themeWatcher.disconnect();
      typed.dispose();
      binary.dispose();
      socket.dispose();
      terminal.dispose();
      terminalRef.current = null;
    };
    // fontSize is applied by the next effect; rebuilding the terminal for it would drop the session.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal || terminal.options.fontSize === fontSize) return;
    terminal.options.fontSize = fontSize;
    refitRef.current();
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
