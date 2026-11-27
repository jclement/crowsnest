import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithClient, stubApi } from "../../test/render";
import { settingsPage } from "./fixtures.test-data";
import type { SettingsPage } from "../../lib/system";
import { NetworkSettings, ServerSettings } from "./SettingsSheet";

/** Every server section and the network, as the Settings view draws them across its sections. */
function SettingsBody({ page }: { page: SettingsPage }) {
  const sections = [...new Set(page.settings.map((setting) => setting.section))];
  return (
    <>
      <ServerSettings page={page} sections={sections} />
      <NetworkSettings page={page} />
    </>
  );
}

describe("the server's settings", () => {
  it("draws only the sections asked for", () => {
    stubApi(() => undefined);
    const page = settingsPage();
    const first = page.settings[0]!.section;
    renderWithClient(<ServerSettings page={page} sections={[first]} />);
    expect(screen.getByRole("region", { name: first })).toBeInTheDocument();
    expect(screen.getAllByRole("region")).toHaveLength(1);
  });

  afterEach(() => vi.unstubAllGlobals());

  it("shows each value's source and a locked setting read-only with where to change it", () => {
    stubApi(() => undefined);
    renderWithClient(<SettingsBody page={settingsPage()} />);
    expect(screen.getByLabelText("Poke interval")).toBeDisabled();
    expect(screen.getByText("Set by the environment (CROWSNEST_POKE_INTERVAL): change it there")).toBeInTheDocument();
    expect(screen.getByText("environment")).toBeInTheDocument();
    expect(screen.getByText("crowsnest --bind tailnet")).toBeInTheDocument();
    // Write-only: never shows the value, says it is set.
    expect(screen.getByLabelText("Notify URL")).toHaveValue("");
    expect(screen.getByLabelText("Notify URL")).toHaveAttribute("placeholder", expect.stringContaining("set (write-only)"));
  });

  it("saves a text setting with PUT and says when it applies", async () => {
    const calls = stubApi((call) =>
      call.method === "PUT" ? { body: { data: { ...settingsPage().settings[0], value: "opus", source: "database", where: "database" } } } : undefined,
    );
    const user = userEvent.setup();
    renderWithClient(<SettingsBody page={settingsPage()} />);
    const input = screen.getByLabelText("Orchestrator model");
    await user.clear(input);
    await user.type(input, "opus");
    await user.click(screen.getAllByRole("button", { name: "Save" })[0]!);
    expect(await screen.findByText("Orchestrator model saved")).toBeInTheDocument();
    expect(screen.getByText("Applies when the orchestrator restarts.")).toBeInTheDocument();
    expect(calls).toContainEqual({ method: "PUT", path: "/settings/model", body: { value: "opus" } });
  });

  it("an inverted switch turns Remote Control off by saving no-remote-control=true", async () => {
    const calls = stubApi((call) => (call.method === "PUT" ? { body: { data: { ...settingsPage().settings[2], value: "true" } } } : undefined));
    const user = userEvent.setup();
    renderWithClient(<SettingsBody page={settingsPage()} />);
    const toggle = screen.getByRole("switch", { name: /Remote Control/ });
    expect(toggle).toHaveAttribute("aria-checked", "true");
    await user.click(toggle);
    expect(calls).toContainEqual({ method: "PUT", path: "/settings/no-remote-control", body: { value: "true" } });
  });

  it("shows the server's refusal", async () => {
    stubApi(() => ({ status: 400, body: { error: { code: "VALIDATION_ERROR", message: '--model "x y" is not a model name' } } }));
    const user = userEvent.setup();
    renderWithClient(<SettingsBody page={settingsPage()} />);
    const input = screen.getByLabelText("Orchestrator model");
    await user.clear(input);
    await user.type(input, "x y");
    await user.click(screen.getAllByRole("button", { name: "Save" })[0]!);
    expect(await screen.findByRole("alert")).toHaveTextContent("is not a model name");
  });
});
