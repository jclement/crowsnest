import { afterEach, describe, expect, it, vi } from "vitest";
import { player } from "./player";
import { agentSpeakOn, onSayEvent, resetSpeakModeForTests, setAgentSpeak, setSpeakMode, speakModeOn } from "./speak";

afterEach(() => {
  vi.restoreAllMocks();
  resetSpeakModeForTests();
});

function spyPlayer() {
  vi.spyOn(player, "unlock").mockImplementation(() => {});
  vi.spyOn(player, "stop").mockImplementation(() => {});
  return vi.spyOn(player, "enqueue").mockImplementation(() => {});
}

const say = (agent: string, textId: string) => ({ kind: "speak", text_id: textId, text: `from ${agent}`, agent });

describe("say events and the per-agent switches", () => {
  it("queues only the agents switched on, while the master is on", () => {
    const enqueue = spyPlayer();
    setAgentSpeak("lead-website", true);
    onSayEvent(say("orchestrator", "t_orch"));
    onSayEvent(say("lead-website", "t_web"));
    expect(enqueue).toHaveBeenCalledTimes(1);
    expect(enqueue).toHaveBeenCalledWith(expect.objectContaining({ textId: "t_web", agent: "lead-website" }));
  });

  it("the master mute silences every agent, and keeps their switches", () => {
    const enqueue = spyPlayer();
    setAgentSpeak("orchestrator", true);
    setSpeakMode(false);
    onSayEvent(say("orchestrator", "t1"));
    expect(enqueue).not.toHaveBeenCalled();
    expect(agentSpeakOn("orchestrator")).toBe(true);
  });

  it("switching an agent on turns the master on (one tap to hear it), and remembers it per browser", () => {
    spyPlayer();
    expect(speakModeOn()).toBe(false);
    setAgentSpeak("lead-crowsnest", true);
    expect(speakModeOn()).toBe(true);
    expect(JSON.parse(localStorage.getItem("crowsnest-speak-agents") ?? "{}")).toEqual({ "lead-crowsnest": true });
    setAgentSpeak("lead-crowsnest", false);
    expect(agentSpeakOn("lead-crowsnest")).toBe(false);
    expect(speakModeOn()).toBe(true);
  });

  it("is off by default for every agent", () => {
    const enqueue = spyPlayer();
    setSpeakMode(true);
    onSayEvent(say("orchestrator", "t1"));
    onSayEvent(say("", "t2"));
    expect(enqueue).not.toHaveBeenCalled();
  });
});
