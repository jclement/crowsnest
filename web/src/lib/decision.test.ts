import { describe, expect, it } from "vitest";
import { fixtureApprovals } from "./approvalFixtures";
import {
  approvalBrief,
  approvalStale,
  decisionLink,
  dialogBrief,
  isRoutine,
  lastAssistantText,
  loadNote,
  parseItemId,
  questionBrief,
  questionStale,
  saveNote,
} from "./decision";
import { makeQuestion } from "./questionFixtures";

/** A Storage in memory, so the note tests need no browser. */
function memoryStorage(): Storage {
  const data = new Map<string, string>();
  return {
    get length() {
      return data.size;
    },
    clear: () => data.clear(),
    getItem: (key) => data.get(key) ?? null,
    key: (index) => [...data.keys()][index] ?? null,
    removeItem: (key) => void data.delete(key),
    setItem: (key, value) => void data.set(key, value),
  };
}

const confirm = makeQuestion({
  id: "q_7",
  title: "Ship the migration tonight?",
  kind: "confirm",
  recommend: "no",
  options: [
    { key: "yes", label: "Ship it", detail: "Runs at 22:00; 4 minutes of downtime." },
    { key: "no", label: "Wait for Monday" },
  ],
});

describe("parseItemId and decisionLink", () => {
  it("knows the three decision kinds and nothing else", () => {
    expect(parseItemId("question:q_7")).toEqual({ kind: "question", ref: "q_7" });
    expect(parseItemId("approval:ap_3")).toEqual({ kind: "approval", ref: "ap_3" });
    expect(parseItemId("dialog:w1:p2")).toEqual({ kind: "dialog", ref: "w1:p2" });
    expect(parseItemId("job:j_5")).toBeNull();
    expect(parseItemId("question:")).toBeNull();
    expect(parseItemId(undefined)).toBeNull();
    expect(decisionLink({ id: "approval:ap_3", kind: "approval" })).toEqual({ to: "/decide", search: { item: "approval:ap_3" } });
    expect(decisionLink({ id: "job:j_5", kind: "job" })).toBeNull();
  });
});

describe("briefs", () => {
  it("a question: options with consequences, the recommendation, and what is blocked", () => {
    const brief = questionBrief(confirm, true);
    expect(brief.ask).toBe("Ship the migration tonight?");
    expect(brief.choices.map((choice) => [choice.label, choice.consequence, choice.recommended])).toEqual([
      ["Ship it", "Runs at 22:00; 4 minutes of downtime.", false],
      ["Wait for Monday", "The agent does not do it.", true],
    ]);
    expect(brief.recommendation).toBe("The agent recommends “Wait for Monday”.");
    expect(brief.blocking).toMatch(/waiting/);
    expect(questionBrief({ ...confirm, urgency: "normal" }, false).blocking).toBeNull();
  });

  it("an approval: allow once or deny, dangerous spelled out, a recommendation only for low risk", () => {
    const low = fixtureApprovals.find((approval) => approval.risk === "low");
    const dangerous = fixtureApprovals.find((approval) => approval.risk === "dangerous");
    expect(low && dangerous).toBeTruthy();
    if (!low || !dangerous) return;
    expect(approvalBrief(low).choices.map((choice) => choice.label)).toEqual(["Allow once", "Deny"]);
    expect(approvalBrief(low).recommendation).toMatch(/^Low risk/);
    expect(approvalBrief(dangerous).choices[0]?.consequence).toMatch(/dangerous/);
    expect(approvalBrief(dangerous).recommendation).toBeNull();
  });

  it("a dialog: digits, warnings first, arrow-key options without a digit", () => {
    const brief = dialogBrief("w1:p2", "Paused", [
      { key: "1", label: "Yes" },
      { key: "2", label: "Yes, and don't ask again", warning: "stops asking" },
      { key: "", label: "Trust" },
    ]);
    expect(brief.choices.map((choice) => choice.consequence)).toEqual([
      "Presses 1 in the agent's terminal.",
      "Careful: stops asking",
      "Picks it in the agent's terminal.",
    ]);
  });
});

describe("staleness", () => {
  it("says why a settled decision cannot be taken", () => {
    expect(questionStale(confirm)).toBeNull();
    expect(questionStale({ ...confirm, status: "answered" })).toBe("Already answered.");
    expect(questionStale({ ...confirm, status: "expired" })).toMatch(/not consent/);
    const approval = fixtureApprovals[0];
    if (!approval) throw new Error("no fixture approval");
    expect(approvalStale({ ...approval, status: "pending" })).toBeNull();
    expect(approvalStale({ ...approval, status: "superseded", status_reason: "Answered in the terminal." })).toBe("No longer waiting: Answered in the terminal.");
    expect(approvalStale({ ...approval, status: "allowed", decided_via: "policy", status_reason: "Allowed by rule r_1." })).toBe("Already allowed by a rule. Allowed by rule r_1.");
  });
});

describe("routine questions", () => {
  it("only a single choice with no files and a short body is answerable from a row", () => {
    expect(isRoutine(confirm)).toBe(true);
    expect(isRoutine({ ...confirm, files: [{ filename: "a", original: "", final: "x" }] })).toBe(false);
    expect(isRoutine({ ...confirm, body_md: "x".repeat(401) })).toBe(false);
    expect(isRoutine({ ...confirm, kind: "choice", multi: true })).toBe(false);
    expect(isRoutine({ ...confirm, kind: "manual" })).toBe(false);
  });
});

describe("the note", () => {
  it("survives a reload, is removed when emptied, and old notes are swept", () => {
    const storage = memoryStorage();
    const now = 1_700_000_000_000;
    saveNote("question:q_7", "only after the backup", storage, now);
    expect(loadNote("question:q_7", storage, now + 1000)).toBe("only after the backup");
    saveNote("question:q_7", "", storage, now);
    expect(loadNote("question:q_7", storage, now)).toBe("");
    saveNote("question:q_1", "old", storage, now);
    expect(loadNote("question:q_1", storage, now + 4 * 24 * 60 * 60 * 1000)).toBe("");
    expect(storage.length).toBe(0);
  });
});

describe("lastAssistantText", () => {
  it("finds the newest assistant text and trims it", () => {
    const messages = [
      { kind: "assistant", text: "first" },
      { kind: "tool" },
      { kind: "assistant", text: "  second  " },
      { kind: "user", text: "later prompt" },
    ];
    expect(lastAssistantText(messages)).toBe("second");
    expect(lastAssistantText([{ kind: "assistant", text: "x".repeat(20) }], 10)).toBe("xxxxxxxxx…");
    expect(lastAssistantText([])).toBeNull();
  });
});
