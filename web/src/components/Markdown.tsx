// Markdown: renders untrusted markdown (transcripts, question bodies, job results) safely, per
// DESIGN.md section 4.8. react-markdown builds React elements, never HTML strings, and raw HTML
// in the source is dropped (skipHtml), so a <script> or an onerror attribute is inert text at
// worst. Its default urlTransform strips javascript: and other unsafe link targets. Links open in
// a new tab with rel="noopener noreferrer". Fenced ```mermaid blocks render as diagrams through
// MermaidDiagram, which loads mermaid only when one appears.
import type { Element, ElementContent } from "hast";
import { lazy, memo, Suspense, type ComponentProps } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import remarkGfm from "remark-gfm";
import { cn } from "../lib/cn";

const MermaidDiagram = lazy(() => import("./MermaidDiagram"));

/** The plain text inside a hast node, for handing a code block's source to mermaid. */
function textOf(node: Element | ElementContent | undefined): string {
  if (!node) return "";
  if (node.type === "text") return node.value;
  if (node.type === "element") return node.children.map(textOf).join("");
  return "";
}

const components: Components = {
  a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer" />,
  pre: ({ node, children, ...props }) => {
    const code = node?.children[0];
    const isMermaid =
      code?.type === "element" &&
      code.tagName === "code" &&
      Array.isArray(code.properties.className) &&
      code.properties.className.includes("language-mermaid");
    if (isMermaid) {
      const source = textOf(code);
      return (
        <Suspense fallback={<pre className="text-muted">{source}</pre>}>
          <MermaidDiagram source={source} />
        </Suspense>
      );
    }
    return <pre {...props}>{children}</pre>;
  },
};

// mermaid fences are left alone by the highlighter (it has no grammar for them) so their source
// reaches MermaidDiagram as plain text.
const rehypePlugins: ComponentProps<typeof ReactMarkdown>["rehypePlugins"] = [
  [rehypeHighlight, { detect: false, plainText: ["mermaid", "text", "txt"] }],
];
const remarkPlugins = [remarkGfm];

interface MarkdownProps {
  children: string;
  className?: string;
}

export const Markdown = memo(function Markdown({ children, className }: MarkdownProps) {
  return (
    <div className={cn("markdown", className)}>
      <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} components={components} skipHtml>
        {children}
      </ReactMarkdown>
    </div>
  );
});
