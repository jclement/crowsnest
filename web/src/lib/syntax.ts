// Syntax tokens per line for the diff viewer (DESIGN.md 6.20), from lowlight (highlight.js as a
// tree, the same engine rehype-highlight uses for markdown, so no second highlighter ships). The
// whole file is highlighted at once and then cut into lines, so a comment or string that spans
// lines is coloured correctly on both sides of a diff; the result is plain data rendered as text
// nodes, never HTML.
import type { Element, ElementContent, Root } from "hast";
import { common, createLowlight } from "lowlight";
import type { Token } from "./changes";

const lowlight = createLowlight(common);

/** The largest text highlighted as a whole; past it each line is highlighted alone. */
const WHOLE_LIMIT = 200_000;

/** Highlights text in language and returns its tokens line by line ([] per line when unknown). */
export function highlightLines(text: string, language: string): Token[][] {
  const lines = text.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  if (!language || !lowlight.registered(language) || text.length > WHOLE_LIMIT) {
    return lines.map((line) => (language && lowlight.registered(language) && line.length < 2000 ? highlightOne(line, language) : plain(line)));
  }
  let tree: Root;
  try {
    tree = lowlight.highlight(language, text);
  } catch {
    return lines.map(plain);
  }
  const out: Token[][] = [[]];
  walk(tree.children, [], out);
  while (out.length > lines.length) out.pop();
  while (out.length < lines.length) out.push([]);
  return out;
}

/** Highlights one line by itself (a diff whose file text is not available). */
export function highlightOne(line: string, language: string): Token[] {
  if (!language || !lowlight.registered(language)) return plain(line);
  try {
    const out: Token[][] = [[]];
    walk(lowlight.highlight(language, line).children, [], out);
    return out[0] ?? [];
  } catch {
    return plain(line);
  }
}

function plain(line: string): Token[] {
  return line ? [{ text: line, className: "" }] : [];
}

function walk(nodes: (ElementContent | Root["children"][number])[], classes: string[], out: Token[][]): void {
  for (const node of nodes) {
    if (node.type === "text") {
      const parts = node.value.split("\n");
      parts.forEach((part, index) => {
        if (index > 0) out.push([]);
        if (part) out[out.length - 1]?.push({ text: part, className: classes.join(" ") });
      });
    } else if (node.type === "element") {
      const element = node as Element;
      const own = element.properties?.className;
      const names = Array.isArray(own) ? own.map(String) : typeof own === "string" ? [own] : [];
      walk(element.children, [...classes, ...names], out);
    }
  }
}
