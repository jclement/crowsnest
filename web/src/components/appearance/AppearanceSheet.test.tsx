import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithClient, stubApi } from "../../test/render";
import { THEME_KEY, THEME_NAME_KEY } from "../../lib/theme";
import { themes } from "../../themes";
import { AppearanceBody } from "./AppearanceSheet";

const themeSetting = (value: string) => ({
  name: "theme",
  label: "Default theme",
  section: "Appearance",
  help: "",
  kind: "text",
  apply: "live",
  value,
  is_set: value !== "",
  default: "",
  source: value ? "database" : "default",
  where: value ? "database" : "default",
});

function settingsRoute(value = "") {
  return (call: { method: string; path: string; body?: unknown }) => {
    if (call.path === "/settings" && call.method === "GET") return { body: { data: { settings: [themeSetting(value)], bind: "localhost", name: "quark" } } };
    if (call.path === "/settings/theme") return { body: { data: themeSetting((call.body as { value: string }).value) } };
    return undefined;
  };
}

describe("AppearanceBody", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.className = "";
    document.documentElement.dataset.theme = "crowsnest";
    delete document.documentElement.dataset.themeDefault;
  });
  afterEach(() => vi.unstubAllGlobals());

  it("offers every theme, each shown in both modes, with the one in use checked", () => {
    stubApi(settingsRoute());
    renderWithClient(<AppearanceBody />);
    const group = screen.getByRole("radiogroup", { name: "Theme" });
    const cards = within(group).getAllByRole("radio");
    expect(cards).toHaveLength(themes.length);
    expect(themes.length).toBeGreaterThanOrEqual(5);
    expect(within(group).getByRole("radio", { name: "Crowsnest theme" })).toHaveAttribute("aria-checked", "true");
    // A light and a dark preview of each theme, scoped by data-theme.
    for (const theme of themes) {
      const previews = document.querySelectorAll(`[data-theme="${theme.id}"]:not(html)`);
      expect(previews).toHaveLength(2);
      expect([...previews].filter((preview) => preview.classList.contains("dark"))).toHaveLength(1);
    }
  });

  it("switches the theme for this browser, on <html> and in storage", async () => {
    stubApi(settingsRoute());
    const user = userEvent.setup();
    renderWithClient(<AppearanceBody />);
    await user.click(screen.getByRole("radio", { name: "Phosphor theme" }));
    expect(document.documentElement.dataset.theme).toBe("phosphor");
    expect(localStorage.getItem(THEME_NAME_KEY)).toBe("phosphor");
    expect(screen.getByRole("radio", { name: "Phosphor theme" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("radio", { name: "Crowsnest theme" })).toHaveAttribute("aria-checked", "false");
    // Forgetting the choice goes back to the default.
    await user.click(screen.getByRole("button", { name: "Follow the default" }));
    expect(localStorage.getItem(THEME_NAME_KEY)).toBeNull();
    expect(document.documentElement.dataset.theme).toBe("crowsnest");
  });

  it("sets the mode separately from the theme", async () => {
    stubApi(settingsRoute());
    const user = userEvent.setup();
    renderWithClient(<AppearanceBody />);
    const mode = screen.getByRole("radiogroup", { name: "Mode" });
    expect(within(mode).getByRole("radio", { name: "System" })).toHaveAttribute("aria-checked", "true");
    await user.click(within(mode).getByRole("radio", { name: "Dark" }));
    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem(THEME_KEY)).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("crowsnest");
  });

  it("makes a theme the default for new browsers through the setting", async () => {
    const calls = stubApi(settingsRoute("deep-sea"));
    const user = userEvent.setup();
    renderWithClient(<AppearanceBody />);
    const card = (name: string) => screen.getByRole("radio", { name: `${name} theme` }).parentElement!;
    await waitFor(() => expect(within(card("Deep sea")).getByText("default")).toBeInTheDocument());
    expect(within(card("Deep sea")).queryByRole("button", { name: "Use for new browsers" })).toBeNull();
    const ember = screen.getByRole("radio", { name: "Ember theme" }).parentElement!;
    await user.click(within(ember).getByRole("button", { name: "Use for new browsers" }));
    await waitFor(() => expect(calls).toContainEqual({ method: "PUT", path: "/settings/theme", body: { value: "ember" } }));
  });

  it("follows the server's default when this browser has not chosen", () => {
    document.documentElement.dataset.themeDefault = "ember";
    stubApi(settingsRoute("ember"));
    renderWithClient(<AppearanceBody />);
    expect(screen.getByRole("radio", { name: "Ember theme" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByText(/Following the default for new browsers/)).toBeInTheDocument();
  });
});
