// TerminalSocket: the browser side of a terminal WebSocket (DESIGN.md section 10), kept free of
// React and xterm.js so the protocol and the reconnect rules are tested directly
// (termSocket.test.ts) with a mock WebSocket.
//
// The protocol: on open, send {"type":"resize"} first (the server starts the process on it);
// then binary frames are terminal bytes both ways; a text frame {"type":"exit","reason"} says
// the process ended. The reconnect rules:
//
//   - a dropped connection (network, server restart, "not keeping up") reconnects after 1, 2,
//     4 … 30 s, and the terminal is reset on every open so the server's fresh redraw lands on a
//     clean screen;
//   - an `exit` (detached, the shell exited, another browser took the pane over) does not: an
//     automatic reconnect would steal the pane straight back from the other browser, or reopen
//     something the user closed on purpose. The view offers "Reconnect" instead;
//   - a policy close (signed out, 1008) does not either: the app's auth handling takes over.

/** Where a terminal socket is in its life. */
export type TerminalStatus = "connecting" | "open" | "reconnecting" | "exited";

export interface TerminalState {
  status: TerminalStatus;
  /** Why the terminal ended (status "exited"), or why the last connection dropped. */
  reason?: string;
  /** When status is "reconnecting": when the next attempt starts (ms since the epoch). */
  retryAt?: number;
  /** Consecutive failed connections; resets on a successful open. */
  attempt: number;
}

export interface TerminalSize {
  cols: number;
  rows: number;
}

export interface TerminalSocketOptions {
  /** API path of the socket, e.g. /api/v1/term/tui. */
  path: string;
  /** The terminal's size now; sent as the first message of every connection. */
  size: () => TerminalSize;
  /** Terminal output. */
  onData: (data: Uint8Array) => void;
  /** Called on every successful open, before any output: the place to reset the screen. */
  onOpen?: () => void;
  onState: (state: TerminalState) => void;
  /** For tests. */
  WebSocketImpl?: typeof WebSocket;
}

/** Reconnect delays: 1, 2, 4, 8, 16, then 30 s. */
export function backoffMs(attempt: number): number {
  return Math.min(30_000, 1000 * 2 ** Math.max(0, attempt - 1));
}

/** Close codes after which reconnecting would be wrong. */
const POLICY_VIOLATION = 1008;

/** The ws:// or wss:// URL for an API path on this page's origin. */
export function socketURL(path: string, location: Pick<Location, "protocol" | "host"> = window.location): string {
  return `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}${path}`;
}

/** Parses a server control message; null for anything else. */
export function parseControl(text: string): { type: string; reason?: string } | null {
  try {
    const message = JSON.parse(text) as { type?: unknown; reason?: unknown };
    if (typeof message.type !== "string") return null;
    return { type: message.type, reason: typeof message.reason === "string" ? message.reason : undefined };
  } catch {
    return null;
  }
}

export class TerminalSocket {
  private readonly options: TerminalSocketOptions;
  private readonly encoder = new TextEncoder();
  private socket: WebSocket | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private attempt = 0;
  private exitReason: string | undefined;
  private lastSize: TerminalSize | null = null;
  private disposed = false;

  constructor(options: TerminalSocketOptions) {
    this.options = options;
  }

  /** Opens the connection. Call once; later connections are this class's business. */
  connect(): void {
    if (this.disposed) return;
    this.clearTimer();
    this.exitReason = undefined;
    const Impl = this.options.WebSocketImpl ?? WebSocket;
    const socket = new Impl(socketURL(this.options.path));
    socket.binaryType = "arraybuffer";
    this.socket = socket;
    this.emit({ status: this.attempt > 0 ? "reconnecting" : "connecting", attempt: this.attempt });
    socket.onopen = () => this.opened(socket);
    socket.onmessage = (event: MessageEvent) => this.message(socket, event);
    socket.onclose = (event: CloseEvent) => this.closed(socket, event);
  }

  /** Sends keystrokes (xterm's onData strings) or raw bytes. Dropped while not connected. */
  send(data: string | Uint8Array<ArrayBuffer>): void {
    if (this.socket?.readyState !== 1) return;
    this.socket.send(typeof data === "string" ? this.encoder.encode(data) : data);
  }

  /** Tells the server the terminal's size, when it changed. */
  resize(size: TerminalSize): void {
    if (this.lastSize && this.lastSize.cols === size.cols && this.lastSize.rows === size.rows) return;
    if (this.socket?.readyState !== 1) return;
    this.lastSize = size;
    this.socket.send(JSON.stringify({ type: "resize", cols: size.cols, rows: size.rows }));
  }

  /** Reconnects now: after an exit, or to skip a backoff wait. */
  reconnect(): void {
    if (this.disposed) return;
    this.detach();
    this.connect();
  }

  /** Closes the connection for good. */
  dispose(): void {
    this.disposed = true;
    this.clearTimer();
    this.detach();
  }

  // ---- Events ----

  private opened(socket: WebSocket): void {
    if (socket !== this.socket) return;
    this.attempt = 0;
    this.lastSize = null;
    this.options.onOpen?.();
    this.resize(this.options.size());
    this.emit({ status: "open", attempt: 0 });
  }

  private message(socket: WebSocket, event: MessageEvent): void {
    if (socket !== this.socket) return;
    if (typeof event.data === "string") {
      const control = parseControl(event.data);
      if (control?.type === "exit") this.exitReason = control.reason ?? "ended";
      return;
    }
    // binaryType is "arraybuffer". Checked by shape rather than instanceof, which fails across
    // realms (an ArrayBuffer from another window or a test environment).
    const buffer = event.data as ArrayBuffer;
    if (typeof buffer?.byteLength === "number") this.options.onData(new Uint8Array(buffer));
  }

  private closed(socket: WebSocket, event: CloseEvent): void {
    if (socket !== this.socket || this.disposed) return;
    this.socket = null;
    if (this.exitReason !== undefined) {
      this.emit({ status: "exited", reason: this.exitReason, attempt: 0 });
      return;
    }
    if (event.code === POLICY_VIOLATION) {
      this.emit({ status: "exited", reason: event.reason || "closed by the server", attempt: 0 });
      return;
    }
    this.attempt += 1;
    const delay = backoffMs(this.attempt);
    this.emit({ status: "reconnecting", reason: event.reason || undefined, retryAt: Date.now() + delay, attempt: this.attempt });
    this.timer = setTimeout(() => this.connect(), delay);
  }

  // ---- Helpers ----

  private detach(): void {
    const socket = this.socket;
    this.socket = null;
    if (socket) {
      socket.onopen = socket.onmessage = socket.onclose = null;
      try {
        socket.close(1000, "closed by the page");
      } catch {
        /* already closed */
      }
    }
  }

  private clearTimer(): void {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
  }

  private emit(state: TerminalState): void {
    if (!this.disposed) this.options.onState(state);
  }
}
