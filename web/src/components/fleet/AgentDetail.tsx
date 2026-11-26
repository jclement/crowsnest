// AgentDetail: everything about one agent. On desktop it is the right-hand panel of the Fleet
// view; on mobile it fills a Sheet (FleetView decides). From the top: who and where, the dialog
// card when the agent is asking something, the Conversation/Terminal toggle (slots that later
// milestones fill), and the screen preview.
import { MessagesSquare, SquareTerminal, X } from "lucide-react";
import { useState } from "react";
import { cleanTitle } from "../../lib/fleet";
import { useAgentScreen } from "../../lib/queries";
import type { Agent, Tab, Workspace } from "../../lib/types";
import { AgentConversation } from "../agent/AgentConversation";
import { AgentTerminal } from "../agent/AgentTerminal";
import { IconButton } from "../ui/Button";
import { Chip, StatusChip, StatusDot } from "../ui/Chip";
import { TabPanel, Tabs } from "../ui/Tabs";
import { DialogCard } from "./DialogCard";
import { ScreenPreview } from "./ScreenPreview";

type DetailView = "conversation" | "terminal";

interface AgentDetailProps {
  agent: Agent;
  workspace: Workspace;
  tab: Tab;
  /** Shown as a close button in the panel header; the mobile sheet has its own. */
  onClose?: () => void;
  /** Hide the name row (the mobile sheet shows it in its own header). */
  hideHeading?: boolean;
  /** Which tab to open on (the URL's &view=); by default Conversation when there is a transcript. */
  initialView?: DetailView;
}

export function AgentDetail({ agent, workspace, tab, onClose, hideHeading, initialView }: AgentDetailProps) {
  const target = agent.pane_id;
  const screen = useAgentScreen(target);
  const [view, setView] = useState<DetailView>(initialView ?? (agent.has_transcript ? "conversation" : "terminal"));
  const showDialog = !!screen.data && (screen.data.dialog.present || agent.needs_you);
  const tabsId = `agent-${target.replace(/[^a-zA-Z0-9]/g, "-")}`;

  return (
    <article aria-label={`Agent ${agent.name}`} className="flex flex-col gap-3 p-3 md:p-4">
      <header className="flex flex-col gap-1.5">
        {!hideHeading && (
          <div className="flex items-center gap-2">
            <StatusDot status={agent.status} />
            <h2 className="min-w-0 flex-1 truncate text-base font-semibold text-heading">{agent.name}</h2>
            {onClose && <IconButton icon={X} label="Close agent" onClick={onClose} />}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-1.5">
          <StatusChip status={agent.status} />
          <Chip mono>{agent.kind}</Chip>
          <Chip mono title="Pane id">
            {agent.pane_id}
          </Chip>
          <span className="truncate text-2xs text-muted">
            {workspace.label} › {tab.label}
          </span>
        </div>
        {agent.title && (
          <p className="text-xs text-body" title="Terminal title">
            {cleanTitle(agent.title)}
          </p>
        )}
        <p className="break-all font-mono text-2xs text-muted" title="Working directory">
          {agent.cwd}
        </p>
      </header>

      {showDialog && screen.data && (
        <DialogCard target={target} agentName={agent.name} screen={screen.data} onOpenTerminal={() => setView("terminal")} />
      )}

      <div className="flex flex-col gap-2">
        <Tabs
          id={tabsId}
          label="Agent view"
          value={view}
          onChange={setView}
          className="w-full md:w-auto md:self-start"
          items={[
            { value: "conversation", label: "Conversation", icon: MessagesSquare },
            { value: "terminal", label: "Terminal", icon: SquareTerminal },
          ]}
        />
        <TabPanel id={tabsId} value={view} className="rounded-md border border-border">
          {view === "conversation" ? <AgentConversation agent={agent} /> : <AgentTerminal agent={agent} />}
        </TabPanel>
      </div>

      {!showDialog && <ScreenPreview query={screen} />}
    </article>
  );
}
