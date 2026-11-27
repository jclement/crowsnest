import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderWithClient } from "../../test/render";
import { DiffNavigator } from "./DiffNavigator";

const small = [{ filename: "a.go", original: "a\n", final: "b\n" }];
const many = Array.from({ length: 5 }, (_, index) => ({ filename: `pkg/f${index}.go`, original: "x\n", final: `y${index}\n` }));

describe("DiffNavigator", () => {
  it("shows a small diff open", () => {
    renderWithClient(<DiffNavigator files={small} />);
    expect(screen.getByRole("region", { name: "Diff of a.go" })).toBeInTheDocument();
  });

  it("collapses a big diff behind a file list; a file in the list opens and jumps to it", async () => {
    renderWithClient(<DiffNavigator files={many} />);
    expect(screen.getByText(/5 files/)).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: /Diff of/ })).toBeNull();
    const list = screen.getByRole("navigation", { name: "Changed files" });
    await userEvent.setup().click(list.querySelectorAll("button")[3] as HTMLElement);
    expect(screen.getByRole("region", { name: "Diff of pkg/f3.go" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Diff of pkg/f0.go" })).toBeNull();
  });
});
