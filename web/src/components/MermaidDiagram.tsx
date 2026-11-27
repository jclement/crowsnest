// MermaidDiagram: renders one mermaid source to SVG. Loaded lazily by Markdown, and mermaid
// itself is a dynamic import, so the ~1 MB library is fetched only when a diagram appears.
// securityLevel "strict" makes mermaid sanitise labels (DOMPurify) and refuse click handlers
// and HTML labels; the resulting SVG is the one place dangerouslySetInnerHTML is allowed
// (DESIGN.md section 4.8). A diagram that fails to parse shows its source and the error.
//
// Mermaid's "base" theme is driven by themeVariables built from the applied theme's tokens, so a
// diagram is drawn in the theme's own surfaces, lines and accent, and redrawn when the theme or
// the mode changes.
import { useEffect, useId, useState } from "react";

type RenderState = { kind: "loading" } | { kind: "ready"; svg: string } | { kind: "error"; message: string };

/** Mermaid's theme variables from the theme now applied (src/themes/). */
function themeVariables(): Record<string, string | boolean> {
  const style = getComputedStyle(document.documentElement);
  const token = (name: string) => style.getPropertyValue(`--color-${name}`).trim();
  return {
    darkMode: document.documentElement.classList.contains("dark"),
    background: token("surface"),
    primaryColor: token("surface-raised"),
    primaryTextColor: token("heading"),
    primaryBorderColor: token("border-strong"),
    secondaryColor: token("selected"),
    secondaryTextColor: token("heading"),
    secondaryBorderColor: token("accent"),
    tertiaryColor: token("surface-sunken"),
    tertiaryTextColor: token("body"),
    tertiaryBorderColor: token("border"),
    lineColor: token("muted"),
    textColor: token("body"),
    mainBkg: token("surface-raised"),
    nodeBorder: token("border-strong"),
    clusterBkg: token("surface-sunken"),
    clusterBorder: token("border"),
    edgeLabelBackground: token("surface"),
    noteBkgColor: token("selected"),
    noteTextColor: token("heading"),
    noteBorderColor: token("accent"),
    actorBkg: token("surface-raised"),
    actorBorder: token("border-strong"),
    actorTextColor: token("heading"),
    signalColor: token("body"),
    signalTextColor: token("body"),
    labelBoxBkgColor: token("surface-raised"),
    labelTextColor: token("heading"),
    errorBkgColor: token("danger"),
    errorTextColor: token("accent-fg"),
  };
}

/** A key that changes whenever the theme or the mode does, so the diagram is drawn again. */
function useThemeKey(): string {
  const read = () => `${document.documentElement.dataset.theme ?? ""}/${document.documentElement.className}`;
  const [key, setKey] = useState(read);
  useEffect(() => {
    const observer = new MutationObserver(() => setKey(read()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class", "data-theme"] });
    return () => observer.disconnect();
  }, []);
  return key;
}

export default function MermaidDiagram({ source }: { source: string }) {
  const id = "mermaid-" + useId().replace(/[^a-zA-Z0-9]/g, "");
  const [state, setState] = useState<RenderState>({ kind: "loading" });
  const themeKey = useThemeKey();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const mermaid = (await import("mermaid")).default;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme: "base",
          themeVariables: themeVariables(),
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
  }, [id, source, themeKey]);

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
