import { describe, expect, it } from "vitest";
import {
  appTarget,
  buildIndex,
  buildManual,
  classifyLink,
  helpPath,
  linksIn,
  looksLikeQuestion,
  neighbours,
  parseContents,
  parseHeadings,
  searchManual,
  searchTerms,
  sectionMarkdown,
  slugify,
} from "./manual";
import { manualItem, withManual, type PaletteGroup } from "./palette";

const files = {
  "../../../docs/manual/README.md": "# Manual\n\n## Start\n\n- [Start here](start-here.md)\n\n## How to\n\n- [Land](how-to/land.md)\n- [Rescue](how-to/rescue.md)\n",
  "../../../docs/manual/start-here.md": "# Start here\n\nWelcome.\n\n## A first session\n\nGive a job.\n",
  "../../../docs/manual/how-to/land.md":
    "# Review and land a job\n\nMerge the branch.\n\n## Land it\n\nPress **Merge**.\n\n### Confirm\n\nYes, merge.\n\n## What can go wrong\n\nA conflict.\n\n```sh\n# not a heading\n```\n",
  "../../../docs/manual/how-to/rescue.md": "# Rescue a stuck agent\n\nNudge it. See [landing](land.md#land-it) and [Jobs](app:/jobs).\n\n![x](../img/a.png)\n",
};

describe("headings", () => {
  it("slugs like GitHub", () => {
    expect(slugify("What \"accepted\" proves, and what it does not")).toBe("what-accepted-proves-and-what-it-does-not");
    expect(slugify("The fleet's usage limit")).toBe("the-fleets-usage-limit");
    expect(slugify("Speak mode vs the mic")).toBe("speak-mode-vs-the-mic");
  });

  it("skips headings inside code and numbers repeats", () => {
    const headings = parseHeadings("# A\n\n## B\n\n```\n## not\n```\n\n## B\n");
    expect(headings.map((heading) => heading.slug)).toEqual(["a", "b", "b-1"]);
  });
});

describe("the contents", () => {
  it("reads parts and pages in order", () => {
    expect(parseContents(files["../../../docs/manual/README.md"])).toEqual([
      { title: "Start", pages: ["start-here"] },
      { title: "How to", pages: ["how-to/land", "how-to/rescue"] },
    ]);
  });

  it("orders previous and next by the contents", () => {
    const manual = buildManual(files, { "../../../docs/manual/img/a.png": "/assets/a-123.png" });
    expect(manual.order).toEqual(["README", "start-here", "how-to/land", "how-to/rescue"]);
    expect(neighbours(manual, "how-to/land").previous?.id).toBe("start-here");
    expect(neighbours(manual, "how-to/land").next?.id).toBe("how-to/rescue");
    expect(manual.images.get("img/a.png")).toBe("/assets/a-123.png");
  });
});

describe("links", () => {
  it("resolve between pages, to headings, to the app and out", () => {
    expect(classifyLink("how-to/rescue", "land.md#land-it")).toEqual({ kind: "page", id: "how-to/land", hash: "land-it" });
    expect(classifyLink("how-to/rescue", "../concepts/jobs.md")).toEqual({ kind: "page", id: "concepts/jobs", hash: "" });
    expect(classifyLink("how-to/rescue", "#nudge")).toEqual({ kind: "anchor", hash: "nudge" });
    expect(classifyLink("how-to/rescue", "https://herdr.dev")).toEqual({ kind: "external", href: "https://herdr.dev" });
    expect(classifyLink("how-to/rescue", "../../DESIGN.md").kind).toBe("other");
    expect(classifyLink("how-to/rescue", "app:/nowhere").kind).toBe("other");
  });

  it("knows the app's views and sheets", () => {
    expect(appTarget("app:/jobs?project=unfiled")).toEqual({ kind: "route", to: "/jobs", search: { project: "unfiled" } });
    expect(appTarget("app:/projects/crowsnest")).toEqual({ kind: "route", to: "/projects/crowsnest", search: {} });
    expect(appTarget("app:sheet/settings")).toEqual({ kind: "sheet", sheet: "settings" });
    expect(appTarget("app:sheet/bogus")).toBeNull();
    expect(appTarget("app:/admin")).toBeNull();
  });

  it("finds every link and picture outside code", () => {
    expect(linksIn("See [a](a.md) and ![b](img/b.png). `[c](c.md)`\n```\n[d](d.md)\n```")).toEqual([
      { href: "a.md", image: false },
      { href: "img/b.png", image: true },
    ]);
  });

  it("makes in-app paths", () => {
    expect(helpPath("README")).toBe("/help");
    expect(helpPath("how-to/land", "land-it")).toBe("/help/how-to/land#land-it");
  });
});

describe("sections", () => {
  const manual = buildManual(files);
  const page = manual.pages.get("how-to/land")!;

  it("cut from a heading to the next of its depth", () => {
    const section = sectionMarkdown(page, "land-it");
    expect(section.heading?.text).toBe("Land it");
    expect(section.markdown).toContain("Press **Merge**.");
    expect(section.markdown).toContain("### Confirm");
    expect(section.markdown).not.toContain("A conflict");
  });

  it("give the whole page for no or an unknown anchor", () => {
    expect(sectionMarkdown(page, "").markdown).toBe(page.markdown);
    expect(sectionMarkdown(page, "nope").heading).toBeUndefined();
  });
});

describe("search", () => {
  const manual = buildManual(files);
  const index = buildIndex(manual);

  it("reduces a question to its terms", () => {
    expect(searchTerms("How do I land a job?")).toEqual(["land", "job"]);
    expect(searchTerms("landing jobs")).toEqual(["land", "job"]);
  });

  it("answers a question with the page about it", () => {
    const hits = searchManual(index, "how do I land a job");
    expect(hits[0]?.pageId).toBe("how-to/land");
  });

  it("needs every term", () => {
    expect(searchManual(index, "merge conflict").map((hit) => hit.slug)).toEqual([]);
    expect(searchManual(index, "conflict")[0]?.slug).toBe("what-can-go-wrong");
  });

  it("tells a question from a search", () => {
    expect(looksLikeQuestion("how do I land")).toBe(true);
    expect(looksLikeQuestion("stuck jobs?")).toBe(true);
    expect(looksLikeQuestion("j_12")).toBe(false);
  });
});

describe("the palette's manual group", () => {
  const hit = { pageId: "how-to/land", pageTitle: "Review and land a job", slug: "land-it", heading: "Land it", snippet: "Press Merge.", score: 9 };
  const other: PaletteGroup = { id: "jobs", label: "Jobs", items: [] };

  it("opens the Help view at the heading", () => {
    expect(manualItem(hit).command).toEqual({ kind: "navigate", to: "/help/how-to/land", hash: "land-it" });
  });

  it("comes first for a question, last otherwise", () => {
    expect(withManual([other], [hit], true).map((group) => group.id)).toEqual(["manual", "jobs"]);
    expect(withManual([other], [hit], false).map((group) => group.id)).toEqual(["jobs", "manual"]);
    expect(withManual([other], [], true)).toEqual([other]);
  });
});
