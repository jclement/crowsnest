import { describe, expect, it } from "vitest";
import { navItems } from "./nav";

describe("navItems", () => {
  it("lists the six views in order, Jobs first as the default route, Projects beside it", () => {
    expect(navItems.map((item) => [item.label, item.to])).toEqual([
      ["Jobs", "/"],
      ["Projects", "/projects"],
      ["Chat", "/chat"],
      ["Inbox", "/inbox"],
      ["Fleet", "/fleet"],
      ["Terminal", "/terminal"],
    ]);
    expect(navItems.find((item) => item.label === "Inbox")?.badge).toBe("needsYou");
  });
});
