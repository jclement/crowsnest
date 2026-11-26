import { describe, expect, it } from "vitest";
import { inboxCount } from "./fleet";
import { fixtureQuestions, makeQuestion } from "./questionFixtures";
import { answerSummary, buildAnswer, canSubmit, compareQuestions } from "./questions";

const byId = (id: string) => fixtureQuestions.find((question) => question.id === id)!;

describe("compareQuestions", () => {
  it("puts open first, blocking at the top, notices last, then settled newest first", () => {
    const order = [...fixtureQuestions].sort(compareQuestions).map((question) => question.id);
    expect(order[0]).toBe("q_1"); // blocking
    expect(order.at(-2)).toBe("q_6"); // the notice, last of the open ones
    expect(order.at(-1)).toBe("q_7"); // answered
  });
});

describe("answers", () => {
  it("summarises single, multi, manual, ack, expired and cancelled outcomes", () => {
    expect(answerSummary(byId("q_7"))).toBe("Yes");
    expect(answerSummary({ ...byId("q_2"), status: "answered", answer_value: ["race", "leak"] })).toBe("Data race in the hub, Goroutine leak");
    expect(answerSummary({ ...byId("q_4"), status: "answered", answer_note: "Do X" })).toBe("Do X");
    expect(answerSummary({ ...byId("q_5"), status: "answered", answer_value: "ack" })).toBe("Acknowledged");
    expect(answerSummary({ ...byId("q_1"), status: "expired" })).toBe("Expired with no answer");
    expect(answerSummary({ ...byId("q_1"), status: "cancelled" })).toBe("Cancelled");
  });

  it("builds the request each kind expects", () => {
    expect(buildAnswer(byId("q_1"), ["yes"], " soon ")).toEqual({ answer_value: "yes", answer_note: "soon" });
    expect(buildAnswer(byId("q_2"), ["race", "names"], "")).toEqual({ answer_value: ["race", "names"] });
    expect(buildAnswer(byId("q_4"), [], "The sentence")).toEqual({ answer_note: "The sentence" });
    expect(buildAnswer(byId("q_5"), [], "")).toEqual({});
  });

  it("knows when a form can be submitted", () => {
    expect(canSubmit(byId("q_2"), [], "")).toBe(false);
    expect(canSubmit(byId("q_2"), ["race"], "")).toBe(true);
    expect(canSubmit(byId("q_4"), [], "  ")).toBe(false);
    expect(canSubmit(byId("q_4"), [], "x")).toBe(true);
    expect(canSubmit(makeQuestion({ kind: "ack" }), [], "")).toBe(true);
  });
});

describe("badge", () => {
  it("counts open questions plus dialogs", () => {
    expect(inboxCount(3, 2)).toBe(5);
    expect(inboxCount(undefined, 1)).toBe(1);
  });
});
