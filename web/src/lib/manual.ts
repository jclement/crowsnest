// The built-in user manual's model (DESIGN.md 12.11): pages are the markdown files under
// docs/manual (so the same text reads on GitHub), bundled into the web app by
// src/manual/content.ts. Everything here is pure: the contents (parsed from docs/manual/README.md,
// the one list of pages), headings and their anchors (GitHub's slugs, so a deep link works in both
// places), where a link inside a page goes, a section's text for the contextual help sheet, and the
// client-side search the Help view and the command palette share.
import { isSettingsSection } from "./settingsSections";
import { PALETTE_SHEETS, type PaletteSheet } from "./palette";

export interface Heading {
  depth: number;
  text: string;
  /** The anchor, as GitHub makes it (lower case, punctuation dropped, spaces to dashes). */
  slug: string;
}

export interface ManualPage {
  /** The path under docs/manual without ".md": "concepts/jobs". The contents page is "README". */
  id: string;
  /** The page's h1. */
  title: string;
  markdown: string;
  headings: Heading[];
}

export interface ContentsPart {
  title: string;
  /** Page ids, in reading order. */
  pages: string[];
}

export interface Manual {
  pages: Map<string, ManualPage>;
  parts: ContentsPart[];
  /** Every page in reading order (the contents page first), for previous and next. */
  order: string[];
  /** Image file name ("img/now-1440.png") to the URL the build gave it. */
  images: Map<string, string>;
}

/** The contents page: docs/manual/README.md, which GitHub shows for the folder. */
export const CONTENTS_ID = "README";

// ---- Headings ------------------------------------------------------------------------------

/** GitHub's heading anchor: lower case, everything but letters, digits, spaces, - and _ dropped. */
export function slugify(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s_-]/gu, "")
    .replace(/\s/g, "-");
}

/** Gives repeated slugs GitHub's -1, -2 suffixes. One per document. */
export function slugger(): (text: string) => string {
  const seen = new Map<string, number>();
  return (text) => {
    const base = slugify(text);
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    return count === 0 ? base : `${base}-${count}`;
  };
}

/** Inline markdown reduced to the text GitHub slugs: code ticks, emphasis and link targets gone. */
export function plainInline(text: string): string {
  return text
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/(\*\*|__)(.*?)\1/g, "$2")
    .replace(/(\*|_)(.*?)\1/g, "$2")
    .trim();
}

