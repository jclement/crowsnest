import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithClient, stubApi } from "../../test/render";
import { MachinesCard, RestoreHoldBanner, StorageCard, TailscaleCard, UpdatedNotice } from "./OpsCards";

const policy = { ArchiveAfterDays: 14, DeleteAfterDays: 180 };
const storage = {
  areas: [{ name: "database", label: "Database", bytes: 2 << 20, count: 0 }],
  tables: [{ table: "jobs", label: "Jobs", rows: 12, oldest: "2025-01-01T00:00:00Z" }],
  policy,
  last_run: null,
  worktrees: "Worktrees are removed by the landing sweep.",
};
const plan = {
  dry_run: true, at: "2026-11-27T03:00:00Z", policy, archive: 3, delete_jobs: 2, confirmed: 0, state_dirs: 1, state_bytes: 2048, blobs_freed: 0,
  sweeps: [{ name: "journal", label: "Journal entries", count: 40 }],
  held: [{ id: "j_7", title: "Refactor the poker", state: "done", settled_at: "2026-01-01T00:00:00Z", reason: "worked on branch cn/j7, which may hold unlanded work" }],
};

describe("StorageCard", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("previews first, and deletes a held job only when ticked", async () => {
    const calls = stubApi((call) => {
      if (call.path === "/storage") return { body: { data: storage } };
      if (call.path === "/storage/cleanup") return { body: { data: { ...plan, dry_run: (call.body as { dry_run: boolean }).dry_run } } };
      return undefined;
    });
    const user = userEvent.setup();
    renderWithClient(<StorageCard />);
    expect(await screen.findByText("Database")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Download backup/ })).toHaveAttribute("href", "/api/v1/backup");
    await user.click(screen.getByRole("button", { name: "Clean up now…" }));
    expect(await screen.findByText(/delete 40 × journal entries/)).toBeInTheDocument();
    expect(calls.find((call) => call.path === "/storage/cleanup")?.body).toEqual({ dry_run: true, confirm: [] });
    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "Clean up, and delete 1 ticked" }));
    await vi.waitFor(() => expect(calls.filter((call) => call.path === "/storage/cleanup").at(-1)?.body).toEqual({ dry_run: false, confirm: ["j_7"] }));
  });
});

describe("MachinesCard", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows each machine's figures or its error, and a minted token once", async () => {
    stubApi((call) => {
      if (call.path === "/machines") {
        return {
          body: {
            data: [
              { id: 1, name: "bedev", url: "https://bedev.example.ts.net", created_at: "", summary: { attention: 2, running: 1, paused: false, version: "v1.2.0" } },
              { id: 2, name: "relic", url: "https://relic.example.ts.net", created_at: "", error: "could not reach it" },
            ],
          },
        };
      }
      if (call.path === "/tokens" && call.method === "GET") return { body: { data: [] } };
      if (call.path === "/tokens") return { status: 201, body: { data: { token: "cnk_secretvalue", record: { id: 1, name: "quark", scope: "read", hint: "alue", created_at: "" } } } };
      return undefined;
    });
    const user = userEvent.setup();
    renderWithClient(<MachinesCard />);
    expect(await screen.findByText("2 need you")).toBeInTheDocument();
    expect(screen.getByText("could not reach it")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Token name"), "quark");
    await user.click(screen.getByRole("button", { name: "Mint a token" }));
    expect(await screen.findByText("cnk_secretvalue")).toBeInTheDocument();
  });
});

describe("TailscaleCard and UpdatedNotice", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("offers to allow the login this browser came through as", async () => {
    const calls = stubApi((call) => {
      if (call.path === "/auth/tailscale") return { body: { data: { enabled: true, serve_configured: true, current_login: "jeff@github", via_tailscale: false, allowed: [] } } };
      if (call.path === "/auth/tailscale/allow-me") return { body: { data: { enabled: true, serve_configured: true, current_login: "jeff@github", via_tailscale: false, allowed: ["jeff@github"] } } };
      if (call.path === "/updated") return { body: { data: { from: "v1.0.0", to: "v1.1.0", at: "", release_url: "https://github.com/jclement/crowsnest/releases/tag/v1.1.0" } } };
      return undefined;
    });
    const user = userEvent.setup();
    renderWithClient(
      <>
        <UpdatedNotice />
        <TailscaleCard />
      </>,
    );
    expect(await screen.findByRole("link", { name: "What changed" })).toHaveAttribute("href", "https://github.com/jclement/crowsnest/releases/tag/v1.1.0");
    await user.click(await screen.findByRole("button", { name: "Allow me (jeff@github)" }));
    await vi.waitFor(() => expect(calls.some((call) => call.path === "/auth/tailscale/allow-me")).toBe(true));
  });
});

describe("RestoreHoldBanner", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("says the instance is paused after a restore and resumes it", async () => {
    const calls = stubApi((call) => {
      if (call.path === "/restore/hold") return { body: { data: { at: "2026-11-27T03:00:00Z", reason: "restored from a backup made 2026-11-26" } } };
      if (call.path === "/restore/resume") return { body: { data: null } };
      return undefined;
    });
    const user = userEvent.setup();
    renderWithClient(<RestoreHoldBanner />);
    expect(await screen.findByText(/Paused after a restore/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Resume" }));
    await vi.waitFor(() => expect(calls.some((call) => call.path === "/restore/resume")).toBe(true));
    await vi.waitFor(() => expect(screen.queryByText(/Paused after a restore/)).not.toBeInTheDocument());
  });
});
