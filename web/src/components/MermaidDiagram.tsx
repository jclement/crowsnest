// MermaidDiagram: renders one mermaid source to SVG. Loaded lazily by Markdown, and mermaid
// itself is a dynamic import, so the ~1 MB library is fetched only when a diagram appears.
// securityLevel "strict" makes mermaid sanitise labels (DOMPurify) and refuse click handlers
// and HTML labels; the resulting SVG is the one place dangerouslySetInnerHTML is allowed
// (DESIGN.md section 4.8). A diagram that fails to parse shows its source and the error.
import { useEffect, useId, useState } from "react";

type RenderState = { kind: "loading" } | { kind: "ready"; svg: string } | { kind: "error"; message: string };

function isDark(): boolean {
  return document.documentElement.classList.contains("dark");
}

export default function MermaidDiagram({ source }: { source: string }) {
  const id = "mermaid-" + useId().replace(/[^a-zA-Z0-9]/g, "");
  const [state, setState] = useState<RenderState>({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const mermaid = (await import("mermaid")).default;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme: isDark() ? "dark" : "neutral",
          fontFamily: "Inter Variable, ui-sans-serif, system-ui, sans-serif",
        });
        const { svg } = await mermaid.render(id, source);
        if (!cancelled) setState({ kind: "ready", svg });
      } catch (error) {
        if (!cancelled) setState({ kind: "error", message: error instanceof Error ? error.message : String(error) });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id, source]);

  if (state.kind === "ready") {
    return (
      <div
        className="mermaid-diagram overflow-x-auto rounded border border-border bg-surface p-3 [&_svg]:mx-auto [&_svg]:h-auto [&_svg]:max-w-full"
        role="img"
        aria-label="Diagram"
        dangerouslySetInnerHTML={{ __html: state.svg }}
      />
    );
  }
  return (
    <div className="rounded border border-border">
      {state.kind === "error" && (
        <p role="alert" className="border-b border-border bg-warning/10 px-3 py-1 text-xs text-warning">
          Diagram could not be drawn: {state.message}
        </p>
      )}
      <pre className="m-0 overflow-x-auto border-0 p-3 text-muted">{source}</pre>
    </div>
  );
}
