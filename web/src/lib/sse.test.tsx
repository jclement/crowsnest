import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConnectionBanner } from "../components/ConnectionBanner";
import { backoffDelay, MAX_BACKOFF_MS, useEventStream, type EventHandlers } from "./sse";

/** A controllable stand-in for the browser's EventSource. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  readonly url: string;
  readyState = FakeEventSource.CONNECTING;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, ((event: MessageEvent) => void)[]>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(name: string, listener: (event: MessageEvent) => void) {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), listener]);
  }
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
  emit(name: string, data: unknown) {
    this.readyState = FakeEventSource.OPEN;
    for (const listener of this.listeners.get(name) ?? []) listener(new MessageEvent(name, { data: JSON.stringify(data) }));
  }
  fail() {
    this.onerror?.();
  }
  static get latest(): FakeEventSource {
    const latest = FakeEventSource.instances.at(-1);
    if (!latest) throw new Error("no EventSource was opened");
    return latest;
  }
}

function Harness({ handlers }: { handlers: EventHandlers }) {
  const stream = useEventStream(handlers);
  return (
    <>
      <span data-testid="status">{stream.status}</span>
      <ConnectionBanner stream={stream} />
    </>
  );
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  // The session check before each reconnect.
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ data: { username: "jeff", features: {} } }), { status: 200 })),
  );
});

afterEach(() => {
  vi.useRealTimers();
});

describe("backoffDelay", () => {
  it("doubles from one second and stops at the cap", () => {
    expect([0, 1, 2, 3, 4, 5, 6, 10].map(backoffDelay)).toEqual([1000, 2000, 4000, 8000, 16000, MAX_BACKOFF_MS, MAX_BACKOFF_MS, MAX_BACKOFF_MS]);
  });
});

describe("useEventStream and the reconnect banner", () => {
  it("is open after hello and dispatches named events with parsed JSON", () => {
    const fleet = vi.fn();
    render(<Harness handlers={{ fleet }} />);
    expect(FakeEventSource.latest.url).toBe("/api/v1/events");
    expect(screen.getByTestId("status")).toHaveTextContent("connecting");
    act(() => FakeEventSource.latest.emit("hello", { version: "dev", session: "crowsnest" }));
    expect(screen.getByTestId("status")).toHaveTextContent("open");
    act(() => FakeEventSource.latest.emit("fleet", { changed: true }));
    expect(fleet).toHaveBeenCalledWith({ changed: true });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("listens for every documented event name, including job", () => {
    render(<Harness handlers={{}} />);
    const source = FakeEventSource.latest as unknown as { listeners: Map<string, unknown> };
    expect([...source.listeners.keys()]).toEqual(
      expect.arrayContaining(["hello", "heartbeat", "job", "fleet", "message", "question", "orchestrator", "poke", "say"]),
    );
  });

  it("shows the banner on failure and reconnects with growing backoff", async () => {
    vi.useFakeTimers();
    render(<Harness handlers={{}} />);
    act(() => FakeEventSource.latest.emit("hello", {}));

    act(() => FakeEventSource.latest.fail());
    expect(FakeEventSource.latest.readyState).toBe(FakeEventSource.CLOSED);
    expect(screen.getByRole("status")).toHaveTextContent("Connection lost. Reconnecting in 1 s");

    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(FakeEventSource.instances).toHaveLength(2); // reconnected after 1 s

    act(() => FakeEventSource.latest.fail());
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting in 2 s");
    expect(screen.getByRole("status")).toHaveTextContent("attempt 2");
    await act(async () => vi.advanceTimersByTimeAsync(1_999));
    expect(FakeEventSource.instances).toHaveLength(2); // not yet
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(FakeEventSource.instances).toHaveLength(3);

    act(() => FakeEventSource.latest.emit("hello", {}));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByTestId("status")).toHaveTextContent("open");

    // A success resets the backoff.
    act(() => FakeEventSource.latest.fail());
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting in 1 s");
  });

  it("checks the session before reconnecting and backs off while it fails", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("offline"));
    vi.stubGlobal("fetch", fetchMock);
    render(<Harness handlers={{}} />);
    act(() => FakeEventSource.latest.fail());
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(FakeEventSource.instances).toHaveLength(1); // no new stream while the server is unreachable
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting in 2 s");
  });

  it("retries at once on Retry now", async () => {
    const user = userEvent.setup();
    render(<Harness handlers={{}} />);
    act(() => FakeEventSource.latest.fail());
    await user.click(screen.getByRole("button", { name: "Retry now" }));
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
  });
});
