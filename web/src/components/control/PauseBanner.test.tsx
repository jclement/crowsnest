import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderWithClient, stubApi } from "../../test/render";
import { PauseBanner } from "./PauseBanner";

describe("PauseBanner", () => {
  it("is absent while running", async () => {
    const calls = stubApi((call) => (call.path === "/pause" ? { body: { data: { paused: false, file: "", file_exists: false } } } : undefined));
    renderWithClient(<PauseBanner />);
    await waitFor(() => expect(calls.some((call) => call.path === "/pause")).toBe(true));
    expect(screen.queryByRole("status", { name: "Automation paused" })).toBeNull();
  });

  it("says who paused and why, and resumes", async () => {
    const calls = stubApi((call) => {
      if (call.path === "/pause") return { body: { data: { paused: true, by: "you", reason: "deploy window", file: "", file_exists: false } } };
      if (call.path === "/resume") return { body: { data: { paused: false, file: "", file_exists: false } } };
      return undefined;
    });
    renderWithClient(<PauseBanner />);
    const banner = await screen.findByRole("status", { name: "Automation paused" });
    expect(banner).toHaveTextContent("Automation paused by you: deploy window");
    await userEvent.click(screen.getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/resume" && call.method === "POST")).toBe(true));
  });
});
