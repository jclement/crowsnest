// The voice composer, speak mode and playback against a mocked MediaRecorder, getUserMedia, Audio
// and fetch. The keyboard tests are the hold-space bugs found by driving the real app (a quick
// Space locked the microphone on; Space in the composer only typed spaces; Space did nothing until
// a hidden toggle was on), written so each would have failed before the fix.
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithClient } from "../../test/render";
import { player } from "../../lib/voice/player";
import { onSayEvent, resetSpeakModeForTests, setSpeakMode, speakModeOn } from "../../lib/voice/speak";
import { NowPlaying, SpeakToggle } from "./SpeakMode";
import { VoiceComposer } from "./VoiceComposer";

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
  // A desktop: Space to talk is on from md up.
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: true, media: query, addEventListener() {}, removeEventListener() {} }));
  getUserMedia = vi.fn(async () => ({ getTracks: () => [{ stop: trackStop }] }));
  Object.defineProperty(navigator, "mediaDevices", { value: { getUserMedia }, configurable: true });
  Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
  play = vi.fn(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(play as () => Promise<void>);
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  URL.createObjectURL = vi.fn(() => "blob:silence");
  stubFetch();
});

afterEach(() => {
  act(() => {
    player.stop();
    resetSpeakModeForTests();
  });
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const mic = () => screen.getByTestId("mic-button");
const phase = () => mic().getAttribute("data-phase");

function renderComposer(onSent = vi.fn()) {
  renderWithClient(<VoiceComposer target="orchestrator" agentName="the orchestrator" state="idle" onSent={onSent} />);
  return onSent;
}

async function holdMic(holdMs: number) {
  fireEvent.pointerDown(mic(), { button: 0, pointerId: 1 });
  await waitFor(() => expect(phase()).toBe("recording"));
  now += holdMs;
  fireEvent.pointerUp(mic(), { button: 0, pointerId: 1 });
}

const space = (type: "keyDown" | "keyUp", target: EventTarget = document.body, init: KeyboardEventInit = {}) =>
  fireEvent[type](target, { code: "Space", key: " ", bubbles: true, ...init });

// ---- The microphone -------------------------------------------------------------------

describe("VoiceComposer: the microphone", () => {
  it("is always there, with the Space hint, and talk mode needs no switching on", () => {
    renderComposer();
    expect(mic()).toHaveAccessibleName("Hold to talk");
    expect(screen.getByTestId("space-hint")).toHaveTextContent(/Hold Space to talk/);
    expect(speakModeOn()).toBe(false);
  });

  it("hold to talk: records, uploads as webm, shows the text, and sends it with the voice flag", async () => {
    const onSent = renderComposer();
    await holdMic(1500);
    expect(await screen.findByTestId("voice-transcript")).toHaveTextContent("deploy the docs");
    expect(requests.find((request) => request.path === "/voice/stt")?.contentType).toBe("audio/webm;codecs=opus");
    expect(trackStop).toHaveBeenCalled(); // the microphone is released after each recording
    expect(requests.some((request) => request.path.endsWith("/prompt"))).toBe(false); // not before the review
    fireEvent.click(screen.getByRole("button", { name: "Send now" }));
    await waitFor(() => expect(onSent).toHaveBeenCalled());
    expect(requests.find((request) => request.path.endsWith("/prompt"))).toMatchObject({
      path: "/agents/orchestrator/prompt",
      body: { text: "deploy the docs", voice: true },
    });
  });

  it("Cancel during review sends nothing", async () => {
    renderComposer();
    await holdMic(1200);
    await screen.findByTestId("voice-transcript");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await new Promise((resolve) => setTimeout(resolve, 1700));
    expect(requests.some((request) => request.path.endsWith("/prompt"))).toBe(false);
  });

  it("a tap on the mic locks recording on; the next press stops and uploads", async () => {
    renderComposer();
    await holdMic(100);
    expect(mic()).toHaveAccessibleName(/locked/);
    expect(FakeRecorder.instances[0]?.state).toBe("recording");
    now += 3000;
    fireEvent.pointerDown(mic(), { button: 0, pointerId: 1 });
    expect(await screen.findByTestId("voice-transcript")).toHaveTextContent("deploy the docs");
  });

  it("shows elapsed time, warns in the last 30 s, and stops and sends at five minutes", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "setInterval", "clearTimeout", "clearInterval"] });
    try {
      renderComposer();
      fireEvent.pointerDown(mic(), { button: 0, pointerId: 1 });
      await vi.waitFor(() => expect(phase()).toBe("recording"));
      now += 61_000;
      act(() => vi.advanceTimersByTime(61_000));
      expect(screen.getByTestId("recording-elapsed")).toHaveTextContent("1:01 / 5:00");
      expect(screen.queryByTestId("recording-limit-warning")).not.toBeInTheDocument();
      now += 215_000; // 4:36
      act(() => vi.advanceTimersByTime(215_000));
      expect(screen.getByTestId("recording-limit-warning")).toHaveTextContent(/Stops and sends in 0:2\d/);
      now += 24_000; // 5:00: the limit timer fires and what was recorded is sent, not dropped
      act(() => vi.advanceTimersByTime(24_000));
    } finally {
      vi.useRealTimers();
    }
    expect(await screen.findByTestId("voice-transcript")).toHaveTextContent("deploy the docs");
    expect(requests.filter((request) => request.path === "/voice/stt")).toHaveLength(1);
  });

  it("explains a blocked microphone", async () => {
    getUserMedia.mockRejectedValueOnce(new DOMException("denied", "NotAllowedError"));
    renderComposer();
    fireEvent.pointerDown(mic(), { button: 0, pointerId: 1 });
    expect(await screen.findByRole("alert")).toHaveTextContent(/Microphone access is blocked/);
  });

  it("on a page that is not a secure context, pressing the mic points at tailscale serve", () => {
    Object.defineProperty(window, "isSecureContext", { value: false, configurable: true });
    renderComposer();
    fireEvent.pointerDown(mic(), { button: 0, pointerId: 1 });
    expect(screen.getByText(/tailscale serve/)).toBeInTheDocument();
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it("is not offered while the agent is at a dialog", () => {
    renderWithClient(<VoiceComposer target="orchestrator" agentName="the orchestrator" state="blocked" />);
    expect(screen.queryByTestId("mic-button")).not.toBeInTheDocument();
  });
});

