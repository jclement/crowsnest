import { describe, expect, it } from "vitest";
import { fuzzyMatch, fuzzyMatchAll, highlightRuns, queryTokens } from "./fuzzy";

describe("fuzzyMatch", () => {
  it("matches everything with an empty pattern", () => {
    expect(fuzzyMatch("", "anything")).toEqual({ score: 0, positions: [] });
  });

  it("returns null when the characters are not all there in order", () => {
    expect(fuzzyMatch("xyz", "jobs")).toBeNull();
    expect(fuzzyMatch("bj", "job")).toBeNull();
    expect(fuzzyMatch("jobs!", "jobs")).toBeNull();
  });

  it("is case-insensitive", () => {
    expect(fuzzyMatch("INBOX", "Inbox")?.positions).toEqual([0, 1, 2, 3, 4]);
    expect(fuzzyMatch("inbox", "INBOX")).not.toBeNull();
  });

  it("prefers word starts over the first occurrence", () => {
    // The first f is inside "buffer"; the word-start f is "fix".
    const match = fuzzyMatch("fx", "buffer: fix export");
    expect(match?.positions).toEqual([8, 10]);
    const acronym = fuzzyMatch("rso", "restart orchestrator");
    expect(acronym?.positions[0]).toBe(0);
  });

  it("prefers consecutive characters to scattered ones", () => {
    const tight = fuzzyMatch("term", "Terminal")!;
    const loose = fuzzyMatch("term", "the error message")!;
    expect(tight.score).toBeGreaterThan(loose.score);
  });

  it("ranks a prefix above a mid-word match", () => {
    const prefix = fuzzyMatch("chat", "Chat")!;
    const inside = fuzzyMatch("chat", "Rechatter")!;
    expect(prefix.score).toBeGreaterThan(inside.score);
  });

  it("treats camel humps and letter-to-digit steps as boundaries", () => {
    expect(fuzzyMatch("nj", "newJob")?.positions).toEqual([0, 3]);
    expect(fuzzyMatch("j12", "j_12")?.positions).toEqual([0, 2, 3]);
  });

  it("finds the best alignment, not the greedy one", () => {
    // Greedy would take the o of "go" and miss "orch"'s run.
    const match = fuzzyMatch("orch", "go to orchestrator")!;
    expect(match.positions).toEqual([6, 7, 8, 9]);
  });

  it("cuts very long texts rather than slowing down", () => {
    const long = "a".repeat(10_000) + "zz";
    expect(fuzzyMatch("zz", long)).toBeNull();
    const start = performance.now();
    for (let index = 0; index < 200; index++) fuzzyMatch("abcabcabc", "ab".repeat(200));
    expect(performance.now() - start).toBeLessThan(500);
  });
});

describe("fuzzyMatchAll", () => {
  it("needs every token to match", () => {
    expect(fuzzyMatchAll(["fix", "login"], "Fix the login page")).not.toBeNull();
    expect(fuzzyMatchAll(["fix", "logout"], "Fix the login page")).toBeNull();
  });

  it("sums scores and merges positions", () => {
    const match = fuzzyMatchAll(["ru", "j1"], "running j_1")!;
    expect(match.positions).toEqual([0, 1, 8, 10]);
    expect(match.score).toBe(fuzzyMatch("ru", "running j_1")!.score + fuzzyMatch("j1", "running j_1")!.score);
  });
});

describe("queryTokens", () => {
  it("splits on whitespace and drops empties", () => {
    expect(queryTokens("  give   job  ")).toEqual(["give", "job"]);
    expect(queryTokens("")).toEqual([]);
  });
});

describe("highlightRuns", () => {
  it("cuts a text into matched and plain runs", () => {
    expect(highlightRuns("Inbox", [0, 1, 4])).toEqual([
      { text: "In", match: true },
      { text: "bo", match: false },
      { text: "x", match: true },
    ]);
    expect(highlightRuns("Jobs", [])).toEqual([{ text: "Jobs", match: false }]);
    expect(highlightRuns("", [])).toEqual([]);
  });
});
