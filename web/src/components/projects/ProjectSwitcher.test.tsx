import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { makeProjectList } from "../../lib/projectFixtures";
import { ProjectSwitcher } from "./ProjectSwitcher";

describe("ProjectSwitcher", () => {
  it("offers All, each project and Unfiled with their need-you and running counts", async () => {
    const onChange = vi.fn();
    render(<ProjectSwitcher list={makeProjectList()} value={{ kind: "project", slug: "website" }} onChange={onChange} />);
    const options = screen.getAllByRole("radio");
    expect(options.map((option) => option.textContent)).toEqual(["All2 need you4 running", "Crowsnest1 need you2 running", "Website1 running", "Infra", "Unfiled1 need you1 running"]);
    expect(screen.getByRole("radio", { name: /^Website/ })).toHaveAttribute("aria-checked", "true");
    await userEvent.click(screen.getByRole("radio", { name: /^Unfiled/ }));
    expect(onChange).toHaveBeenCalledWith({ kind: "unfiled" });
    await userEvent.click(screen.getByRole("radio", { name: /^All/ }));
    expect(onChange).toHaveBeenLastCalledWith({ kind: "all" });
  });
});