// ---- Space to talk --------------------------------------------------------------------

describe("VoiceComposer: hold Space", () => {
  it("records while Space is held anywhere outside a text field and sends on release; repeats are ignored", async () => {
    renderComposer();
    space("keyDown");
    await waitFor(() => expect(phase()).toBe("recording"));
    for (let i = 0; i < 20; i++) space("keyDown", document.body, { repeat: true });
    expect(getUserMedia).toHaveBeenCalledTimes(1);
    now += 30_000;
    space("keyUp");
    expect(await screen.findByTestId("voice-transcript")).toBeInTheDocument();
  });

  it("a quick Space does not lock the microphone on and sends nothing", async () => {
    renderComposer();
    space("keyDown");
    await waitFor(() => expect(phase()).toBe("recording"));
    now += 100;
    space("keyUp");
    expect(await screen.findByRole("alert")).toHaveTextContent(/Hold the key down/);
    expect(FakeRecorder.instances[0]?.state).toBe("inactive");
    expect(requests.some((request) => request.path === "/voice/stt")).toBe(false);
  });

  it("a quick Space before the microphone starts sends nothing either", async () => {
    let resolve: (value: unknown) => void = () => {};
    getUserMedia.mockImplementationOnce(() => new Promise((r) => (resolve = r)));
    renderComposer();
    space("keyDown");
    now += 100;
    space("keyUp");
    resolve({ getTracks: () => [{ stop: trackStop }] });
    await screen.findByRole("alert");
    await new Promise((r) => setTimeout(r, 50));
    expect(requests.some((request) => request.path === "/voice/stt")).toBe(false);
  });

  it("never scrolls the page or presses a focused button", () => {
    renderComposer();
    const button = screen.getByRole("button", { name: "Send" });
    const down = new KeyboardEvent("keydown", { code: "Space", key: " ", bubbles: true, cancelable: true });
    const up = new KeyboardEvent("keyup", { code: "Space", key: " ", bubbles: true, cancelable: true });
    button.dispatchEvent(down);
    now += 1000;
    button.dispatchEvent(up);
    expect(down.defaultPrevented).toBe(true);
    expect(up.defaultPrevented).toBe(true);
  });

  it("in the composer Space types; Option/Ctrl+Space talks", async () => {
    renderComposer();
    const box = screen.getByRole("textbox");
    const plain = new KeyboardEvent("keydown", { code: "Space", key: " ", bubbles: true, cancelable: true });
    box.dispatchEvent(plain);
    expect(plain.defaultPrevented).toBe(false);
    expect(getUserMedia).not.toHaveBeenCalled();
    space("keyDown", box, { altKey: true });
    await waitFor(() => expect(phase()).toBe("recording"));
    now += 2000;
    space("keyUp", box); // the modifier may already be up
    expect(await screen.findByTestId("voice-transcript")).toBeInTheDocument();
  });

  it("leaving the window while holding ends the recording as a release", async () => {
    renderComposer();
    space("keyDown");
    await waitFor(() => expect(phase()).toBe("recording"));
    now += 2000;
    fireEvent.blur(window); // the keyup will never come
    expect(await screen.findByTestId("voice-transcript")).toBeInTheDocument();
    space("keyUp"); // a late keyup does nothing more
    expect(requests.filter((request) => request.path === "/voice/stt")).toHaveLength(1);
  });

  it("leaves Space alone inside a dialog", () => {
    renderComposer();
    const dialog = document.createElement("div");
    dialog.setAttribute("role", "dialog");
    const inside = document.createElement("button");
    dialog.appendChild(inside);
    document.body.appendChild(dialog);
    const down = new KeyboardEvent("keydown", { code: "Space", key: " ", bubbles: true, cancelable: true });
    inside.dispatchEvent(down);
    expect(down.defaultPrevented).toBe(false);
    dialog.remove();
  });
});

