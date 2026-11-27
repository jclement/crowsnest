// The manual cannot rot silently (DESIGN.md 12.10). Every page is in the contents; every link
// between pages, every heading anchor, every "open the thing" button and every picture resolves;
// every contextual "?" and hint in the app names a section that exists; links out to the
// repository's files point at files that exist; every flag the prose names is a real flag (or a
// known subcommand's); and the generated keyboard shortcuts page matches the app's own table.
import * as fs from "node:fs";
import * as path from "node:path";
import { describe, expect, it } from "vitest";
import { appTarget, classifyLink, CONTENTS_ID, linksIn, searchManual } from "../lib/manual";
import { loadManual, manualFiles, manualImages } from "./content";
import { shortcutsPage } from "./shortcutsPage";

const repo = path.resolve(__dirname, "../../..");
const manualDir = path.join(repo, "docs/manual");
const { manual, index } = loadManual();

function allFiles(dir: string, ext: RegExp): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) return allFiles(full, ext);
    return ext.test(entry.name) ? [full] : [];
  });
}

describe("the manual's files", () => {
  it("bundles every markdown file under docs/manual", () => {
    const onDisk = allFiles(manualDir, /\.md$/).length;
    expect(Object.keys(manualFiles).length).toBe(onDisk);
    expect(manual.pages.size).toBe(onDisk);
  });

  it("lists every page in the contents, and only pages that exist", () => {
    const listed = new Set(manual.parts.flatMap((part) => part.pages));
    const missing = [...manual.pages.keys()].filter((id) => id !== CONTENTS_ID && !listed.has(id));
    expect(missing, "pages not in docs/manual/README.md").toEqual([]);
    const dangling = [...listed].filter((id) => !manual.pages.has(id));
    expect(dangling, "contents entries with no page").toEqual([]);
  });

  it("gives every page exactly one title", () => {
    for (const page of manual.pages.values()) {
      expect(page.headings.filter((heading) => heading.depth === 1).length, page.id).toBe(1);
    }
  });
});

