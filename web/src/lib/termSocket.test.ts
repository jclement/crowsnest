import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { backoffMs, parseControl, socketURL, TerminalSocket, type TerminalState } from "./termSocket";

/** A WebSocket stand-in the test drives by hand. */
class MockSocket {
  static instances: MockSocket[] = [];
  static latest(): MockSocket {
    const socket = MockSocket.instances.at(-1);
    if (!socket) throw new Error("no socket opened");
    return socket;
  }

  readonly url: string;
  binaryType = "blob";
  readyState = 0;
  sent: (string | Uint8Array)[] = [];
  closedWith: { code?: number; reason?: string } | null = null;
  onopen: (() => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;

  constructor(url: string) {
    this.url = url;
    MockSocket.instances.push(this);
  }

  send(data: string | Uint8Array) {
    this.sent.push(data);
  }

  close(code?: number, reason?: string) {
    this.closedWith = { code, reason };
    this.readyState = 3;
  }

  // ---- what the server does ----
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  receiveBytes(text: string) {
    this.onmessage?.({ data: new TextEncoder().encode(text).buffer } as MessageEvent);
  }
  receiveText(text: string) {
    this.onmessage?.({ data: text } as MessageEvent);
  }
  drop(code = 1006, reason = "") {
    this.readyState = 3;
    this.onclose?.({ code, reason } as CloseEvent);
  }
}

function setup(size = { cols: 80, rows: 24 }) {
  const states: TerminalState[] = [];
  const output: string[] = [];
  const events: string[] = [];
  const socket = new TerminalSocket({
    path: "/api/v1/term/tui",
    size: () => size,
    onData: (data) => output.push(new TextDecoder().decode(data)),
    onOpen: () => events.push("open"),
    onState: (state) => states.push(state),
    WebSocketImpl: MockSocket as unknown as typeof WebSocket,
  });
  return { socket, states, output, events, last: () => states.at(-1) };
}

const decode = (data: string | Uint8Array) => (typeof data === "string" ? data : new TextDecoder().decode(data));

describe("TerminalSocket", () => {
  beforeEach(() => {
    MockSocket.instances = [];
    vi.useFakeTimers();
  });
  afterEach(() => vi.useRealTimers());

  it("connects to the page's origin and sends a resize before anything else", () => {
    const { socket, events, last } = setup({ cols: 120, rows: 40 });
    socket.connect();
    const ws = MockSocket.latest();
    expect(ws.url).toBe(`ws://${window.location.host}/api/v1/term/tui`);
    expect(ws.binaryType).toBe("arraybuffer");
    expect(last()?.status).toBe("connecting");
    ws.open();
    expect(events).toEqual(["open"]);
    expect(ws.sent).toEqual([JSON.stringify({ type: "resize", cols: 120, rows: 40 })]);
    expect(last()?.status).toBe("open");
  });

  it("carries bytes both ways, input as binary frames", () => {
    const { socket, output } = setup();
    socket.connect();
    const ws = MockSocket.latest();
    socket.send("lost"); // not open yet: dropped
    ws.open();
    ws.receiveBytes("hello \x1b[1mworld");
    expect(output).toEqual(["hello \x1b[1mworld"]);
    socket.send("ls\r");
    const last = ws.sent.at(-1);
    expect(ArrayBuffer.isView(last)).toBe(true); // a binary frame, not text
    expect(decode(last!)).toBe("ls\r");
    expect(ws.sent.map(decode)).not.toContain("lost");
  });

  it("sends a resize only when the size changed", () => {
    const { socket } = setup();
    socket.connect();
    const ws = MockSocket.latest();
    ws.open();
    socket.resize({ cols: 80, rows: 24 }); // same as the opening resize
    socket.resize({ cols: 100, rows: 30 });
    socket.resize({ cols: 100, rows: 30 });
    expect(ws.sent).toEqual([
      JSON.stringify({ type: "resize", cols: 80, rows: 24 }),
      JSON.stringify({ type: "resize", cols: 100, rows: 30 }),
    ]);
  });

  it("reconnects after a drop with growing delays, and resets them after a good open", () => {
    const { socket, events, last } = setup();
    socket.connect();
    MockSocket.latest().open();
    MockSocket.latest().drop(1006);
    expect(last()).toMatchObject({ status: "reconnecting", attempt: 1 });
    expect(last()?.retryAt).toBe(Date.now() + 1000);

    vi.advanceTimersByTime(999);
    expect(MockSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(MockSocket.instances).toHaveLength(2);
    MockSocket.latest().drop(1006); // the retry fails too
    expect(last()).toMatchObject({ status: "reconnecting", attempt: 2 });
    vi.advanceTimersByTime(2000);
    expect(MockSocket.instances).toHaveLength(3);

    MockSocket.latest().open();
    expect(events).toEqual(["open", "open"]); // the screen is reset for the fresh redraw
    expect(last()).toMatchObject({ status: "open", attempt: 0 });
    MockSocket.latest().drop(1013, "the browser is not keeping up with the terminal");
    expect(last()).toMatchObject({ status: "reconnecting", attempt: 1, reason: "the browser is not keeping up with the terminal" });
  });

  it("does not reconnect after the terminal exits, and says why", () => {
    const { socket, last } = setup();
    socket.connect();
    const ws = MockSocket.latest();
    ws.open();
    ws.receiveText(JSON.stringify({ type: "exit", reason: "terminal attach taken over" }));
    ws.drop(1000);
    expect(last()).toEqual({ status: "exited", reason: "terminal attach taken over", attempt: 0 });
    vi.advanceTimersByTime(120_000);
    expect(MockSocket.instances).toHaveLength(1);

    socket.reconnect(); // the user's Reconnect button
    expect(MockSocket.instances).toHaveLength(2);
    MockSocket.latest().open();
    expect(last()?.status).toBe("open");
  });

  it("does not reconnect after a policy close (signed out)", () => {
    const { socket, last } = setup();
    socket.connect();
    MockSocket.latest().open();
    MockSocket.latest().drop(1008, "signed out");
    expect(last()).toEqual({ status: "exited", reason: "signed out", attempt: 0 });
    vi.advanceTimersByTime(60_000);
    expect(MockSocket.instances).toHaveLength(1);
  });

  it("retry now skips the wait", () => {
    const { socket } = setup();
    socket.connect();
    MockSocket.latest().drop(1006);
    socket.reconnect();
    expect(MockSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(5000); // the cancelled timer must not open a third
    expect(MockSocket.instances).toHaveLength(2);
  });

  it("dispose closes the socket and stops everything", () => {
    const { socket, states } = setup();
    socket.connect();
    const ws = MockSocket.latest();
    ws.open();
    const before = states.length;
    socket.dispose();
    expect(ws.closedWith).toEqual({ code: 1000, reason: "closed by the page" });
    ws.drop(1006); // late events from the old socket are ignored
    vi.advanceTimersByTime(60_000);
    expect(MockSocket.instances).toHaveLength(1);
    expect(states).toHaveLength(before);
  });
});

describe("helpers", () => {
  it("backs off 1, 2, 4, 8, 16, then 30 s", () => {
    expect([1, 2, 3, 4, 5, 6, 7, 20].map(backoffMs)).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000]);
  });

  it("builds ws and wss URLs from the page's scheme", () => {
    expect(socketURL("/api/v1/term/tui", { protocol: "http:", host: "localhost:2769" })).toBe("ws://localhost:2769/api/v1/term/tui");
    expect(socketURL("/x", { protocol: "https:", host: "crow.ts.net" })).toBe("wss://crow.ts.net/x");
  });

  it("parses control messages and ignores anything else", () => {
    expect(parseControl('{"type":"exit","reason":"detached"}')).toEqual({ type: "exit", reason: "detached" });
    expect(parseControl('{"type":"exit"}')).toEqual({ type: "exit", reason: undefined });
    expect(parseControl("not json")).toBeNull();
    expect(parseControl('{"reason":"x"}')).toBeNull();
  });
});
