import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ToolCard } from "./ToolCard";

describe("ToolCard", () => {
  it("is one collapsed line with the name and a short input", () => {
    render(<ToolCard tool={{ id: "t", name: "Bash", summary: "go test ./...", input: '{"command":"go test ./..."}', done: true, result: "ok" }} />);
    const toggle = screen.getByRole("button", { name: /Bash/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveTextContent("go test ./...");
    expect(screen.queryByText("Result")).not.toBeInTheDocument();
  });

  it("expands to the input and the truncated result", async () => {
    const user = userEvent.setup();
    render(
      <ToolCard tool={{ id: "t", name: "Read", summary: "a.go", input: '{"file_path":"a.go"}', done: true, result: "package main", result_truncated: true }} />,
    );
    await user.click(screen.getByRole("button", { name: /Read/ }));
    expect(screen.getByText('{"file_path":"a.go"}')).toBeInTheDocument();
    expect(screen.getByText("package main")).toBeInTheDocument();
    expect(screen.getByText(/Truncated/)).toBeInTheDocument();
  });

  it("marks a running call and a failed one", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<ToolCard tool={{ id: "t", name: "Bash", summary: "sleep 9", input: "", done: false }} />);
    expect(screen.getByLabelText("Running")).toBeInTheDocument();
    rerender(<ToolCard tool={{ id: "t", name: "Bash", summary: "cat x", input: "", done: true, is_error: true, result: "No such file" }} />);
    expect(screen.getByLabelText("Failed")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Bash/ }));
    expect(screen.getByText("Error")).toBeInTheDocument();
  });
});
