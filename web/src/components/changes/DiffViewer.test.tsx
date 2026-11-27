import { screen, waitFor, within, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DiffFile, DiffSummary, FileView } from "../../lib/changes";
import { renderWithClient, stubApi, type Call } from "../../test/render";
import { DiffViewer } from "./DiffViewer";

function wideScreen(wide: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: wide && /min-width/.test(query),
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
}

const file = (path: string, extra: Partial<DiffFile> = {}): DiffFile => ({
  path, status: "modified", added: 1, deleted: 1, binary: false, old_blob: "a".repeat(40), new_blob: "b".repeat(40), hash: path + "-1",
  group: path.endsWith(".ts") ? "client" : "server", churn: false, viewed: false, changed_since_viewed: false, since_available: false, ...extra,
});

function summary(files: DiffFile[]): DiffSummary {
  return {
    subject: "j_7", kind: "job", label: "j_7: Checkout", root: "/w", job_id: "j_7", scope: "all", base: "c".repeat(40), base_label: "where it left main",
    base_ref: "", head: "d".repeat(40), commits: [], files, added: 2, deleted: 2, viewed: files.filter((f) => f.viewed).length, total: files.length,
    truncated: false, computed_at: "2026-11-27T10:00:00Z", revision: "r",
    scopes: (["all", "committed", "uncommitted", "since_viewed"] as const).map((name) => ({ name, label: name, files: files.length, added: 2, deleted: 2 })),
  };
}

const cartView: FileView = {
  path: "web/cart.ts", status: "modified", hash: "web/cart.ts-1",
  diff: "@@ -1,2 +1,2 @@\n /* items */\n-const total = price * count;\n+const total = price * quantity;\n",
  binary: false, too_large: false, cut: false, since: false, context: 0,
  old_text: "/* items */\nconst total = price * count;\n",
  new_text: "/* items */\nconst total = price * quantity;\n",
};