/** The lines outside fenced code blocks, with their index, so headings inside code are skipped. */
function proseLines(markdown: string): Array<{ line: string; index: number }> {
  const out: Array<{ line: string; index: number }> = [];
  let fence: string | null = null;
  markdown.split("\n").forEach((line, index) => {
    const open = /^\s*(```+|~~~+)/.exec(line);
    if (open) {
      const marker = open[1]!;
      if (fence === null) fence = marker[0]!.repeat(marker.length);
      else if (line.trim().startsWith(fence)) fence = null;
      return;
    }
    if (fence === null) out.push({ line, index });
  });
  return out;
}

export function parseHeadings(markdown: string): Heading[] {
  const slug = slugger();
  const headings: Heading[] = [];
  for (const { line } of proseLines(markdown)) {
    const match = /^(#{1,6})\s+(.+?)\s*#*\s*$/.exec(line);
    if (!match) continue;
    const text = plainInline(match[2]!);
    headings.push({ depth: match[1]!.length, text, slug: slug(text) });
  }
  return headings;
}

export function makePage(id: string, markdown: string): ManualPage {
  const headings = parseHeadings(markdown);
  const title = headings.find((heading) => heading.depth === 1)?.text ?? id;
  return { id, title, markdown, headings };
}

// ---- Contents -----------------------------------------------------------------------------

/**
 * The parts and their pages from the contents page: each "## Part" heading, then the page links
 * under it ("- [Jobs](concepts/jobs.md)"). Links to headings and outside the manual are ignored.
 */
export function parseContents(markdown: string): ContentsPart[] {
  const parts: ContentsPart[] = [];
  for (const { line } of proseLines(markdown)) {
    const heading = /^##\s+(.+?)\s*$/.exec(line);
    if (heading) {
      parts.push({ title: plainInline(heading[1]!), pages: [] });
      continue;
    }
    const link = /^\s*[-*]\s+\[[^\]]+\]\(([^)#\s]+\.md)\)/.exec(line);
    const part = parts[parts.length - 1];
    if (link && part) {
      const target = resolvePath(CONTENTS_ID, link[1]!);
      if (target && !part.pages.includes(target)) part.pages.push(target);
    }
  }
  return parts.filter((part) => part.pages.length > 0);
}

export function buildManual(files: Record<string, string>, images: Record<string, string> = {}): Manual {
  const pages = new Map<string, ManualPage>();
  for (const [path, markdown] of Object.entries(files)) {
    const id = pageIdFromPath(path);
    if (id) pages.set(id, makePage(id, markdown));
  }
  const contents = pages.get(CONTENTS_ID);
  const parts = contents ? parseContents(contents.markdown) : [];
  const order = [CONTENTS_ID, ...parts.flatMap((part) => part.pages)].filter((id, index, all) => pages.has(id) && all.indexOf(id) === index);
  const imageMap = new Map<string, string>();
  for (const [path, url] of Object.entries(images)) {
    const at = path.lastIndexOf("/img/");
    if (at >= 0) imageMap.set(path.slice(at + 1), url);
  }
  return { pages, parts, order, images: imageMap };
}

/** "../../../docs/manual/concepts/jobs.md" (a glob key) → "concepts/jobs". */
export function pageIdFromPath(path: string): string | null {
  const at = path.lastIndexOf("docs/manual/");
  const rest = at >= 0 ? path.slice(at + "docs/manual/".length) : path;
  if (!rest.endsWith(".md")) return null;
  return rest.slice(0, -3);
}

export function neighbours(manual: Manual, id: string): { previous?: ManualPage; next?: ManualPage } {
  const at = manual.order.indexOf(id);
  if (at < 0) return {};
  const previousId = manual.order[at - 1];
  const nextId = manual.order[at + 1];
  return { previous: previousId ? manual.pages.get(previousId) : undefined, next: nextId ? manual.pages.get(nextId) : undefined };
}

/** Which part a page is in, for the breadcrumb. */
export function partOf(manual: Manual, id: string): ContentsPart | undefined {
  return manual.parts.find((part) => part.pages.includes(id));
}

// ---- Links --------------------------------------------------------------------------------

/** The views an "open the thing" link may open (app:/jobs). Keep in step with router.tsx. */
export const APP_ROUTES = ["/", "/jobs", "/projects", "/chat", "/inbox", "/agents", "/fleet", "/terminal", "/system", "/settings", "/help"] as const;
/** The sheets one may open (app:sheet/settings): exactly the palette's. */
export const APP_SHEETS = PALETTE_SHEETS;

export type AppSheet = PaletteSheet;

export type AppTarget = { kind: "route"; to: string; search: Record<string, string>; hash?: string } | { kind: "sheet"; sheet: AppSheet };

/**
 * An "open the thing" link: app:/projects, app:/jobs?project=unfiled, app:sheet/settings, and a
 * section of a view: app:/settings#guard (a Settings section must exist, lib/settingsSections.ts).
 */
export function appTarget(href: string): AppTarget | null {
  if (!href.startsWith("app:")) return null;
  const rest = href.slice(4);
  if (rest.startsWith("sheet/")) {
    const sheet = rest.slice(6);
    return (APP_SHEETS as readonly string[]).includes(sheet) ? { kind: "sheet", sheet: sheet as AppSheet } : null;
  }
  const [beforeHash = "", hash] = rest.split("#", 2);
  const [path = "", query = ""] = beforeHash.split("?", 2);
  const known = (APP_ROUTES as readonly string[]).includes(path) || /^\/projects\/[a-z0-9-]{1,32}$/.test(path);
  if (!known) return null;
  if (hash !== undefined && (path !== "/settings" || !isSettingsSection(hash))) return null;
  return { kind: "route", to: path, search: Object.fromEntries(new URLSearchParams(query)), ...(hash ? { hash } : {}) };
}

/** A relative path from one page to another file, resolved to a page id or an image name. */
export function resolvePath(fromId: string, href: string): string | null {
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("/")) return null;
  const parts = fromId.split("/").slice(0, -1);
  for (const piece of href.split("/")) {
    if (piece === "" || piece === ".") continue;
    if (piece === "..") {
      if (parts.length === 0) return null;
      parts.pop();
    } else parts.push(piece);
  }
  const joined = parts.join("/");
  return joined.endsWith(".md") ? joined.slice(0, -3) : joined;
}

export type ManualLink =
  | { kind: "page"; id: string; hash: string }
  | { kind: "app"; target: AppTarget }
  | { kind: "external"; href: string }
  | { kind: "anchor"; hash: string }
  /** Anything else: a relative link out of the manual (to the repository's files) or a bad one. */
  | { kind: "other"; href: string };

/** Where a link written in page fromId goes. */
export function classifyLink(fromId: string, href: string): ManualLink {
  if (href.startsWith("#")) return { kind: "anchor", hash: href.slice(1) };
  if (href.startsWith("app:")) {
    const target = appTarget(href);
    return target ? { kind: "app", target } : { kind: "other", href };
  }
  if (/^(https?:|mailto:)/i.test(href)) return { kind: "external", href };
  const [path = "", hash = ""] = href.split("#", 2);
  if (path.endsWith(".md")) {
    const id = resolvePath(fromId, path);
    if (id !== null) return { kind: "page", id, hash };
  }
  return { kind: "other", href };
}

/** The in-app path of a page (and heading). */
export function helpPath(id: string, hash = ""): string {
  const base = id === CONTENTS_ID ? "/help" : `/help/${id}`;
  return hash ? `${base}#${hash}` : base;
}

/** A topic "concepts/jobs#stuck" split into its page and anchor. */
export function parseTopic(topic: string): { id: string; hash: string } {
  const [id = "", hash = ""] = topic.split("#", 2);
  return { id, hash };
}

/** Every link target written in a page's markdown (outside code), with images marked. */
export function linksIn(markdown: string): Array<{ href: string; image: boolean }> {
  const links: Array<{ href: string; image: boolean }> = [];
  for (const { line } of proseLines(markdown)) {
    const stripped = line.replace(/`[^`]*`/g, "");
    for (const match of stripped.matchAll(/(!?)\[(?:[^\]]|\][^(])*?\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
      links.push({ href: match[2]!, image: match[1] === "!" });
    }
  }
  return links;
}

// ---- Sections -----------------------------------------------------------------------------

/**
 * The markdown of one section: from its heading to the next heading of the same depth or above.
 * An unknown or empty slug gives the whole page.
 */
export function sectionMarkdown(page: ManualPage, slug: string): { heading?: Heading; markdown: string } {
  const heading = page.headings.find((entry) => entry.slug === slug);
  if (!slug || !heading) return { markdown: page.markdown };
  const lines = page.markdown.split("\n");
  const prose = proseLines(page.markdown);
  const slugOf = slugger();
  let start = -1;
  let end = lines.length;
  for (const { line, index } of prose) {
    const match = /^(#{1,6})\s+(.+?)\s*#*\s*$/.exec(line);
    if (!match) continue;
    const depth = match[1]!.length;
    const thisSlug = slugOf(plainInline(match[2]!));
    if (start < 0) {
      if (thisSlug === slug) start = index;
      continue;
    }
    if (depth <= heading.depth) {
      end = index;
      break;
    }
  }
  return { heading, markdown: lines.slice(start + 1, end).join("\n").trim() };
}

// ---- Search -------------------------------------------------------------------------------

export interface SearchEntry {
  pageId: string;
  pageTitle: string;
  /** The section's anchor; "" for the page's lead (before its first h2). */
  slug: string;
  heading: string;
  /** The section's plain text, lower case, for matching and the snippet. */
  text: string;
  /** Stemmed words of the heading and the page title, and of the body. */
  headWords: Set<string>;
  bodyWords: Map<string, number>;
}

export interface SearchHit {
  pageId: string;
  pageTitle: string;
  slug: string;
  heading: string;
  snippet: string;
  score: number;
}

// Words a question is made of that say nothing about what it is about.
const STOPWORDS = new Set(
  "a an and are as at be by can could do does did for from get got has have how i if in into is it its me my of on or should so that the their them then there these this to use using want was what when where which who why will with would you your about after again any before being both each few further here just more most no nor not only other our out over same some such than too under until up very s t don doesn".split(" "),
);

/** A crude stem: plurals and -ing/-ed endings off, so "landing" finds "land" and "jobs" "job". */
export function stem(word: string): string {
  let out = word.toLowerCase();
  if (out.length > 5 && out.endsWith("ing")) out = out.slice(0, -3);
  else if (out.length > 4 && out.endsWith("ed")) out = out.slice(0, -2);
  else if (out.length > 4 && out.endsWith("ies")) out = out.slice(0, -3) + "y";
  else if (out.length > 3 && out.endsWith("es") && /(sh|ch|x|ss)es$/.test(out)) out = out.slice(0, -2);
  else if (out.length > 3 && out.endsWith("s") && !out.endsWith("ss")) out = out.slice(0, -1);
  if (out.length > 4 && /(.)\1$/.test(out)) out = out.slice(0, -1);
  return out;
}

export function searchTerms(query: string): string[] {
  const words = query.toLowerCase().match(/[\p{L}\p{N}][\p{L}\p{N}_-]*/gu) ?? [];
  const terms = words.filter((word) => !STOPWORDS.has(word)).map(stem);
  return [...new Set(terms)];
}

function wordsOf(text: string): string[] {
  return (text.toLowerCase().match(/[\p{L}\p{N}][\p{L}\p{N}_-]*/gu) ?? []).map(stem);
}

/** Markdown reduced to searchable prose: code fences kept as words, links' text kept, syntax gone. */
export function plainText(markdown: string): string {
  return markdown
    .replace(/^\s*(```+|~~~+).*$/gm, " ")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, " ")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/[`*_>#|]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

export function buildIndex(manual: Manual): SearchEntry[] {
  const entries: SearchEntry[] = [];
  for (const id of manual.order) {
    const page = manual.pages.get(id);
    if (!page || id === CONTENTS_ID) continue;
    const lines = page.markdown.split("\n");
    const prose = proseLines(page.markdown);
    const slugOf = slugger();
    // Cut the page at every h2 and h3.
    const cuts: Array<{ index: number; heading: string; slug: string }> = [{ index: 0, heading: page.title, slug: "" }];
    for (const { line, index } of prose) {
      const match = /^(#{1,6})\s+(.+?)\s*#*\s*$/.exec(line);
      if (!match) continue;
      const text = plainInline(match[2]!);
      const slug = slugOf(text);
      if (match[1]!.length >= 2 && match[1]!.length <= 3) cuts.push({ index, heading: text, slug });
    }
    cuts.forEach((cut, at) => {
      const end = cuts[at + 1]?.index ?? lines.length;
      const body = plainText(lines.slice(cut.index + 1, end).join("\n"));
      const bodyWords = new Map<string, number>();
      for (const word of wordsOf(body)) bodyWords.set(word, (bodyWords.get(word) ?? 0) + 1);
      entries.push({
        pageId: id,
        pageTitle: page.title,
        slug: cut.slug,
        heading: cut.heading,
        text: body,
        headWords: new Set([...wordsOf(cut.heading), ...wordsOf(page.title)]),
        bodyWords,
      });
    });
  }
  return entries;
}

function snippetFor(text: string, terms: string[]): string {
  const lower = text.toLowerCase();
  let at = -1;
  for (const term of terms) {
    const found = lower.search(new RegExp(`\\b${term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`));
    if (found >= 0 && (at < 0 || found < at)) at = found;
  }
  const start = Math.max(0, at - 40);
  const cut = text.slice(start, start + 140).trim();
  return (start > 0 ? "…" : "") + cut + (start + 140 < text.length ? "…" : "");
}

/**
 * Sections that match a query, best first. Every term must appear in the section (its heading,
 * its page's title or its body); heading words count most. A question ("how do I land a job")
 * is reduced to its terms first.
 */
export function searchManual(index: SearchEntry[], query: string, limit = 8): SearchHit[] {
  const terms = searchTerms(query);
  if (terms.length === 0) return [];
  const hits: SearchHit[] = [];
  for (const entry of index) {
    let score = 0;
    let all = true;
    for (const term of terms) {
      const inHead = [...entry.headWords].some((word) => word === term || (term.length >= 3 && word.startsWith(term)));
      let body = entry.bodyWords.get(term) ?? 0;
      if (!body && term.length >= 3) {
        for (const [word, count] of entry.bodyWords) if (word.startsWith(term)) body += count;
      }
      if (!inHead && !body) {
        all = false;
        break;
      }
      score += (inHead ? 6 : 0) + Math.min(body, 6);
    }
    if (!all) continue;
    // A page's own lead section is the best answer when the page is about the terms.
    if (entry.slug === "") score += 1;
    hits.push({ pageId: entry.pageId, pageTitle: entry.pageTitle, slug: entry.slug, heading: entry.heading, snippet: snippetFor(entry.text, terms), score });
  }
  hits.sort((a, b) => b.score - a.score || a.pageTitle.localeCompare(b.pageTitle));
  return hits.slice(0, limit);
}

/** Whether a palette query reads as a question to the manual ("how do I…", "what is…", "…?"). */
export function looksLikeQuestion(query: string): boolean {
  const text = query.trim().toLowerCase();
  return /\?$/.test(text) || /^(how|what|why|when|where|which|who|can|should|is|does|do)\b/.test(text) || /^help\b/.test(text);
}
