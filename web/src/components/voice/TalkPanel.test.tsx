// The talk panel and push-to-talk button against a mocked MediaRecorder, getUserMedia, Audio and
// fetch: hold to talk sends the transcript with the voice flag, a tap locks recording on, Cancel
// in review sends nothing, mic errors and an insecure page are explained, and `say` events play
// through the one audio element in order.
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithClient } from "../../test/render";
import { player } from "../../lib/voice/player";
import { onSayEvent, setTalkMode } from "../../lib/voice/talk";
import { TalkPanel } from "./TalkPanel";

// ---- Mocks --------------------------------------------------------------------------

class FakeRecorder {
  static isTypeSupported = (type: string) => type.startsWith("audio/webm");
  static instances: FakeRecorder[] = [];
  state: "inactive" | "recording" = "inactive";
  readonly mimeType: string;
  private listeners: Record<string, ((event: { data: Blob }) => void)[]> = {};
  constructor(_stream: MediaStream, options: { mimeType?: string }) {
    this.mimeType = options.mimeType ?? "";
    FakeRecorder.instances.push(this);
  }
  addEventListener(type: string, listener: (event: { data: Blob }) => void) {
    (this.listeners[type] ??= []).push(listener);
  }
  start() {
    this.state = "recording";
  }
  stop() {
    this.state = "inactive";
    this.listeners.dataavailable?.forEach((listener) => listener({ data: new Blob(["opus-bytes"], { type: this.mimeType }) }));
    this.listeners.stop?.forEach((listener) => listener({ data: new Blob() }));
  }
}

const trackStop = vi.fn();
let getUserMedia: ReturnType<typeof vi.fn>;
let now = 0;
let requests: { path: string; body: unknown; contentType: string | null }[] = [];
let play: ReturnType<typeof vi.fn>;

function stubFetch(transcript = "deploy the docs") {
  requests = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const path = url.replace(/^\/api\/v1/, "");
      const headers = new Headers(init?.headers);
      const body = init?.body instanceof Blob ? `blob:${init.body.type}` : init?.body ? JSON.parse(String(init.body)) : undefined;
      requests.push({ path, body, contentType: headers.get("Content-Type") });
      const answer = (data: unknown) => new Response(JSON.stringify({ data }), { status: 200, headers: { "Content-Type": "application/json" } });
      if (path === "/voice/talk") return answer({ talk: true, heartbeat_seconds: 15, max_recording_seconds: 60 });
      if (path === "/voice/stt") return answer({ text: transcript });
      if (path.endsWith("/prompt")) return answer({ pane_id: "w1:p1", status: "working", queued: false, started: true });
      return new Response("{}", { status: 599 });
    }),
  );
}