function setup(files: DiffFile[], options: { onMark?: (call: Call) => DiffFile[] } = {}) {
  let current = files;
  const calls = stubApi((call) => {
    if (call.method === "PUT" && call.path.startsWith("/changes/jobs/j_7/viewed")) {
      current = options.onMark ? options.onMark(call) : current.map((entry) => ({ ...entry, viewed: true }));
      return { body: { data: summary(current) } };
    }
    if (call.method === "DELETE" && call.path.startsWith("/changes/jobs/j_7/viewed")) {
      current = current.map((entry) => ({ ...entry, viewed: false }));
      return { body: { data: summary(current) } };
    }
    if (call.path.startsWith("/changes/jobs/j_7/file")) {
      const params = new URLSearchParams(call.path.split("?")[1]);
      const path = params.get("path") ?? "";
      // The server serves the version asked for (R7-C12): the stub answers with the hash requested.
      const hash = params.get("hash") ?? cartView.hash;
      if (call.path.includes("since=1")) return { body: { data: { ...cartView, path, hash, since: true, diff: "@@ -2,1 +2,2 @@\n const total = price * quantity;\n+export const fresh = 1;\n", old_text: cartView.new_text, new_text: cartView.new_text + "export const fresh = 1;\n" } } };
      return { body: { data: { ...cartView, path, hash } } };
    }
    if (call.path.startsWith("/changes/jobs/j_7")) return { body: { data: summary(current) } };
    return undefined;
  });
  renderWithClient(<DiffViewer who={{ kind: "job", id: "j_7" }} />);
  return calls;
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("DiffViewer", () => {
  it("highlights both sides of a split diff and emphasises the changed words", async () => {
    wideScreen(true);
    setup([file("web/cart.ts")]);
    const table = await screen.findByRole("table", { name: "Diff of web/cart.ts" });
    const rows = within(table).getAllByRole("row");
    // The modified line: old on the left, new on the right, each with its keyword coloured.
    const changed = rows.find((row) => row.textContent?.includes("count") && row.textContent.includes("quantity"))!;
    expect(changed.querySelectorAll(".hljs-keyword")).toHaveLength(2);
    expect(Array.from(changed.querySelectorAll("mark")).map((mark) => mark.textContent)).toEqual(["count", "quantity"]);
    // A comment line is coloured too (from the whole file's highlight), and signs are spoken.
    expect(table.querySelector(".hljs-comment")?.textContent).toBe("/* items */");
    expect(within(changed).getByText("removed")).toHaveClass("sr-only");
    expect(within(changed).getByText("added")).toHaveClass("sr-only");
  });

  it("switches to unified and remembers it", async () => {
    wideScreen(true);
    setup([file("web/cart.ts")]);
    await screen.findByRole("table", { name: "Diff of web/cart.ts" });
    await userEvent.click(screen.getByRole("button", { name: "Split view" }));
    const table = await screen.findByRole("table", { name: "Diff of web/cart.ts" });
    expect(table.querySelector("colgroup")).toBeNull();
    expect(JSON.parse(localStorage.getItem("crowsnest-diff-prefs") ?? "{}")).toMatchObject({ split: false });
  });

  it("marks a file viewed with its hash, collapses it, and hide viewed takes it off the list", async () => {
    wideScreen(true);
    const calls = setup([file("web/cart.ts"), file("server/a.go")], {
      onMark: (call) => [file("web/cart.ts", { viewed: (call.body as { files: { path: string }[] }).files[0]?.path === "web/cart.ts" }), file("server/a.go")],
    });
    await screen.findByRole("table", { name: "Diff of web/cart.ts" });
    const section = screen.getByRole("region", { name: "web/cart.ts" });
    await userEvent.click(within(section).getByRole("checkbox", { name: "Viewed: web/cart.ts" }));
    await waitFor(() => expect(calls.find((call) => call.method === "PUT")?.body).toEqual({ scope: "all", files: [{ path: "web/cart.ts", hash: "web/cart.ts-1" }], all: false }));
    await waitFor(() => expect(within(section).queryByRole("table")).toBeNull());
    expect(await screen.findByText("1 of 2 viewed")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Hide viewed" }));
    const list = screen.getByRole("list", { name: "Files" });
    expect(within(list).queryByText("web/cart.ts")).toBeNull();
    expect(within(list).getByText("server/a.go")).toBeInTheDocument();
  });

  it("flags a file changed since it was viewed and shows only what changed since", async () => {
    wideScreen(true);
    const calls = setup([file("web/cart.ts", { changed_since_viewed: true, since_available: true, viewed_at: "2026-11-27T09:00:00Z" })]);
    const section = await screen.findByRole("region", { name: "web/cart.ts" });
    expect(within(section).getByText("changed since you viewed it")).toBeInTheDocument();
    await userEvent.click(within(section).getByRole("button", { name: "Show since viewed" }));
    expect(await within(section).findByText(/only the new edits/)).toBeInTheDocument();
    expect(calls.some((call) => call.path.includes("since=1"))).toBe(true);
    await waitFor(() => expect(within(section).getByRole("table").textContent).toContain("export const fresh = 1;"));
  });

  it("moves between files with j and k, and marks the current one with v", async () => {
    wideScreen(true);
    const calls = setup([file("server/a.go"), file("web/cart.ts")]);
    const list = await screen.findByRole("list", { name: "Files" });
    fireEvent.keyDown(window, { key: "j" });
    await waitFor(() => expect(within(list).getByRole("button", { name: /server\/a\.go/ })).toHaveAttribute("aria-current", "true"));
    fireEvent.keyDown(window, { key: "j" });
    await waitFor(() => expect(within(list).getByRole("button", { name: /web\/cart\.ts/ })).toHaveAttribute("aria-current", "true"));
    fireEvent.keyDown(window, { key: "k" });
    await waitFor(() => expect(within(list).getByRole("button", { name: /server\/a\.go/ })).toHaveAttribute("aria-current", "true"));
    fireEvent.keyDown(window, { key: "v" });
    await waitFor(() => expect((calls.find((call) => call.method === "PUT")?.body as { files: unknown[] }).files).toEqual([{ path: "server/a.go", hash: "server/a.go-1" }]));
  });

  it("on a phone shows one file at a time with Previous, Viewed and Next at the bottom", async () => {
    wideScreen(false);
    setup([file("server/a.go"), file("web/cart.ts")]);
    const nav = await screen.findByRole("navigation", { name: "Files" });
    expect(within(nav).getByText("1 / 2")).toBeInTheDocument();
    expect(screen.getAllByRole("region").filter((region) => region.getAttribute("data-file-section"))).toHaveLength(1);
    await userEvent.click(within(nav).getByRole("button", { name: "Next" }));
    expect(within(nav).getByText("2 / 2")).toBeInTheDocument();
    expect(within(nav).getByRole("checkbox", { name: "Viewed: web/cart.ts" })).toBeInTheDocument();
  });
});
