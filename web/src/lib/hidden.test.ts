import { describe, expect, it } from "vitest";
import { describeHidden, findHidden, hidesSomething, revealHidden } from "./hidden";

const RLO = String.fromCodePoint(0x202e);
const LRI = String.fromCodePoint(0x2066);
const PDI = String.fromCodePoint(0x2069);
const ZWSP = String.fromCodePoint(0x200b);
const ZWJ = String.fromCodePoint(0x200d);
const LSEP = String.fromCodePoint(0x2028);
const CYRILLIC_A = String.fromCodePoint(0x0430);

describe("hidden characters (R7-S3)", () => {
  it("finds bidi controls, zero-width characters and separators, and says which change direction", () => {
    const report = findHidden(`if (isAdmin) { ${RLO}} ${LRI}// begin admins only${PDI}`, `a${ZWSP}b`, `line${LSEP}two`);
    expect(report.invisible).toBe(5);
    expect(report.bidi).toBe(true);
    expect(hidesSomething(report)).toBe(true);
    expect(describeHidden(report)).toContain("5 invisible characters, which can change the reading direction");
  });

  it("finds words that mix Latin with look-alike letters, and leaves ordinary text alone", () => {
    expect(findHidden(`${CYRILLIC_A}llow: merge j_5`).mixedWords).toEqual([`${CYRILLIC_A}llow`]);
    const clean = findHidden("const total = items.reduce((a, b) => a + b, 0); // Привет, мир 👍");
    expect(hidesSomething(clean)).toBe(false);
  });

  it("reveals each hidden character as its own piece, keeping the text around it in order", () => {
    expect(revealHidden(`ab${ZWJ}cd ${RLO}x`)).toEqual([
      { kind: "text", text: "ab" },
      { kind: "invisible", text: ZWJ, code: "U+200D" },
      { kind: "text", text: "cd " },
      { kind: "invisible", text: RLO, code: "U+202E" },
      { kind: "text", text: "x" },
    ]);
    expect(revealHidden(`go to ${CYRILLIC_A}pple.com`)).toEqual([
      { kind: "text", text: "go to " },
      { kind: "mixed", text: `${CYRILLIC_A}pple` },
      { kind: "text", text: ".com" },
    ]);
    expect(revealHidden("plain")).toEqual([{ kind: "text", text: "plain" }]);
  });
});

// R9-S11: a word written wholly in Cyrillic letters that look Latin is marked too; ordinary
// Cyrillic text with letters that do not look Latin is not.
describe("look-alike words with no Latin letter", () => {
  it("marks an all-Cyrillic file name and leaves a Russian word alone", () => {
    expect(findHidden("рауроӏ.go").mixedWords).toEqual(["рауроӏ"]);
    expect(findHidden("привет world").mixedWords).toEqual([]);
    expect(findHidden("о").mixedWords).toEqual([]);
  });
});