describe("links in the manual", () => {
  const problems: string[] = [];
  for (const page of manual.pages.values()) {
    for (const { href, image } of linksIn(page.markdown)) {
      const where = `${page.id}: ${href}`;
      if (image) {
        const name = href.replace(/^(\.\.\/)+/, "").replace(/^\.\//, "");
        if (!manual.images.has(name)) problems.push(`${where}: no such picture in docs/manual/img`);
        continue;
      }
      const link = classifyLink(page.id, href);
      switch (link.kind) {
        case "page": {
          const target = manual.pages.get(link.id);
          if (!target) problems.push(`${where}: no such page`);
          else if (link.hash && !target.headings.some((heading) => heading.slug === link.hash)) problems.push(`${where}: no heading #${link.hash} there`);
          break;
        }
        case "anchor":
          if (!page.headings.some((heading) => heading.slug === link.hash)) problems.push(`${where}: no heading #${link.hash} on this page`);
          break;
        case "app":
          break;
        case "external":
          break;
        default: {
          if (href.startsWith("app:")) {
            problems.push(`${where}: not a view or sheet the app has`);
            break;
          }
          // A link out to the repository (../../DESIGN.md): the file must exist.
          const file = path.resolve(path.dirname(path.join(manualDir, page.id)), href.split("#")[0]!);
          if (!fs.existsSync(file)) problems.push(`${where}: no such file in the repository`);
        }
      }
    }
  }

  it("all resolve", () => {
    expect(problems).toEqual([]);
  });

  it("include at least one open-the-thing button per how-to page", () => {
    const howTo = [...manual.pages.values()].filter((page) => page.id.startsWith("how-to/"));
    expect(howTo.length).toBeGreaterThan(5);
    for (const page of howTo) {
      const opens = linksIn(page.markdown).filter((link) => link.href.startsWith("app:"));
      expect(opens.length, `${page.id} has no "open" button`).toBeGreaterThan(0);
      for (const open of opens) expect(appTarget(open.href), `${page.id}: ${open.href}`).not.toBeNull();
    }
  });
});

describe("pictures", () => {
  it("are all used, and small", () => {
    const used = new Set<string>();
    for (const page of manual.pages.values()) {
      for (const { href, image } of linksIn(page.markdown)) if (image) used.add(href.replace(/^(\.\.\/)+/, "").replace(/^\.\//, ""));
    }
    const files = allFiles(path.join(manualDir, "img"), /\.(png|jpg|svg)$/);
    expect(Object.keys(manualImages).length).toBe(files.length);
    for (const file of files) {
      const name = `img/${path.basename(file)}`;
      expect(used.has(name), `${name} is not used by any page`).toBe(true);
      expect(fs.statSync(file).size, `${name} is over 250 KB`).toBeLessThan(250_000);
    }
  });
});

describe("contextual help in the app", () => {
  // Every topic the app names: topic="…" attributes, openHelp("…") calls, and topic strings in
  // maps (lib/jobs.ts causeHelpTopic), anywhere in the app's source but the manual's own files.
  const sources = allFiles(path.join(repo, "web/src"), /\.tsx?$/).filter((file) => !/\.test\.tsx?$/.test(file) && !file.includes(`${path.sep}manual${path.sep}`) && !file.endsWith("lib/manual.ts"));
  const topics = new Map<string, string>();
  for (const file of sources) {
    const text = fs.readFileSync(file, "utf8");
    for (const match of text.matchAll(/["`]((?:concepts|how-to|reference|troubleshooting)\/[a-z0-9-]+(?:#[a-z0-9-]+)?|start-here(?:#[a-z0-9-]+)?|safety(?:#[a-z0-9-]+)?)["`]/g)) {
      topics.set(match[1]!, path.relative(repo, file));
    }
  }

  it("is used on the views and cards the manual promises", () => {
    expect(topics.size).toBeGreaterThanOrEqual(15);
  });

  it("names only sections that exist", () => {
    const bad: string[] = [];
    for (const [topic, file] of topics) {
      const [id = "", hash = ""] = topic.split("#");
      const page = manual.pages.get(id);
      if (!page) bad.push(`${file}: ${topic}: no such page`);
      else if (hash && !page.headings.some((heading) => heading.slug === hash)) bad.push(`${file}: ${topic}: no such heading`);
    }
    expect(bad).toEqual([]);
  });
});

describe("generated reference pages", () => {
  it("keyboard shortcuts match the app's own table", () => {
    const file = path.join(manualDir, "reference/keyboard-shortcuts.md");
    const want = shortcutsPage();
    if (process.env.UPDATE_MANUAL === "1") fs.writeFileSync(file, want);
    expect(fs.readFileSync(file, "utf8"), "run UPDATE_MANUAL=1 npx vitest run src/manual").toBe(want);
  });

  it("every flag the prose names is a real flag or a known subcommand's", () => {
    const settings = fs.readFileSync(path.join(manualDir, "reference/settings.md"), "utf8");
    const real = new Set([...settings.matchAll(/`(--[a-z0-9-]+)`/g)].map((match) => match[1]!));
    // Flags of subcommands (crowsnest ask, job, doctor, backup, …) and of other tools the pages show.
    const subcommand = new Set(
      "--wait --timeout --attach --file --urgency --job --socket --token-file --rules --state --reason --waiting --result --result-file --verified --not-verified --dir --role --done-when --isolate --json --deep --force --until --status --check --walkthrough-file --no-commits-reason --recheck-in --day --out --bg --resume --settings --mcp-config --append-system-prompt-file --session-id --remote-control --permission-mode --help --name --rm --volume -- --publish --env --dangerously-skip-permissions --model --hard --verify".split(" "),
    );
    const unknown: string[] = [];
    for (const page of manual.pages.values()) {
      if (page.id === "reference/settings") continue;
      const prose = page.markdown.replace(/^```[\s\S]*?^```/gm, "");
      for (const match of prose.matchAll(/`[^`]*?(--[a-z][a-z0-9-]*)[^`]*`/g)) {
        const flag = match[1]!;
        if (!real.has(flag) && !subcommand.has(flag)) unknown.push(`${page.id}: ${flag}`);
      }
    }
    expect(unknown).toEqual([]);
  });
});

describe("search", () => {
  it("answers a question with the page about it", () => {
    const hits = searchManual(index, "how do I land a job");
    expect(hits[0]?.pageId).toBe("how-to/review-and-land");
  });

  it("finds a stuck agent's rescue", () => {
    expect(searchManual(index, "agent stopped").map((hit) => hit.pageId)).toContain("troubleshooting/agent-stopped");
  });

  it("finds what a held job is", () => {
    expect(searchManual(index, "held job").length).toBeGreaterThan(0);
  });
});
