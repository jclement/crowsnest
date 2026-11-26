import { describe, expect, it } from "vitest";
import { formatVersion, safeRedirect } from "./format";
import { taglines } from "./taglines";
import { applyTheme, nextTheme, readTheme, THEME_KEY } from "./theme";

describe("safeRedirect", () => {
  it.each([
    [undefined, "/"],
    ["/fleet", "/fleet"],
    ["/inbox?x=1", "/inbox?x=1"],
    ["//evil.example/path", "/"],
    ["https://evil.example", "/"],
    ["/login?redirect=/", "/"],
  ])("%s → %s", (input, want) => expect(safeRedirect(input)).toBe(want));
});

describe("formatVersion", () => {
  it.each([
    [undefined, ""],
    ["dev", "DEV"],
    ["v1.2.3", "v1.2.3"],
    ["1.2.3", "v1.2.3"],
    ["194db89", "194db89"],
    ["v0.1.0-3-g194db89-dirty", "v0.1.0-3-g194db89-dirty"],
  ])("%s → %s", (input, want) => expect(formatVersion(input)).toBe(want));
});

describe("theme", () => {
  it("cycles system → light → dark → system and applies the class", () => {
    expect(nextTheme("system")).toBe("light");
    expect(nextTheme("light")).toBe("dark");
    expect(nextTheme("dark")).toBe("system");
    applyTheme("dark");
    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem(THEME_KEY)).toBe("dark");
    applyTheme("light");
    expect(document.documentElement).not.toHaveClass("dark");
    applyTheme("system");
    expect(localStorage.getItem(THEME_KEY)).toBeNull();
    expect(readTheme()).toBe("system");
  });
});

describe("taglines", () => {
  it("loads the shared set of 200", () => {
    expect(taglines).toHaveLength(200);
  });
});
