import { describe, expect, it } from "vitest";
import {
  advanceQueue,
  agentKey,
  agentLabel,
  clearQueue,
  emptyQueue,
  enqueueLine,
  JOB_AGENTS,
  MAX_WAITING,
  playFirst,
  STALE_MS,
  wantsLine,
  type QueueState,
  type SpokenLine,
} from "./queue";

const line = (textId: string, agent: string, at = 0): SpokenLine => ({ textId, text: `said ${textId}`, agent, at });

/** Feeds lines in order at `now` and returns the final state with everything that started. */
function run(lines: SpokenLine[], now = 0): { state: QueueState; started: string[] } {
  let state = emptyQueue;
  const started: string[] = [];
  for (const next of lines) {
    const step = enqueueLine(state, next, now);
    state = step.state;
    if (step.started) started.push(step.started.textId);
  }
  return { state, started };
}

describe("the spoken-reply queue", () => {
  it("never lets two agents talk at once: the second waits for the first to finish", () => {
    const { state, started } = run([line("t1", "orchestrator"), line("t2", "lead-website")]);
    expect(started).toEqual(["t1"]);
    expect(state.current?.textId).toBe("t1");
    expect(state.waiting.map((l) => l.textId)).toEqual(["t2"]);
    const after = advanceQueue(state, 1000);
    expect(after.started?.textId).toBe("t2");
    expect(after.state.current?.agent).toBe("lead-website");
  });

  it("is first come, first served across agents", () => {
    let { state } = run([line("a", "orchestrator"), line("b", "lead-x"), line("c", "orchestrator"), line("d", "lead-y")]);
    const order = [state.current?.textId];
    while (state.current) {
      state = advanceQueue(state, 10).state;
      if (state.current) order.push(state.current.textId);
    }
    expect(order).toEqual(["a", "b", "c", "d"]);
  });

  it("skip moves to the next line; skipping the last leaves it idle", () => {
    const { state } = run([line("a", "orchestrator"), line("b", "lead-x")]);
    const skipped = advanceQueue(state, 0);
    expect(skipped.state.current?.textId).toBe("b");
    const done = advanceQueue(skipped.state, 0);
    expect(done.state).toEqual(emptyQueue);
    expect(done.started).toBeNull();
  });

  it("stop clears what plays and what waits, and reports them dropped", () => {
    const { state } = run([line("a", "orchestrator"), line("b", "lead-x"), line("c", "lead-y")]);
    const stopped = clearQueue(state);
    expect(stopped.state).toEqual(emptyQueue);
    expect(stopped.dropped.map((l) => l.textId)).toEqual(["a", "b", "c"]);
  });

  it("drops a line older than two minutes when its turn comes, instead of speaking it late", () => {
    const { state } = run([line("old-1", "orchestrator", 0), line("old-2", "lead-x", 0), line("fresh", "lead-y", STALE_MS)], 0);
    // old-1 played for over two minutes (a long answer); then old-2 is stale, fresh is not.
    const next = advanceQueue(state, STALE_MS + 1);
    expect(next.dropped.map((l) => l.textId)).toEqual(["old-2"]);
    expect(next.state.current?.textId).toBe("fresh");
  });

  it("drops a stale line even when it arrives to an idle queue", () => {
    const step = enqueueLine(emptyQueue, line("late", "orchestrator", 0), STALE_MS + 5);
    expect(step.started).toBeNull();
    expect(step.dropped.map((l) => l.textId)).toEqual(["late"]);
    expect(step.state).toEqual(emptyQueue);
  });

  it("keeps at most MAX_WAITING lines waiting, dropping the oldest", () => {
    const lines = Array.from({ length: MAX_WAITING + 3 }, (_, index) => line(`t${index}`, "orchestrator"));
    const { state } = run(lines);
    expect(state.current?.textId).toBe("t0");
    expect(state.waiting).toHaveLength(MAX_WAITING);
    expect(state.waiting[0]?.textId).toBe("t3");
  });

  it("a play button plays its line now and keeps the waiting lines' turn", () => {
    const { state } = run([line("a", "orchestrator"), line("b", "lead-x")]);
    const step = playFirst(state, line("button", "lead-y"));
    expect(step.started?.textId).toBe("button");
    expect(step.dropped.map((l) => l.textId)).toEqual(["a"]);
    expect(step.state.waiting.map((l) => l.textId)).toEqual(["b"]);
  });
});

describe("who is heard", () => {
  const prefs = (master: boolean, agents: Record<string, boolean>) => ({ master, agents });

  it("speaks an agent only when it is opted in and the master is on", () => {
    expect(wantsLine("orchestrator", prefs(true, { orchestrator: true }))).toBe(true);
    expect(wantsLine("lead-website", prefs(true, { orchestrator: true }))).toBe(false);
    expect(wantsLine("lead-website", prefs(true, { "lead-website": true }))).toBe(true);
  });

  it("is silent for everyone when the master mute is off", () => {
    expect(wantsLine("orchestrator", prefs(false, { orchestrator: true, "lead-website": true }))).toBe(false);
  });

  it("is off by default: an agent nobody chose is not spoken", () => {
    expect(wantsLine("orchestrator", prefs(true, {}))).toBe(false);
  });

  it("puts job agents' say lines (no agent) under one switch of their own", () => {
    expect(agentKey("")).toBe(JOB_AGENTS);
    expect(agentKey(undefined)).toBe(JOB_AGENTS);
    expect(wantsLine("", prefs(true, { [JOB_AGENTS]: true }))).toBe(true);
    expect(wantsLine("", prefs(true, { orchestrator: true }))).toBe(false);
  });

  it("names the speaker from the server's labels, else from the agent name", () => {
    expect(agentLabel("lead-website", new Map([["lead-website", "Website"]]))).toBe("Website");
    expect(agentLabel("lead-website")).toBe("website lead");
    expect(agentLabel("orchestrator")).toBe("Orchestrator");
    expect(agentLabel("")).toBe("A job agent");
  });
});
