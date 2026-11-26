import { createEvent, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { KeyBar } from "./KeyBar";

function renderBar(overrides: Partial<Parameters<typeof KeyBar>[0]> = {}) {
  const props = {
    onKey: vi.fn(),
    ctrl: false,
    onCtrlChange: vi.fn(),
    onPaste: vi.fn(),
    fontSize: 13,
    onFontSize: vi.fn(),
    ...overrides,
  };
  render(<KeyBar {...props} />);
  return props;
}

describe("KeyBar", () => {
  it("offers the keys a phone keyboard lacks, in order", () => {
    renderBar();
    const names = within(screen.getByRole("toolbar", { name: "Terminal keys" }))
      .getAllByRole("button")
      .map((button) => button.getAttribute("aria-label"));
    expect(names).toEqual([
      "Escape",
      "Tab",
      "Control (applies to the next key)",
      "Left",
      "Up",
      "Down",
      "Right",
      "Enter",
      "Paste",
      "Smaller text",
      "Larger text",
    ]);
  });

  it("reports each key", async () => {
    const user = userEvent.setup();
    const props = renderBar();
    for (const [name, key] of [
      ["Escape", "esc"],
      ["Tab", "tab"],
      ["Left", "left"],
      ["Up", "up"],
      ["Down", "down"],
      ["Right", "right"],
      ["Enter", "enter"],
    ] as const) {
      await user.click(screen.getByRole("button", { name }));
      expect(props.onKey).toHaveBeenLastCalledWith(key);
    }
    await user.click(screen.getByRole("button", { name: "Paste" }));
    expect(props.onPaste).toHaveBeenCalledTimes(1);
  });

  it("toggles the sticky Ctrl and shows its state", async () => {
    const user = userEvent.setup();
    const props = renderBar({ ctrl: true });
    const ctrl = screen.getByRole("button", { name: /^Control/ });
    expect(ctrl).toHaveAttribute("aria-pressed", "true");
    await user.click(ctrl);
    expect(props.onCtrlChange).toHaveBeenCalledWith(false);
  });

  it("steps the font size", async () => {
    const user = userEvent.setup();
    const props = renderBar({ fontSize: 14 });
    await user.click(screen.getByRole("button", { name: "Larger text" }));
    expect(props.onFontSize).toHaveBeenLastCalledWith(15);
    await user.click(screen.getByRole("button", { name: "Smaller text" }));
    expect(props.onFontSize).toHaveBeenLastCalledWith(13);
  });

  it("never takes focus from the terminal", () => {
    render(
      <>
        <textarea aria-label="terminal input" />
        <KeyBar onKey={() => {}} ctrl={false} onCtrlChange={() => {}} onPaste={() => {}} fontSize={13} onFontSize={() => {}} />
      </>,
    );
    const input = screen.getByRole("textbox", { name: "terminal input" });
    input.focus();
    for (const button of within(screen.getByRole("toolbar")).getAllByRole("button")) {
      expect(button).toHaveAttribute("tabindex", "-1");
      for (const make of [createEvent.pointerDown, createEvent.mouseDown]) {
        const event = make(button);
        fireEvent(button, event);
        expect(event.defaultPrevented).toBe(true); // what would have moved focus is cancelled
      }
    }
    expect(input).toHaveFocus();
  });
});
