import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithClient, stubApi } from "../../test/render";
import type { PublishView } from "../../lib/tailnet";
import { PublishCard } from "./PublishCard";

const offer: PublishView = {
  show: true,
  state: "off",
  enabled: false,
  url: "https://quark.tail1234.ts.net:8443",
  port: 8443,
  port_note: "Port 443 already serves something else on this node, so crowsnest uses 8443.",
  target: "http://127.0.0.1:2769",
  name: "quark.tail1234.ts.net",
};
const qr = "data:image/png;base64,iVBORw0KGgo=";

describe("PublishCard", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the exact address and what it means before publishing, then the link, QR and Stop", async () => {
    let current: PublishView = offer;
    const calls = stubApi((call) => {
      if (call.path !== "/tailscale/serve") return undefined;
      if (call.method === "POST") current = { ...offer, state: "publishing", enabled: true, qr };
      if (call.method === "DELETE") current = { ...offer, state: "stopped" };
      return { body: { data: current } };
    });
    const user = userEvent.setup();
    renderWithClient(<PublishCard />);
    expect(await screen.findByText("https://quark.tail1234.ts.net:8443")).toBeInTheDocument();
    expect(screen.getByText(/gets crowsnest's sign-in page there/)).toBeInTheDocument();
    expect(screen.getByText(/so crowsnest uses 8443/)).toBeInTheDocument();
    expect(screen.queryByRole("img")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Publish" }));
    const link = await screen.findByRole("link", { name: /quark\.tail1234\.ts\.net:8443/ });
    expect(link).toHaveAttribute("href", "https://quark.tail1234.ts.net:8443");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(screen.getByRole("img", { name: "QR code for https://quark.tail1234.ts.net:8443" })).toHaveAttribute("src", qr);
    expect(screen.getByText("publishing")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Stop publishing" }));
    expect(await screen.findByText("stopped")).toBeInTheDocument();
    expect(calls.filter((call) => call.path === "/tailscale/serve").map((call) => call.method)).toEqual(["GET", "POST", "DELETE"]);
  });

  it("is hidden without Tailscale, and shows the fallback instead", async () => {
    stubApi((call) => (call.path === "/tailscale/serve" ? { body: { data: { show: false, state: "unavailable", enabled: false } } } : undefined));
    renderWithClient(<PublishCard fallback={<p>run it yourself</p>} />);
    expect(await screen.findByText("run it yourself")).toBeInTheDocument();
    expect(screen.queryByText("Publish to your tailnet")).not.toBeInTheDocument();
  });

  it("explains a failure with the fix, links only Tailscale's own pages, and offers Stop", async () => {
    stubApi((call) =>
      call.path === "/tailscale/serve"
        ? {
            body: {
              data: {
                ...offer,
                state: "failed",
                enabled: true,
                reason: "HTTPS certificates are off for your tailnet, so tailscale serve cannot give crowsnest an https:// address.",
                fix: "Tailscale admin console → DNS → HTTPS Certificates → Enable HTTPS.",
                fix_url: "https://login.tailscale.com/admin/dns",
                retry_at: "2026-11-27T12:00:00Z",
              },
            },
          }
        : undefined,
    );
    renderWithClient(<PublishCard />);
    expect(await screen.findByRole("alert")).toHaveTextContent("HTTPS certificates are off");
    expect(screen.getByText(/Enable HTTPS/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /login\.tailscale\.com\/admin\/dns/ })).toBeInTheDocument();
    expect(screen.getByText(/Trying again at/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Stop publishing" })).toBeInTheDocument();
  });

  it("refuses without a login, says why, and has nothing to press", async () => {
    stubApi((call) =>
      call.path === "/tailscale/serve"
        ? { body: { data: { show: true, state: "refused", enabled: false, reason: "crowsnest runs with --no-auth (no login), so publishing it would give every device on your tailnet a shell.", fix: "Start crowsnest with a login.", fix_url: "https://evil.example/x" } } }
        : undefined,
    );
    renderWithClient(<PublishCard />);
    expect(await screen.findByText(/--no-auth/)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  // R9-S3: publishing together with Sign in with Tailscale says the risk in words.
  it("says the local risk when Sign in with Tailscale is on too", async () => {
    const risk = "Publishing and Sign in with Tailscale are both on (--tailscale-auth-local-risk): any program on this machine, an agent included, can sign in as you with one forged request to crowsnest's port. Turn one of them off unless you accept that.";
    stubApi((call) => (call.path === "/tailscale/serve" ? { body: { data: { ...offer, state: "publishing", enabled: true, qr, local_risk: risk } } } : undefined));
    renderWithClient(<PublishCard />);
    expect(await screen.findByRole("alert")).toHaveTextContent("any program on this machine, an agent included, can sign in as you");
  });

  it("is read-only when a flag holds the setting", async () => {
    stubApi((call) => (call.path === "/tailscale/serve" ? { body: { data: { ...offer, locked: "Set by the environment (CROWSNEST_TAILSCALE_SERVE): change it there" } } } : undefined));
    renderWithClient(<PublishCard />);
    expect(await screen.findByText(/CROWSNEST_TAILSCALE_SERVE/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Publish" })).toBeDisabled();
  });

  it("shows a refused publish as a toast", async () => {
    stubApi((call) => {
      if (call.path !== "/tailscale/serve") return undefined;
      if (call.method === "POST") return { status: 409, body: { error: { code: "CONFLICT", message: "Port 443 and 8443 changed meanwhile." } } };
      return { body: { data: offer } };
    });
    const user = userEvent.setup();
    renderWithClient(<PublishCard />);
    await user.click(await screen.findByRole("button", { name: "Publish" }));
    expect(await screen.findByText("Could not publish to your tailnet")).toBeInTheDocument();
  });
});
