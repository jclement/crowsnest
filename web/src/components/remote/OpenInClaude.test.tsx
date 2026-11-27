import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { remoteControlView } from "../../lib/remote";
import { OpenInClaude } from "./OpenInClaude";

const url = "https://claude.ai/code/session_01QzP4vRk8sTn2LmWx7yBc3D";

describe("remoteControlView", () => {
  it("links only to a session URL of the exact shape", () => {
    expect(remoteControlView({ remote_control: "quark crowsnest", remote_control_url: url })).toEqual({ kind: "link", name: "quark crowsnest", url });
    for (const bad of ["https://claude.ai.evil.com/code/session_01QzP4vRk8sTn2LmWx7yBc3D", "javascript:alert(1)", "https://claude.ai/code/session_x", `${url}?a=1`]) {
      expect(remoteControlView({ remote_control: "quark crowsnest", remote_control_url: bad }).kind).toBe("fallback");
    }
  });

  it("is a fallback with the reason when requested but unknown, and off without a name", () => {
    expect(remoteControlView({ remote_control: "quark crowsnest", remote_control_note: "active" })).toEqual({ kind: "fallback", name: "quark crowsnest", reason: undefined });
    expect(remoteControlView({ remote_control: "quark crowsnest", remote_control_note: "Claude did not report it." })).toEqual({
      kind: "fallback",
      name: "quark crowsnest",
      reason: "Claude did not report it.",
    });
    expect(remoteControlView({ remote_control_url: url })).toEqual({ kind: "off" });
    expect(remoteControlView(undefined)).toEqual({ kind: "off" });
  });
});

describe("OpenInClaude", () => {
  it("opens a known session in a new tab, with no opener and the name in its title", () => {
    render(<OpenInClaude status={{ remote_control: "quark crowsnest", remote_control_note: "active", remote_control_url: url }} />);
    const link = screen.getByRole("link", { name: /Open in Claude/ });
    expect(link).toHaveAttribute("href", url);
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(link).toHaveAttribute("title", 'Open "quark crowsnest" in the Claude app');
    expect(link.className).toContain("min-h-11");
    expect(screen.queryByTestId("remote-control-fallback")).toBeNull();
  });

  it("says where to find the session, and why, when Remote Control was asked for but no link is known", () => {
    render(<OpenInClaude status={{ remote_control: "quark crowsnest", remote_control_note: "Claude did not report Remote Control active." }} />);
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByTestId("remote-control-fallback")).toHaveTextContent('Remote Control: find "quark crowsnest" in the Claude app. Claude did not report Remote Control active.');
  });

  it("renders nothing when Remote Control is off, or when told to leave out a fallback", () => {
    const { container, rerender } = render(<OpenInClaude status={{ model: "opus" } as never} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<OpenInClaude status={{ remote_control: "quark crowsnest" }} noFallback />);
    expect(container).toBeEmptyDOMElement();
    rerender(<OpenInClaude status={{ remote_control: "quark crowsnest", remote_control_url: url }} fallbackOnly />);
    expect(container).toBeEmptyDOMElement();
  });

  it("the icon variant has an accessible name", () => {
    render(<OpenInClaude status={{ remote_control: "quark Website", remote_control_url: url }} variant="icon" />);
    expect(screen.getByRole("link", { name: 'Open "quark Website" in the Claude app' })).toHaveAttribute("href", url);
  });

  it("opens the help sheet: crowsnest or the Claude app?", async () => {
    render(<OpenInClaude status={{ remote_control: "quark crowsnest", remote_control_url: url }} />);
    await userEvent.click(screen.getByRole("button", { name: "Crowsnest or the Claude app?" }));
    const sheet = await screen.findByTestId("remote-help-sheet");
    expect(sheet).toHaveTextContent("overview, decisions, approvals, many projects and landing");
    expect(sheet).toHaveTextContent("long conversation with one agent");
    expect(screen.getAllByRole("row")).toHaveLength(5);
  });
});
