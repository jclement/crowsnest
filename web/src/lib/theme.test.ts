import { beforeEach, describe, expect, it } from "vitest";
import { themes } from "../themes";
import { applyStartupTheme, clearThemeName, cycleThemeName, nextThemeName, readThemeName, setThemeName, THEME_NAME_KEY } from "./theme";

describe("named themes", () => {
  beforeEach(() => {
    localStorage.clear();
    delete document.documentElement.dataset.themeDefault;
    document.documentElement.dataset.theme = "crowsnest";
  });

  it("reads this browser's choice, else the server's default, else the house theme", () => {
    expect(readThemeName()).toBe("crowsnest");
    document.documentElement.dataset.themeDefault = "ember";
    expect(readThemeName()).toBe("ember");
    localStorage.setItem(THEME_NAME_KEY, "phosphor");
    expect(readThemeName()).toBe("phosphor");
  });

  it("ignores a theme that no longer exists, and corrects <html> at start", () => {
    localStorage.setItem(THEME_NAME_KEY, "vaporwave");
    document.documentElement.dataset.theme = "vaporwave";
    expect(readThemeName()).toBe("crowsnest");
    applyStartupTheme();
    expect(document.documentElement.dataset.theme).toBe("crowsnest");
  });

  it("sets, cycles in picker order and wraps, and clears back to the default", () => {
    setThemeName("deep-sea");
    expect(document.documentElement.dataset.theme).toBe("deep-sea");
    const order = themes.map((theme) => theme.id);
    expect(order[0]).toBe("crowsnest");
    expect(nextThemeName(order[order.length - 1]!)).toBe(order[0]);
    const before = readThemeName();
    expect(cycleThemeName()).toBe(order[(order.indexOf(before) + 1) % order.length]);
    clearThemeName();
    expect(localStorage.getItem(THEME_NAME_KEY)).toBeNull();
    expect(document.documentElement.dataset.theme).toBe("crowsnest");
  });

  it("fires an event the pickers follow", () => {
    let seen = "";
    const follow = (event: Event) => (seen = (event as CustomEvent<string>).detail);
    window.addEventListener("crowsnest-theme-name", follow);
    setThemeName("ember");
    window.removeEventListener("crowsnest-theme-name", follow);
    expect(seen).toBe("ember");
  });
});
