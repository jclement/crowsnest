// FileDiff: Trojan Source is never silent (R7-S3). A bidi override in a changed line is drawn as a
// chip naming its code point, and the file says what it hides above the rows.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { FileView } from "../../lib/changes";
import { FileDiff } from "./FileDiff";

const RLO = String.fromCodePoint(0x202e);
const LRI = String.fromCodePoint(0x2066);
const PDI = String.fromCodePoint(0x2069);

function view(diff: string): FileView {
  return { path: "auth.js", status: "modified", hash: "h", diff, binary: false, too_large: false, cut: false, old_text: null, new_text: null, since: false, context: 3 };
}

describe("FileDiff", () => {
  it("shows invisible and direction-changing characters as chips, with a warning", () => {
    const line = `+if (accessLevel != "user${RLO} ${LRI}// Check if admin${PDI} ${LRI}") {`;
    render(<FileDiff view={view(`@@ -1,1 +1,2 @@\n x\n${line}\n`)} split={false} wrap={false} onMoreContext={() => {}} />);
    expect(screen.getByTestId("hidden-warning")).toHaveTextContent("4 invisible characters, which can change the reading direction");
    const chips = document.querySelectorAll("[data-hidden-char]");
    expect([...chips].map((chip) => chip.getAttribute("data-hidden-char"))).toEqual(["U+202E", "U+2066", "U+2069", "U+2066"]);
    expect(document.body.textContent).not.toContain(RLO);
  });

  it("says nothing about ordinary text", () => {
    render(<FileDiff view={view("@@ -1,1 +1,1 @@\n-a\n+b\n")} split={false} wrap={false} onMoreContext={() => {}} />);
    expect(screen.queryByTestId("hidden-warning")).not.toBeInTheDocument();
  });
});
