// The folder picker against a small fake tree served through the API stub: keyboard (arrows,
// Enter, Backspace, Cmd/Ctrl+Enter), the filter, a pasted path (and a refused one), the places,
// and the facts and warnings shown for the highlighted folder.
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BrowseEntry, FolderFacts, Listing } from "../../lib/browse";
import { renderWithClient, stubApi } from "../../test/render";
import { DirPicker } from "./DirPicker";

const HOME = "/h";

/** The tree: each folder's sub-folders; git repos marked. */
const tree: Record<string, Array<{ name: string; git?: boolean; dir?: boolean }>> = {
  "/h": [{ name: "Work" }, { name: "Developer" }, { name: "todo.txt", dir: false }],
  "/h/Developer": [{ name: "scratch" }, { name: "acme", git: true }],
  "/h/Developer/acme": [{ name: "cmd" }],
  "/h/Developer/scratch": [],
  "/h/Developer/acme/cmd": [],
  "/h/Work": [],
};

function expand(path: string): string {
  return path === "~" ? HOME : path.startsWith("~/") ? HOME + path.slice(1) : path.replace(/\/+$/, "") || "/";
}

function listing(path: string): Listing {
  const parts = path.split("/").filter(Boolean);
  const crumbs = [{ name: "/", path: "/", browsable: false }, ...parts.map((name, i) => {
    const at = `/${parts.slice(0, i + 1).join("/")}`;
    return { name: at === HOME ? "~" : name, path: at, browsable: at.startsWith(HOME) };
  })];
  // As the server answers by default: folders only, by name; files counted.
  const children = (tree[path] ?? []).filter((child) => child.dir !== false).sort((a, b) => a.name.localeCompare(b.name));
  const entries: BrowseEntry[] = children.map((child) => ({
    name: child.name, path: `${path}/${child.name}`, dir: child.dir !== false, git: !!child.git, symlink: false, hidden: false, mtime: "", readable: true,
  }));
  return {
    path, resolved: path, label: path.replace(HOME, "~"), root: { path: HOME, label: "Home", kind: "home" },
    parent: path === HOME ? "" : path.slice(0, path.lastIndexOf("/")), crumbs, entries, more: 0, hidden_count: 1, file_count: (tree[path] ?? []).length - children.length, partial: false, git: false,
  };
}

function factsFor(path: string): FolderFacts {
  const git = path === "/h/Developer/acme";
  return {
    path, resolved: path, label: path, name: path.split("/").pop() ?? "", git, worktree: false,
    branch: git ? "main" : undefined, default_branch: git ? "main" : undefined, host: git ? "github" : undefined,
    check_command: git ? "mise run check" : "", agents_md: git, claude_md: false, is_home: path === HOME, is_filesystem_root: false,
    git_top: path.startsWith("/h/Developer/acme/") ? "/h/Developer/acme" : undefined, project: null, contains: [],
  };
}

function stubTree() {
  return stubApi((call) => {
    const url = new URL(call.path, "http://x");
    const path = expand(url.searchParams.get("path") ?? "~");
    if (url.pathname === "/browse/places") {
      return { body: { data: { home: HOME, roots: [], favourites: [
        { path: HOME, label: "Home", kind: "home" },
        { path: "/h/Developer", label: "~/Developer", kind: "favourite" },
        { path: "/h/Work", label: "~/Work", kind: "favourite" },
      ] } } };
    }
    if (url.pathname === "/browse" || url.pathname === "/browse/facts") {
      if (!(path in tree)) {
        return { status: 403, body: { error: { code: "FORBIDDEN", message: "That folder is outside what the picker may browse." } } };
      }
      return { body: { data: url.pathname === "/browse" ? listing(path) : factsFor(path) } };
    }
    return undefined;
  });
}

function rows() {
  return within(screen.getByRole("list", { name: /Folders in/ })).getAllByRole("button", { name: /^(?!Open )/ }).map((row) => row.textContent);
}

async function openPicker(props: Partial<Parameters<typeof DirPicker>[0]> = {}) {
  const onSelect = vi.fn();
  const onClose = vi.fn();
  renderWithClient(<DirPicker open onClose={onClose} onSelect={onSelect} {...props} />);
  await screen.findByRole("list", { name: "Folders in ~" });
  return { onSelect, onClose, box: screen.getByLabelText("Filter folders, or paste a path") };
}

beforeEach(() => localStorage.clear());