beforeEach(() => {
  now = 1000;
  vi.spyOn(performance, "now").mockImplementation(() => now);
  FakeRecorder.instances = [];
  vi.stubGlobal("MediaRecorder", FakeRecorder);
  getUserMedia = vi.fn(async () => ({ getTracks: () => [{ stop: trackStop }] }));
  Object.defineProperty(navigator, "mediaDevices", { value: { getUserMedia }, configurable: true });
  Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
  play = vi.fn(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(play as () => Promise<void>);
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  URL.createObjectURL = vi.fn(() => "blob:silence");
  stubFetch();
  act(() => setTalkMode(true));
});

afterEach(() => {
  act(() => setTalkMode(false));
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const talkButton = () => screen.getByRole("button", { name: /Hold to talk|Recording|Transcribing|Starting/ });

async function holdAndRelease(holdMs: number) {
  fireEvent.pointerDown(talkButton(), { button: 0, pointerId: 1 });
  await waitFor(() => expect(talkButton()).toHaveAttribute("data-phase", "recording"));
  now += holdMs;
  fireEvent.pointerUp(talkButton(), { button: 0, pointerId: 1 });
}

// ---- Tests --------------------------------------------------------------------------

describe("TalkPanel", () => {
  it("hold to talk: records, uploads as webm, shows the text, and sends it with the voice flag", async () => {
    const onSent = vi.fn();
    renderWithClient(<TalkPanel target="orchestrator" onSent={onSent} />);
    await holdAndRelease(1500);
    expect(await screen.findByTestId("voice-transcript")).toHaveTextContent("deploy the docs");
    const upload = requests.find((request) => request.path === "/voice/stt");
    expect(upload?.contentType).toBe("audio/webm;codecs=opus");
    expect(trackStop).toHaveBeenCalled(); // the microphone is released after each recording
    expect(requests.some((request) => request.path.endsWith("/prompt"))).toBe(false); // not before the review
    fireEvent.click(screen.getByRole("button", { name: "Send now" }));
    await waitFor(() => expect(onSent).toHaveBeenCalled());
    expect(requests.find((request) => request.path.endsWith("/prompt"))).toMatchObject({
      path: "/agents/orchestrator/prompt",
      body: { text: "deploy the docs", voice: true },
    });
    await waitFor(() => expect(talkButton()).toHaveAttribute("data-phase", "idle"));
  });

  it("sends by itself after the review delay", async () => {
    renderWithClient(<TalkPanel target="orchestrator" />);
    await holdAndRelease(1200);
    await screen.findByTestId("voice-transcript");
    await waitFor(() => expect(requests.some((request) => request.path.endsWith("/prompt"))).toBe(true), { timeout: 3000 });
  });

  it("Cancel during review sends nothing", async () => {
    renderWithClient(<TalkPanel target="orchestrator" />);
    await holdAndRelease(1200);
    await screen.findByTestId("voice-transcript");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await new Promise((resolve) => setTimeout(resolve, 1700));
    expect(requests.some((request) => request.path.endsWith("/prompt"))).toBe(false);
    expect(talkButton()).toHaveAttribute("data-phase", "idle");
  });

  it("a tap locks recording on; the next press stops and uploads", async () => {
    renderWithClient(<TalkPanel target="orchestrator" />);
    await holdAndRelease(100);
    expect(talkButton()).toHaveAccessibleName(/locked/);
    expect(FakeRecorder.instances[0]?.state).toBe("recording");
    now += 3000;
    fireEvent.pointerDown(talkButton(), { button: 0, pointerId: 1 });
    expect(await screen.findByTestId("voice-transcript")).toHaveTextContent("deploy the docs");
  });

  it("the space bar is push-to-talk too", async () => {
    renderWithClient(<TalkPanel target="orchestrator" />);
    fireEvent.keyDown(window, { code: "Space", key: " " });
    await waitFor(() => expect(talkButton()).toHaveAttribute("data-phase", "recording"));
    now += 1000;
    fireEvent.keyUp(window, { code: "Space", key: " " });
    expect(await screen.findByTestId("voice-transcript")).toBeInTheDocument();
  });

  it("explains a blocked microphone", async () => {
    getUserMedia.mockRejectedValueOnce(new DOMException("denied", "NotAllowedError"));
    renderWithClient(<TalkPanel target="orchestrator" />);
    fireEvent.pointerDown(talkButton(), { button: 0, pointerId: 1 });
    expect(await screen.findByRole("alert")).toHaveTextContent(/Microphone access is blocked/);
  });

  it("says nothing was heard for an empty transcript", async () => {
    stubFetch("");
    renderWithClient(<TalkPanel target="orchestrator" />);
    await holdAndRelease(1000);
    expect(await screen.findByRole("alert")).toHaveTextContent(/Didn't catch that/);
  });

  it("points at tailscale serve on a page that is not a secure context", () => {
    Object.defineProperty(window, "isSecureContext", { value: false, configurable: true });
    renderWithClient(<TalkPanel target="orchestrator" />);
    expect(screen.queryByRole("button", { name: /Hold to talk/ })).not.toBeInTheDocument();
    expect(screen.getByText(/tailscale serve/)).toBeInTheDocument();
  });

  it("is hidden while talk mode is off", () => {
    act(() => setTalkMode(false));
    renderWithClient(<TalkPanel target="orchestrator" />);
    expect(screen.queryByTestId("talk-panel")).not.toBeInTheDocument();
  });
});

describe("speech playback", () => {
  const audioElement = () => document.querySelector<HTMLAudioElement>('audio[data-testid="speech-audio"]')!;

  it("plays say events in order through one audio element, with Stop", async () => {
    renderWithClient(<TalkPanel target="orchestrator" />);
    act(() => {
      onSayEvent({ kind: "speak", text_id: "t_one", text: "First." });
      onSayEvent({ kind: "speak", text_id: "t_two", text: "Second." });
    });
    expect(audioElement().src).toContain("/api/v1/voice/tts?text_id=t_one");
    expect(await screen.findByTestId("player-bar")).toHaveTextContent("First. (+1 more)");
    act(() => {
      audioElement().dispatchEvent(new Event("ended"));
    });
    expect(audioElement().src).toContain("text_id=t_two");
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(screen.queryByTestId("player-bar")).not.toBeInTheDocument();
  });

  it("ignores say events while talk mode is off, and offers Play when autoplay is refused", async () => {
    act(() => setTalkMode(false));
    act(() => onSayEvent({ kind: "speak", text_id: "t_x", text: "Ignored." }));
    expect(player.getSnapshot().current).toBeNull();

    act(() => setTalkMode(true));
    play.mockImplementationOnce(() => Promise.reject(new DOMException("no gesture", "NotAllowedError")));
    renderWithClient(<TalkPanel target="orchestrator" />);
    act(() => onSayEvent({ kind: "speak", text_id: "t_y", text: "Hello." }));
    expect(await screen.findByRole("button", { name: "Play" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument());
  });

  it("tells the server when this tab turns talk mode on and off", async () => {
    await waitFor(() => expect(requests.some((request) => request.path === "/voice/talk")).toBe(true));
    const on = requests.find((request) => request.path === "/voice/talk")?.body as { client_id: string; on: boolean };
    expect(on.on).toBe(true);
    expect(on.client_id).toMatch(/^tab-[0-9a-f]{24}$/);
    act(() => setTalkMode(false));
    await waitFor(() => expect(requests.filter((request) => request.path === "/voice/talk").at(-1)?.body).toMatchObject({ on: false }));
  });
});
