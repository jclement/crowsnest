// PaneDetail: a plain shell pane (no agent) selected in the Fleet view: where it is, and a live
// terminal on it through the pane-control WebSocket. The agent equivalent is AgentDetail.
import { SquareTerminal, X } from "lucide-react";
import { terminalPaths } from "../../lib/api";
import type { Pane, Tab, Workspace } from "../../lib/types";
import { TerminalPanel } from "../terminal/TerminalPanel";
import { IconButton } from "../ui/Button";
import { Chip } from "../ui/Chip";

interface PaneDetailProps {
  pane: Pane;
  workspace: Workspace;
  tab: Tab;
  onClose?: () => void;
  hideHeading?: boolean;
}

export function PaneDetail({ pane, workspace, tab, onClose, hideHeading }: PaneDetailProps) {
  return (
    <article aria-label={`Shell ${pane.id}`} className="flex flex-col gap-3 p-3 md:p-4">
      <header className="flex flex-col gap-1.5">
        {!hideHeading && (
          <div className="flex items-center gap-2">
            <SquareTerminal className="h-4 w-4 text-muted" aria-hidden />
            <h2 className="min-w-0 flex-1 truncate text-base font-semibold text-heading">Shell</h2>
            {onClose && <IconButton icon={X} label="Close pane" onClick={onClose} />}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-1.5">
          <Chip mono title="Pane id">
            {pane.id}
          </Chip>
          <span className="truncate text-2xs text-muted">
            {workspace.label} › {tab.label}
          </span>
        </div>
        <p className="break-all font-mono text-2xs text-muted" title="Working directory">
          {pane.cwd}
        </p>
      </header>
      <TerminalPanel
        path={terminalPaths.pane(pane.id)}
        label={`Terminal for pane ${pane.id}`}
        fill="fixed"
        className="overflow-hidden rounded-md border border-border"
      />
    </article>
  );
}
