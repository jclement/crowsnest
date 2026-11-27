// The queue's label on the web side (R8): the server's when it sends one, else its urgency's (a
// server before R8), the words every surface shows, and Now's and the Inbox's sentence.
import { describe, expect, it } from "vitest";
import { countsSentence, emptyCounts, isBlocking, labelOf, labelWords } from "./attention";

describe("the queue's label", () => {
  it("takes the server's label, and falls back to the urgency", () => {
    expect(labelOf({ urgency: "blocking", label: "approval" })).toBe("approval");
    expect(labelOf({ urgency: "blocking" })).toBe("blocking");
    expect(labelOf({ urgency: "can_wait" })).toBe("can_wait");
  });

  it("counts dangerous and blocking as blocking, a routine approval not", () => {
    expect(isBlocking({ urgency: "blocking", label: "dangerous" })).toBe(true);
    expect(isBlocking({ urgency: "blocking", label: "blocking" })).toBe(true);
    expect(isBlocking({ urgency: "blocking", label: "approval" })).toBe(false);
    expect(isBlocking({ urgency: "can_wait", label: "can_wait" })).toBe(false);
  });

  it("has the same words everywhere", () => {
    expect(Object.values(labelWords).map((word) => word.text)).toEqual(["Dangerous", "Blocking", "Approval", "Can wait"]);
  });

  it("says the counts in a sentence, leaving out approvals when there are none", () => {
    expect(countsSentence(emptyCounts)).toBe("Nothing needs you.");
    expect(countsSentence({ ...emptyCounts, total: 16, blocking: 4, approval: 3, can_wait: 9 })).toBe("16 need you: 4 blocking, 3 approvals, 9 can wait.");
    expect(countsSentence({ ...emptyCounts, total: 5, blocking: 1, approval: 1, can_wait: 3 })).toBe("5 need you: 1 blocking, 1 approval, 3 can wait.");
    expect(countsSentence({ ...emptyCounts, total: 2, blocking: 1, can_wait: 1 })).toBe("2 need you: 1 blocking, 1 can wait.");
  });
});
