import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithClient, stubApi } from "../test/render";
import { Footer } from "./Footer";

afterEach(() => vi.unstubAllGlobals());

function serveHealth(health: Record<string, unknown>) {
  stubApi((call) => (call.path === "/health" ? { body: { data: health } } : undefined));
}

describe("Footer", () => {
  it("links to a newer release when one is available", async () => {
    serveHealth({
      status: "ok",
      version: "v1.0.0",
      update_available: true,
      latest_version: "v1.1.0",
      release_url: "https://github.com/jclement/crowsnest/releases/tag/v1.1.0",
    });
    renderWithClient(<Footer />);
    const link = await screen.findByRole("link", { name: "v1.1.0 available" });
    expect(link).toHaveAttribute("href", "https://github.com/jclement/crowsnest/releases/tag/v1.1.0");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(screen.getByText(/v1\.0\.0/)).toBeInTheDocument();
  });

  it("shows no link when up to date", async () => {
    serveHealth({ status: "ok", version: "v1.1.0", update_available: false });
    renderWithClient(<Footer />);
    await screen.findByText(/v1\.1\.0/);
    expect(screen.queryByRole("link")).toBeNull();
  });
});