// ---- Speak mode and playback ----------------------------------------------------------

describe("speak mode", () => {
  const audioElement = () => document.querySelector<HTMLAudioElement>('audio[data-testid="speech-audio"]')!;

  it("the header toggle turns it on, remembers it, and say events then play in order with Stop", async () => {
    renderWithClient(
      <>
        <SpeakToggle />
        <NowPlaying />
      </>,
    );
    const toggle = screen.getByTestId("speak-toggle");
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    act(() => onSayEvent({ kind: "speak", text_id: "t_x", text: "Ignored." }));
    expect(player.getSnapshot().current).toBeNull();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    expect(localStorage.getItem("crowsnest-speak-mode")).toBe("on");
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

  it("plays at the saved rate, or an item's own (a preview)", () => {
    act(() => setSpeakMode(true));
    player.rate = 1.25;
    act(() => onSayEvent({ kind: "speak", text_id: "t_fast", text: "Quick." }));
    expect(audioElement().playbackRate).toBe(1.25);
    act(() => player.playNow({ textId: "t_preview", text: "Preview", rate: 0.9 }));
    expect(audioElement().playbackRate).toBe(0.9);
    player.rate = 1;
  });

  it("offers Play when autoplay is refused", async () => {
    act(() => setSpeakMode(true));
    play.mockImplementationOnce(() => Promise.reject(new DOMException("no gesture", "NotAllowedError")));
    renderWithClient(<NowPlaying />);
    act(() => onSayEvent({ kind: "speak", text_id: "t_y", text: "Hello." }));
    expect(await screen.findByRole("button", { name: "Play" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument());
  });
});
