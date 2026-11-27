// A manual page (or one section of it) through the app's Markdown component, with what a manual
// needs on top: headings get GitHub's anchors (so /help/concepts/jobs#stuck and the same link on
// GitHub land on the same heading), links between pages stay in the app, "app:" links become
// "open the thing" buttons that go to the real view or sheet, and images point at their bundled
// files. The text is ours (docs/manual), but it still goes through the untrusted-markdown renderer.
import type { Element, ElementContent, Root } from "hast";
import { Link } from "@tanstack/react-router";
import { ExternalLink } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { defaultUrlTransform, type Components } from "react-markdown";
import { Markdown } from "../Markdown";
import { classifyLink, helpPath, slugger, type AppSheet, type AppTarget, type Manual } from "../../lib/manual";
import { usePaletteMaybe } from "../palette/palette-context";
import { cn } from "../../lib/cn";

function textOf(node: Element | ElementContent): string {
  if (node.type === "text") return node.value;
  if (node.type === "element") return node.children.map(textOf).join("");
  return "";
}

/** A rehype plugin: every heading gets its GitHub anchor as its id. */
function rehypeHeadingIds() {
  return (tree: Root) => {
    const slug = slugger();
    const walk = (node: Root | Element) => {
      for (const child of node.children) {
        if (child.type !== "element") continue;
        if (/^h[1-6]$/.test(child.tagName)) child.properties = { ...child.properties, id: slug(textOf(child)) };
        else walk(child);
      }
    };
    walk(tree);
  };
}

/** Let app: links through (they become buttons); everything else as the default check says. */
function urlTransform(url: string): string {
  return url.startsWith("app:") ? url : defaultUrlTransform(url);
}

interface ManualMarkdownProps {
  manual: Manual;
  /** The page the text comes from, so relative links resolve. */
  pageId: string;
  children: string;
  className?: string;
  /** Called after an in-app link or an "open" button is followed (a sheet closes itself). */
  onNavigate?: () => void;
}

export function ManualMarkdown({ manual, pageId, children, className, onNavigate }: ManualMarkdownProps) {
  const palette = usePaletteMaybe();
  const components = useMemo<Components>(
    () => ({
      a: ({ node: _node, href = "", children: label, ...props }) => {
        const link = classifyLink(pageId, href);
        switch (link.kind) {
          case "page":
            return (
              <Link to={helpPath(link.id, link.hash)} onClick={onNavigate} className={props.className}>
                {label}
              </Link>
            );
          case "anchor":
            return (
              <a href={`#${link.hash}`} className={props.className}>
                {label}
              </a>
            );
          case "app":
            return (
              <OpenButton target={link.target} onNavigate={onNavigate} openSheet={palette?.openSheet}>
                {label}
              </OpenButton>
            );
          case "external":
            return (
              <a href={link.href} target="_blank" rel="noopener noreferrer">
                {label}
                <ExternalLink className="ml-0.5 inline h-3 w-3 align-baseline" aria-hidden />
              </a>
            );
          default:
            // A link to a repository file (README, DESIGN): plain text in the app, a link on GitHub.
            return <span className="font-medium text-heading">{label}</span>;
        }
      },
      img: ({ node: _node, src = "", alt = "" }) => {
        const name = typeof src === "string" ? src.replace(/^(\.\.\/)+/, "").replace(/^\.\//, "") : "";
        const url = manual.images.get(name);
        if (!url) return <span className="text-muted">[{alt}]</span>;
        return <img src={url} alt={alt} loading="lazy" className="my-2 max-h-[70vh] max-w-full rounded border border-border" />;
      },
    }),
    [manual, pageId, onNavigate, palette],
  );
  return (
    <Markdown className={cn("manual", className)} components={components} extraRehypePlugins={[rehypeHeadingIds]} urlTransform={urlTransform}>
      {children}
    </Markdown>
  );
}

function OpenButton({ target, children, onNavigate, openSheet }: { target: AppTarget; children: ReactNode; onNavigate?: () => void; openSheet?: (sheet: AppSheet) => void }) {
  const className =
    "mx-0.5 inline-flex min-h-7 items-center gap-1 rounded border border-accent/40 bg-accent/10 px-2 py-0.5 align-middle text-xs font-medium text-accent no-underline hover:bg-accent/20";
  if (target.kind === "sheet") {
    return (
      <button
        type="button"
        className={className}
        data-open-target={`sheet/${target.sheet}`}
        onClick={() => {
          onNavigate?.();
          openSheet?.(target.sheet);
        }}
      >
        {children}
      </button>
    );
  }
  return (
    <Link to={target.to} search={target.search as never} hash={target.hash} onClick={onNavigate} className={className} data-open-target={target.to + (target.hash ? `#${target.hash}` : "")}>
      {children}
    </Link>
  );
}