describe("DirPicker", () => {
  it("starts at home with places and the folders, files counted, not listed", async () => {
    stubTree();
    await openPicker();
    expect(rows()).toEqual(["Developer", "Work"]);
    expect(within(screen.getAllByRole("navigation", { name: "Places" })[0]!).getByRole("button", { name: /~\/Developer/ })).toBeInTheDocument();
    expect(screen.getByText("1 hidden · 1 file")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Up one folder" })).toBeDisabled();
  });

  it("is driven from the keyboard: arrows, Enter, Backspace, Cmd/Ctrl+Enter", async () => {
    stubTree();
    const { onSelect, onClose, box } = await openPicker();
    expect(box).toHaveFocus();
    await userEvent.keyboard("{ArrowDown}{Enter}");
    await screen.findByRole("list", { name: "Folders in ~/Developer" });
    // The repository floats to the top and is badged.
    expect(rows()[0]).toMatch(/^acmegit/);
    await userEvent.keyboard("{ArrowDown}");
    expect(await screen.findByText("git repo on GitHub, branch main, has a check task, AGENTS.md present")).toBeInTheDocument();
    expect(screen.getByText("GitHub: Land can open pull requests here.")).toBeInTheDocument();
    await userEvent.keyboard("{Enter}");
    await screen.findByRole("list", { name: "Folders in ~/Developer/acme" });
    await userEvent.keyboard("{Backspace}");
    await screen.findByRole("list", { name: "Folders in ~/Developer" });
    // Back up, the folder we came from is highlighted again.
    await waitFor(() => expect(screen.getByRole("button", { name: /^acme/ })).toHaveAttribute("aria-current", "true"));
    await userEvent.keyboard("{Control>}{Enter}{/Control}");
    expect(onSelect).toHaveBeenCalledWith("/h/Developer/acme", expect.objectContaining({ git: true }));
    expect(onClose).toHaveBeenCalled();
  });

  it("filters the listing as you type", async () => {
    stubTree();
    const { box } = await openPicker();
    await userEvent.type(box, "wo");
    expect(rows()).toEqual(["Work"]);
    await userEvent.clear(box);
    await userEvent.type(box, "zzz");
    expect(screen.getByText(/Nothing here matches/)).toBeInTheDocument();
  });

  it("goes to a pasted path, and says why when one is refused", async () => {
    stubTree();
    const { box } = await openPicker();
    await userEvent.click(box);
    await userEvent.paste("~/Developer/scratch");
    expect(screen.getByRole("button", { name: /Go to ~\/Developer\/scratch/ })).toBeInTheDocument();
    await userEvent.keyboard("{Enter}");
    await screen.findByRole("list", { name: "Folders in ~/Developer/scratch" });
    expect(screen.getByText("No folders here.", { exact: false })).toBeInTheDocument();
    await userEvent.type(box, "/etc{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("outside what the picker may browse");
    expect(screen.getByRole("list", { name: "Folders in ~/Developer/scratch" })).toBeInTheDocument();
  });

  it("opens a place, and warns about the folder in view", async () => {
    stubTree();
    await openPicker();
    // At home with nothing highlighted, the folder in view is home itself.
    expect(await screen.findByText(/your home folder: too wide/)).toBeInTheDocument();
    await userEvent.click(within(screen.getAllByRole("navigation", { name: "Places" })[0]!).getByRole("button", { name: /~\/Work/ }));
    await screen.findByRole("list", { name: "Folders in ~/Work" });
    expect(await screen.findByText("Not a git repository: jobs here cannot be isolated in worktrees.")).toBeInTheDocument();
  });

  it("starts where the form's typed path points, and remembers what was chosen", async () => {
    stubTree();
    const onSelect = vi.fn();
    renderWithClient(<DirPicker open onClose={() => {}} onSelect={onSelect} initialPath="/h/Work" />);
    await screen.findByRole("list", { name: "Folders in ~/Work" });
    await userEvent.click(screen.getByRole("button", { name: "Select this folder" }));
    expect(onSelect.mock.calls[0]?.[0]).toBe("/h/Work"); // the facts ride along only when they have arrived
    expect(JSON.parse(localStorage.getItem("crowsnest-picker-recent") ?? "[]")).toEqual(["/h/Work"]);
  });

  it("falls back to home when the starting path is refused", async () => {
    stubTree();
    renderWithClient(<DirPicker open onClose={() => {}} onSelect={() => {}} initialPath="/etc" />);
    expect(await screen.findByRole("list", { name: "Folders in ~" })).toBeInTheDocument();
  });
});
