// The Agents view (DESIGN.md section 12), under More: herdr's model in one place, for recovery and
// debugging. R8 merged the Fleet view (the tree of workspaces, tabs and agents) and the Terminal
// view (the whole herdr session's TUI) into it; /fleet and /terminal redirect here.
//
// The tree at left; at right the selected agent (or a plain shell pane), else the session
// terminal. On a phone the tree fills the page, an agent opens in a full-screen sheet, and the
// "Session terminal" row opens the TUI in place of the tree. The selection lives in the URL
// (?agent=<pane id>, ?session=true) so a link or a reload lands on the same thing.
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowLeft, RefreshCw, Ship, SquareTerminal } from "lucide-react";
import { TerminalPanel } from "../components/terminal/TerminalPanel";
import { terminalPaths } from "../lib/api";
import { cn } from "../lib/cn";
import { AgentDetail } from "../components/fleet/AgentDetail";
import { FleetTree } from "../components/fleet/FleetTree";
import { PaneDetail } from "../components/fleet/PaneDetail";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { StatusDot } from "../components/ui/Chip";
import { EmptyState } from "../components/ui/EmptyState";
import { Sheet } from "../components/ui/Sheet";
import { SkeletonRows } from "../components/ui/Skeleton";
import { findAgent, findPane } from "../lib/fleet";
import { DESKTOP_QUERY, useMediaQuery } from "../lib/media";
import { useFleet } from "../lib/queries";

/** The whole herdr session (the TUI in a PTY) in a terminal that fills down to the visible bottom. */
function SessionTerminal() {
  return <TerminalPanel path={terminalPaths.tui} label="herdr session terminal" fill="viewport" />;
}

export function AgentsView() {
  const fleet = useFleet();
  const { agent: selected, view, session } = useSearch({ strict: false }) as { agent?: string; view?: "conversation" | "terminal"; session?: true };
  const navigate = useNavigate();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const select = (paneId: string | undefined) => void navigate({ to: "/agents", search: { agent: paneId } });
  const openSession = () => void navigate({ to: "/agents", search: { session: true } });

  // A phone shows the session terminal in place of the tree, with a way back.
  if (!desktop && session) {
    return (
      <div className="flex flex-col">
        <div className="flex items-center gap-1 border-b border-border px-1">
          <Button variant="ghost" icon={ArrowLeft} onClick={() => select(undefined)}>
            Agents
          </Button>
          <span className="text-sm font-medium text-heading">Session terminal</span>
        </div>
        <SessionTerminal />
      </div>
    );
  }

  if (fleet.isPending) return <SkeletonRows rows={8} label="Loading the fleet" />;
  if (fleet.isError) {
    return (
      <Banner
        tone="danger"
        role="alert"
        action={
          <Button compact icon={RefreshCw} onClick={() => void fleet.refetch()}>
            Retry
          </Button>
        }
      >
        Could not load the fleet: {fleet.error.message}
      </Banner>
    );
  }
  if (fleet.data.workspaces.length === 0) {
    // No agents yet, but the session's terminal is still there (a first login is done in it).
    return (
      <div className="flex flex-col">
        <EmptyState icon={Ship} title="No workspaces yet">
          The herdr session <span className="font-mono">{fleet.data.session}</span> has no workspaces. Agents you or the
          orchestrator start will appear here.
        </EmptyState>
        {desktop ? (
          <SessionTerminal />
        ) : (
          <Button variant="secondary" icon={SquareTerminal} onClick={openSession} className="mx-3">
            Session terminal
          </Button>
        )}
      </div>
    );
  }

  const found = selected ? findAgent(fleet.data, selected) : null;
  // A plain shell pane (no agent) opens a terminal on it instead of the agent panel.
  const shell = selected && !found ? findPane(fleet.data, selected) : null;
  const close = desktop ? () => select(undefined) : undefined;
  const sessionRow = (
    <button
      type="button"
      onClick={openSession}
      aria-current={desktop && !found && !shell ? "true" : undefined}
      className={cn(
        "flex min-h-11 w-full items-center gap-2 border-b border-border px-3 text-left text-sm text-heading hover:bg-hover md:min-h-9",
        desktop && !found && !shell && "bg-selected font-medium",
      )}
    >
      <SquareTerminal className="h-4 w-4 text-muted" aria-hidden />
      Session terminal
      <span className="ml-auto text-2xs text-muted">the herdr TUI</span>
    </button>
  );
  const detail = found ? (
    <AgentDetail
      key={`${found.agent.pane_id}:${view ?? ""}`}
      agent={found.agent}
      initialView={view}
      workspace={found.workspace}
      tab={found.tab}
      onClose={close}
      hideHeading={!desktop}
    />
  ) : shell ? (
    <PaneDetail key={shell.pane.id} pane={shell.pane} workspace={shell.workspace} tab={shell.tab} onClose={close} hideHeading={!desktop} />
  ) : null;

  return (
    <div className="md:flex md:h-full md:min-h-0">
      <div className="md:w-[22rem] md:shrink-0 md:overflow-y-auto md:border-r md:border-border lg:w-[26rem]">
        {sessionRow}
        <FleetTree fleet={fleet.data} selected={found?.agent.pane_id ?? shell?.pane.id} onSelect={select} />
      </div>
      {desktop ? (
        <div className="min-w-0 flex-1 overflow-y-auto bg-surface-raised/40">{detail ?? <SessionTerminal />}</div>
      ) : (
        <Sheet
          open={!!found || !!shell}
          onClose={() => select(undefined)}
          title={
            found ? (
              <>
                <StatusDot status={found.agent.status} />
                <span className="truncate">{found.agent.name}</span>
              </>
            ) : (
              shell && <span className="truncate font-mono">shell {shell.pane.id}</span>
            )
          }
        >
          {detail}
        </Sheet>
      )}
    </div>
  );
}
