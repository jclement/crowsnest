// TerminalPanel: a live terminal with everything around it: a status line (connected,
// reconnecting with a countdown, or ended and why, with Reconnect), the xterm itself, and on
// phones and touch screens the key bar under it. The Terminal view uses it on the herdr TUI
// and fills the visible viewport; the Fleet detail uses it on one pane at a fixed height.
import { Minus, Plus, RotateCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { DESKTOP_QUERY, useMediaQuery } from "../../lib/media";
import { clampFontSize, keySequence, readFontSize, saveFontSize, withCtrl, type BarKey } from "../../lib/terminalKeys";
import type { TerminalState } from "../../lib/termSocket";
import { useViewportFit } from "../../lib/viewport";
import { Button, IconButton } from "../ui/Button";
import { useToast } from "../ui/toast-context";
import { KeyBar } from "./KeyBar";
import { XTerm, type XTermHandle } from "./XTerm";

interface TerminalPanelProps {
  /** API path of the terminal socket. */
  path: string;
  /** Accessible name of the terminal. */
  label: string;
  /** "viewport": fill down to the visible bottom (the Terminal view); "fixed": a share of the screen. */
  fill: "viewport" | "fixed";
  className?: string;
}

export function TerminalPanel({ path, label, fill, className }: TerminalPanelProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const handle = useRef<XTermHandle | null>(null);
  const toast = useToast();
  const [state, setState] = useState<TerminalState>({ status: "connecting", attempt: 0 });
  const [ctrl, setCtrl] = useState(false);
  const [fontSize, setFontSize] = useState(readFontSize);
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const coarse = useMediaQuery("(pointer: coarse)");
  const showKeyBar = coarse || !desktop;
  useViewportFit(rootRef, fill === "viewport");

  const changeFontSize = useCallback((size: number) => {
    const next = clampFontSize(size);
    setFontSize(next);
    saveFontSize(next);
  }, []);

  // Sticky Ctrl: the next typed character goes out as its control code, then Ctrl lets go.
  const transformInput = useCallback(
    (data: string) => {
      if (!ctrl) return data;
      setCtrl(false);
      return withCtrl(data) ?? data;
    },
    [ctrl],
  );

  const pressKey = (key: BarKey) => {
    setCtrl(false);
    handle.current?.send(keySequence(key, handle.current.applicationCursor()));
  };

  const paste = () => {
    const clipboard = navigator.clipboard as Clipboard | undefined;
    if (!clipboard?.readText) {
      toast.show({ tone: "warning", title: "Paste is not available here", description: "Long-press the terminal and choose Paste." });
      return;
    }
    clipboard.readText().then(
      (text) => text && handle.current?.paste(text),
      () => toast.show({ tone: "warning", title: "The browser blocked reading the clipboard", description: "Long-press the terminal and choose Paste." }),
    );
  };

  return (
    <div
      ref={rootRef}
      className={cn("flex min-h-0 flex-col", fill === "fixed" && "h-[60vh] max-h-[calc(100dvh-8rem)] min-h-72", className)}
    >
      <StatusLine state={state} onReconnect={() => handle.current?.reconnect()}>
        {!showKeyBar && (
          <>
            <IconButton icon={Minus} label="Smaller text" onClick={() => changeFontSize(fontSize - 1)} />
            <span className="w-6 text-center font-mono text-2xs tabular-nums text-muted" title="Font size">
              {fontSize}
            </span>
            <IconButton icon={Plus} label="Larger text" onClick={() => changeFontSize(fontSize + 1)} />
          </>
        )}
      </StatusLine>
      <XTerm
        path={path}
        label={label}
        fontSize={fontSize}
        onState={setState}
        transformInput={transformInput}
        handleRef={(value) => (handle.current = value)}
        className="flex-1"
      />
      {showKeyBar && (
        <KeyBar onKey={pressKey} ctrl={ctrl} onCtrlChange={setCtrl} onPaste={paste} fontSize={fontSize} onFontSize={changeFontSize} />
      )}
    </div>
  );
}

// ---- Status line -------------------------------------------------------------

function StatusLine({ state, onReconnect, children }: { state: TerminalState; onReconnect: () => void; children?: React.ReactNode }) {
  const seconds = useCountdown(state.status === "reconnecting" ? state.retryAt : undefined);
  const { text, tone } = describe(state, seconds);
  return (
    <div className="flex min-h-11 shrink-0 items-center gap-2 border-b border-border bg-surface-raised px-2 md:min-h-9">
      <span aria-hidden className={cn("h-2 w-2 shrink-0 rounded-full", toneClass[tone])} />
      <span role="status" className="min-w-0 flex-1 truncate text-xs text-body" title={state.reason}>
        {text}
      </span>
      {(state.status === "exited" || state.status === "reconnecting") && (
        <Button compact icon={RotateCw} onClick={onReconnect}>
          {state.status === "exited" ? "Reconnect" : "Retry now"}
        </Button>
      )}
      {children}
    </div>
  );
}

type Tone = "ok" | "wait" | "ended";

const toneClass: Record<Tone, string> = {
  ok: "bg-success",
  wait: "bg-warning animate-shimmer",
  ended: "bg-status-idle",
};

/** The status line's words for a state. */
function describe(state: TerminalState, seconds: number | undefined): { text: string; tone: Tone } {
  switch (state.status) {
    case "connecting":
      return { text: "Connecting…", tone: "wait" };
    case "open":
      return { text: "Connected", tone: "ok" };
    case "reconnecting":
      return {
        text: seconds === undefined || seconds <= 0 ? "Reconnecting…" : `Connection lost. Reconnecting in ${seconds} s`,
        tone: "wait",
      };
    case "exited":
      return { text: `Ended: ${state.reason ?? "closed"}`, tone: "ended" };
  }
}

/** Whole seconds until `at`, ticking; undefined when there is nothing to count down to. */
function useCountdown(at: number | undefined): number | undefined {
  const [left, setLeft] = useState<{ at: number; seconds: number } | null>(null);
  useEffect(() => {
    if (at === undefined) return;
    const tick = () => setLeft({ at, seconds: Math.max(0, Math.ceil((at - Date.now()) / 1000)) });
    const first = setTimeout(tick, 0);
    const timer = setInterval(tick, 500);
    return () => {
      clearTimeout(first);
      clearInterval(timer);
    };
  }, [at]);
  return at !== undefined && left?.at === at ? left.seconds : undefined;
}
