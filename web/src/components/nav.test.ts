import { describe, expect, it } from "vitest";
import { navItems } from "./nav";

describe("navItems", () => {
  it("lists the five views in order, Jobs first as the default route", () => {
    expect(navItems.map((item) => [item.label, item.to])).toEqual([
      ["Jobs", "/"],
      ["Chat", "/chat"],
      ["Inbox", "/inbox"],
      ["Fleet", "/fleet"],
      ["Terminal", "/terminal"],
    ]);
    expect(navItems.find((item) => item.label === "Inbox")?.badge).toBe("needsYou");
  });
});
