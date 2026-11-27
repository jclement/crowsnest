import { describe, expect, it } from "vitest";
import { inMore, moreNav, navItems, primaryNav } from "./nav";

describe("nav", () => {
  it("puts four destinations first, Now as home, with the badge on the Inbox", () => {
    expect(primaryNav.map((item) => [item.label, item.to])).toEqual([
      ["Now", "/"],
      ["Projects", "/projects"],
      ["Inbox", "/inbox"],
      ["Chat", "/chat"],
    ]);
    expect(navItems.find((item) => item.label === "Inbox")?.badge).toBe("needsYou");
  });

  it("keeps the board and the tools under More, in the same order on every screen", () => {
    expect(moreNav.map((item) => item.to)).toEqual(["/jobs", "/fleet", "/terminal", "/help"]);
    expect(navItems).toEqual([...primaryNav, ...moreNav]);
  });

  it("knows when a More view is current", () => {
    expect(inMore("/jobs")).toBe(true);
    expect(inMore("/fleet")).toBe(true);
    expect(inMore("/help/concepts/jobs")).toBe(true);
    expect(inMore("/")).toBe(false);
    expect(inMore("/projects/crowsnest")).toBe(false);
  });
});
