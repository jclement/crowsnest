import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderWithClient } from "../../test/render";
import { DiffView } from "./DiffView";

const file = { filename: "a.go", original: "a\nb\nc\n", final: "a\nB\nc\nd\n" };

describe("DiffView", () => {
  it("unified: one column with +/− markers and counts", () => {
    const { container } = renderWithClient(<DiffView files={[file]} layout="unified" />);
    const region = screen.getByRole("region", { name: "Diff of a.go" });
    expect(within(region).getByText("+2")).toBeInTheDocument();
    expect(within(region).getByText("−1")).toBeInTheDocument();
    expect(container.querySelector("table[data-layout=unified]")).not.toBeNull();
    const kinds = [...container.querySelectorAll("tr[data-kind]")].map((row) => row.getAttribute("data-kind"));
    expect(kinds).toEqual(["same", "del", "add", "same", "add"]);
  });

  it("unified: one narrow gutter (old number only for a deletion), no wrap until asked", async () => {
    const { container } = renderWithClient(<DiffView files={[file]} layout="unified" />);
    const rows = [...container.querySelectorAll("tr[data-kind]")].map((row) => row.querySelectorAll("td").length);
    expect(new Set(rows)).toEqual(new Set([3]));
    const deleted = container.querySelector("tr[data-kind=del]");
    expect(deleted?.querySelector("td")?.textContent).toBe("2");
    const table = container.querySelector("table[data-layout=unified]");
    expect(table?.getAttribute("data-wrap")).toBe("off");
    await userEvent.setup().click(screen.getByRole("button", { name: "Wrap" }));
    expect(container.querySelector("table[data-layout=unified]")?.getAttribute("data-wrap")).toBe("on");
  });

  it("split: the old line beside the new one", () => {
    const { container } = renderWithClient(<DiffView files={[file]} layout="split" />);
    const changed = [...container.querySelectorAll("table[data-layout=split] tr")].find((row) => row.textContent?.includes("B"));
    const cells = [...(changed?.querySelectorAll("td[data-kind]") ?? [])].map((cell) => `${cell.getAttribute("data-kind")}:${cell.textContent}`);
    expect(cells).toEqual(["del:b", "add:B"]);
  });

  it("folds long unchanged runs and expands them on a press", async () => {
    const long = Array.from({ length: 40 }, (_, index) => `l${index}`).join("\n") + "\n";
    renderWithClient(<DiffView files={[{ filename: "big.txt", original: long, final: long.replace("l20\n", "L20\n") }]} layout="unified" />);
    const gap = screen.getByRole("button", { name: /17 unchanged lines/ });
    await userEvent.setup().click(gap);
    expect(screen.queryByRole("button", { name: /17 unchanged lines/ })).toBeNull();
    expect(screen.getByText("l5")).toBeInTheDocument();
  });

  it("renders file text as text, never markup", () => {
    const { container } = renderWithClient(<DiffView files={[{ filename: "x.html", original: "", final: "<img src=x onerror=alert(1)>\n" }]} layout="unified" />);
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText("<img src=x onerror=alert(1)>")).toBeInTheDocument();
  });
});
