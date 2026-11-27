// OpenInClaude: the orchestrator's or a lead's Remote Control session (DESIGN.md 7.10). With a
// captured session URL it is a link that opens the conversation in claude.ai/code or the Claude
// app (a new tab, no opener, no referrer; 44 px on touch), with the session name in its title.
// When Remote Control was asked for but no link is known it is a muted line saying where to find
// the session by name, with the reason when it did not come up. Off, it renders nothing. Beside
// either sits a small "?" that opens RemoteHelpSheet: crowsnest or the Claude app?
import { ExternalLink, HelpCircle } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/cn";
import { remoteControlView, surfaceRows, type RemoteControlFields } from "../../lib/remote";
import { IconButton } from "../ui/Button";
import { Sheet } from "../ui/Sheet";

interface OpenInClaudeProps {
  status: RemoteControlFields | undefined;
  /** "button": a labelled link (cards, the Chat header); "icon": a 44 px icon link (Now's rows). */
  variant?: "button" | "icon";
  /** Leave the fallback line out (where there is no room for a sentence). */
  noFallback?: boolean;
  /** Only the fallback line: nothing when the link is known or Remote Control is off. */
  fallbackOnly?: boolean;
  className?: string;
}

export function OpenInClaude({ status, variant = "button", noFallback, fallbackOnly, className }: OpenInClaudeProps) {
  const view = remoteControlView(status);
  const [help, setHelp] = useState(false);
  if (view.kind === "off" || (view.kind === "fallback" && noFallback) || (view.kind === "link" && fallbackOnly)) return null;
  const helpButton = <IconButton icon={HelpCircle} label="Crowsnest or the Claude app?" onClick={() => setHelp(true)} data-testid="remote-help" />;
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-0.5", className)} data-testid="open-in-claude">
      {view.kind === "link" ? (
        <a
          href={view.url}
          target="_blank"
          rel="noopener noreferrer"
          title={`Open "${view.name}" in the Claude app`}
          aria-label={variant === "icon" ? `Open "${view.name}" in the Claude app` : undefined}
          data-testid="open-in-claude-link"
          className={cn(
            "inline-flex min-h-11 shrink-0 items-center justify-center gap-1.5 rounded border border-border-strong bg-surface text-xs font-medium text-heading hover:bg-hover md:min-h-7",
            variant === "icon" ? "min-w-11 md:min-w-7" : "px-2",
          )}
        >
          <ExternalLink className="h-4 w-4 shrink-0" aria-hidden />
          {variant === "button" && "Open in Claude"}
        </a>
      ) : (
        <span className="min-w-0 text-2xs leading-snug text-muted" data-testid="remote-control-fallback">
          Remote Control: find "{view.name}" in the Claude app.{view.reason ? ` ${view.reason}` : ""}
        </span>
      )}
      {variant === "button" && helpButton}
      <RemoteHelpSheet open={help} onClose={() => setHelp(false)} />
    </span>
  );
}

/** Crowsnest or the Claude app? The division of work, in short (R4 Fable 3.7). */
export function RemoteHelpSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Sheet open={open} onClose={onClose} title="Crowsnest or the Claude app?" size="md">
      <div className="flex flex-col gap-3 p-4 text-sm text-body" data-testid="remote-help-sheet">
        <p>
          Use <strong className="text-heading">crowsnest</strong> for the overview, decisions, approvals, many projects and landing. Use the{" "}
          <strong className="text-heading">Claude app</strong> (Remote Control) for a long conversation with one agent.
        </p>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-xs">
            <thead>
              <tr className="border-b border-border text-muted">
                <th className="py-1.5 pr-2 font-medium" scope="col"></th>
                <th className="py-1.5 pr-2 font-medium" scope="col">
                  Claude app
                </th>
                <th className="py-1.5 font-medium" scope="col">
                  Crowsnest
                </th>
              </tr>
            </thead>
            <tbody>
              {surfaceRows.map((row) => (
                <tr key={row.aspect} className="border-b border-border align-top">
                  <th className="py-1.5 pr-2 font-medium text-heading" scope="row">
                    {row.aspect}
                  </th>
                  <td className="py-1.5 pr-2">{row.claude}</td>
                  <td className="py-1.5">{row.crowsnest}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="text-xs text-muted">
          Work you give an agent in the app is recorded as a crowsnest job (it says the job id in its reply), so it shows on the board and is supervised either way.
        </p>
      </div>
    </Sheet>
  );
}
