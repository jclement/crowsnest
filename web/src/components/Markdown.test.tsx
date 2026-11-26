import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Markdown } from "./Markdown";

const mermaidMock = vi.hoisted(() => ({
  initialize: vi.fn(),
  render: vi.fn(async () => ({ svg: '<svg data-testid="diagram"><text>A to B</text></svg>' })),
}));
vi.mock("mermaid", () => ({ default: mermaidMock }));

afterEach(() => {
  delete (window as { pwned?: boolean }).pwned;
});

describe("Markdown safety", () => {
  it("does not render or run a script tag", () => {
    const { container } = render(<Markdown>{"Hello\n\n<script>window.pwned = true</script>\n\nafter"}</Markdown>);
    expect(container.querySelector("script")).toBeNull();
    expect((window as { pwned?: boolean }).pwned).toBeUndefined();
    expect(screen.getByText("after")).toBeInTheDocument();
  });

  it("does not render an onerror attribute or the element carrying it", () => {
    const { container } = render(<Markdown>{'Look: <img src="x" onerror="window.pwned = true"> done'}</Markdown>);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("[onerror]")).toBeNull();
    expect(container.innerHTML).not.toContain("onerror");
    expect((window as { pwned?: boolean }).pwned).toBeUndefined();
  });

  it("drops inline HTML inside a paragraph", () => {
    const { container } = render(<Markdown>{"a <b onclick=\"x()\">bold</b> word"}</Markdown>);
    expect(container.querySelector("b")).toBeNull();
    expect(container.querySelector("[onclick]")).toBeNull();
  });

  it("strips javascript: links and opens real links safely", () => {
    render(<Markdown>{"[bad](javascript:alert(1)) and [good](https://example.com)"}</Markdown>);
    expect(screen.getByText("bad").closest("a")?.getAttribute("href") ?? "").not.toMatch(/javascript/i);
    const good = screen.getByRole("link", { name: "good" });
    expect(good).toHaveAttribute("href", "https://example.com");
    expect(good).toHaveAttribute("rel", "noopener noreferrer");
    expect(good).toHaveAttribute("target", "_blank");
  });

  it("shows HTML inside code as text", () => {
    const { container } = render(<Markdown>{"```html\n<script>alert(1)</script>\n```"}</Markdown>);
    expect(container.querySelector("pre code")?.textContent).toBe("<script>alert(1)</script>\n");
    expect(document.querySelector("script")).toBeNull();
  });
});

describe("Markdown rendering", () => {
  it("renders GFM tables and task lists", () => {
    render(<Markdown>{"| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] done\n- [ ] todo"}</Markdown>);
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getAllByRole("checkbox")).toHaveLength(2);
  });

  it("highlights fenced code", () => {
    const { container } = render(<Markdown>{"```go\nfunc main() {}\n```"}</Markdown>);
    expect(container.querySelector("code.hljs")).not.toBeNull();
    expect(container.querySelector(".hljs-keyword")).toHaveTextContent("func");
  });

  it("renders mermaid fences lazily with securityLevel strict", async () => {
    render(<Markdown>{"```mermaid\ngraph LR\n  A --> B\n```"}</Markdown>);
    expect(await screen.findByTestId("diagram")).toBeInTheDocument();
    expect(mermaidMock.initialize).toHaveBeenCalledWith(expect.objectContaining({ securityLevel: "strict", startOnLoad: false }));
    expect(mermaidMock.render).toHaveBeenCalledWith(expect.any(String), "graph LR\n  A --> B\n");
  });

  it("shows the source when a diagram fails", async () => {
    mermaidMock.render.mockRejectedValueOnce(new Error("Parse error on line 1"));
    render(<Markdown>{"```mermaid\nnot a diagram\n```"}</Markdown>);
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Parse error on line 1"));
    expect(screen.getByText("not a diagram")).toBeInTheDocument();
  });
});
