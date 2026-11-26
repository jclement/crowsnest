import { describe, expect, it } from "vitest";
import { splitSay } from "./say";

describe("splitSay", () => {
  it.each([
    ["SAY: Tests pass.\n\nThe detail.", "Tests pass.", "The detail."],
    ["\n  SAY:  **Merged**  `main`.\n\nMore", "Merged main.", "More"],
    ["SAY: -\n\nNothing to hear.", null, "Nothing to hear."],
    ["SAY: Only this.", "Only this.", ""],
  ])("takes the SAY line off %j", (reply, spoken, body) => {
    expect(splitSay(reply)).toEqual({ spoken, body });
  });

  it("leaves a reply without a leading SAY line alone", () => {
    const reply = "Answer first.\n\nSAY: quoted later";
    expect(splitSay(reply)).toEqual({ spoken: null, body: reply });
    expect(splitSay("say: lower case")).toEqual({ spoken: null, body: "say: lower case" });
  });
});
