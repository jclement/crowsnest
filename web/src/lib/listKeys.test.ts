import { describe, expect, it } from "vitest";
import { nextRowIndex } from "./listKeys";

// R10 F-F10: j and k move through a list's rows, and stop at its ends.
describe("nextRowIndex", () => {
  it("starts at the first row for j and the last for k", () => {
    expect(nextRowIndex(5, -1, "j")).toBe(0);
    expect(nextRowIndex(5, -1, "k")).toBe(4);
  });
  it("moves one row and stops at the ends", () => {
    expect(nextRowIndex(5, 2, "j")).toBe(3);
    expect(nextRowIndex(5, 2, "k")).toBe(1);
    expect(nextRowIndex(5, 4, "j")).toBeNull();
    expect(nextRowIndex(5, 0, "k")).toBeNull();
    expect(nextRowIndex(0, -1, "j")).toBeNull();
  });
});
