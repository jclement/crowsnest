// The Chat view (DESIGN.md section 12): a standing agent's conversation, a sticky composer, its
// status chip (driven by `orchestrator` and `lead` events), and a menu with thinking, Restart and
// New conversation. When the agent needs the terminal (a start-up prompt) or is not running, a
// banner says why and links to its pane in Fleet. With voice on, the composer has the microphone
// (components/voice/VoiceComposer.tsx, prompting whoever Chat is talking to) and each reply's SAY
// caption a play button; the header's speak toggle is the master mute and each agent has its own
// "Speak" switch here.
//
// Chat talks to the orchestrator or to any project's lead (DESIGN.md 21.2): a switcher in the
// header, the choice in the URL (/chat?target=lead-<slug>). A lead's conversation works exactly
// as the orchestrator's does (it is a herdr agent addressed by its pane id); its menu acts on the
// lead's routes. With --no-orchestrator (status "disabled") Chat defaults to the first running
// lead. A lead that is off offers Start lead instead of a conversation.
import { useNavigate, useSearch } from "@tanstack/react-router";
import { MessageSquare, Play, SquareTerminal } from "lucide-react";
import { useState, type ReactNode } from "react";
import { ChatMenu } from "../components/chat/ChatMenu";
import { ChatStatusChip } from "../components/chat/ChatStatusChip";
import { ConversationView } from "../components/chat/ConversationView";
import { AgentSpeakToggle } from "../components/voice/SpeakMode";
import { VoiceComposer } from "../components/voice/VoiceComposer";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { EmptyState } from "../components/ui/EmptyState";
import { SkeletonRows } from "../components/ui/Skeleton";
import { useToast } from "../components/ui/toast-context";
import { chatStateOf, type OrchestratorStatus } from "../lib/conversation";
import { useOrchestrator } from "../lib/conversation-queries";
import { CHAT_ORCHESTRATOR as ORCHESTRATOR, defaultChatTarget as defaultTarget, leadAgentName, leadRunning, slugOfLead, type ProjectView } from "../lib/projects";
import { useLeadAction, useMe, useProjects } from "../lib/queries";

export function ChatView() {
  const status = useOrchestrator();
  const projects = useProjects();
  const { target: wanted } = useSearch({ strict: false }) as { target?: string };
  const navigate = useNavigate();
  const [showThinking, setShowThinking] = useState(false);
  const voice = useMe().data?.features.voice ?? false;

  if (status.isPending || (status.data?.state === "disabled" && projects.isPending)) return <SkeletonRows rows={6} label="Loading the orchestrator" />;
  const live = (projects.data?.projects ?? []).filter((project) => !project.archived);
  const target = wanted ?? defaultTarget(status.data, live);
  const slug = slugOfLead(target);
  const choose = (next: string) => void navigate({ to: "/chat", search: { target: next === ORCHESTRATOR ? undefined : next } });
  const switcher = <TargetSwitcher value={target} orchestrator={status.data} projects={live} onChange={choose} />;

  if (slug) {
    const project = live.find((entry) => entry.slug === slug);
    return (
      <LeadChat slug={slug} project={project} pending={projects.isPending} switcher={switcher} voice={voice} showThinking={showThinking} onToggleThinking={() => setShowThinking((value) => !value)} />
    );
  }
  if (status.isError) {
    return (
      <EmptyState icon={MessageSquare} title="No orchestrator">
        {status.error.message}
      </EmptyState>
    );
  }
  const orchestrator = status.data;
  const state = chatStateOf(orchestrator.state);
  const paneId = orchestrator.pane_id;
  const openTerminal = paneId ? () => void navigate({ to: "/fleet", search: { agent: paneId } }) : undefined;

  return (
    <section aria-label="Chat with the orchestrator" className="flex min-w-0 flex-col md:h-full md:min-h-0">
      <ChatHeader
        switcher={switcher}
        state={<ChatStatusChip state={state} />}
        sessionId={orchestrator.session_id}
        speak={voice && orchestrator.state !== "disabled" ? <AgentSpeakToggle agent={ORCHESTRATOR} name="the orchestrator" /> : null}
        menu={
          <ChatMenu model={orchestrator.model} showThinking={showThinking} onToggleThinking={() => setShowThinking((value) => !value)} canRelaunch={orchestrator.state !== "disabled"} />
        }
      />
      {orchestrator.detail && (state === "needs_terminal" || state === "failed") && <TerminalBanner state={state} detail={orchestrator.detail} openTerminal={openTerminal} />}
      {orchestrator.state === "disabled" ? (
        <EmptyState icon={MessageSquare} title="The orchestrator is off">
          This server was started with --no-orchestrator. Project leads can still be chatted with (pick one above), and agents in the fleet messaged from Fleet.
        </EmptyState>
      ) : paneId ? (
        <ConversationView
          target={ORCHESTRATOR}
          agentName="the orchestrator"
          state={state}
          variant="page"
          showThinking={showThinking}
          onOpenTerminal={openTerminal}
          paneFallback={false}
          renderComposer={voice ? (props) => <VoiceComposer {...props} /> : undefined}
          playTarget={voice ? ORCHESTRATOR : undefined}
          speaker={ORCHESTRATOR}
        />
      ) : (
        <EmptyState icon={MessageSquare} title="Starting the orchestrator">
          {orchestrator.detail ?? "Crowsnest is starting Claude in your herdr session."}
        </EmptyState>
      )}
    </section>
  );
}

interface LeadChatProps {
  slug: string;
  project?: ProjectView;
  pending: boolean;
  switcher: ReactNode;
  voice: boolean;
  showThinking: boolean;
  onToggleThinking: () => void;
}

/** A project lead's conversation, or Start lead while it is off. */
function LeadChat({ slug, project, pending, switcher, voice, showThinking, onToggleThinking }: LeadChatProps) {
  const navigate = useNavigate();
  const start = useLeadAction(slug);
  const toast = useToast();
  if (!project) {
    if (pending) return <SkeletonRows rows={6} label="Loading the lead" />;
    return (
      <section aria-label="Chat" className="flex min-w-0 flex-col">
        <ChatHeader switcher={switcher} />
        <EmptyState icon={MessageSquare} title="No such project">
          There is no project {slug} (or it is archived). Pick another above.
        </EmptyState>
      </section>
    );
  }
  const lead = project.lead;
  const running = leadRunning(lead);
  const state = running ? chatStateOf(lead.state) : "off";
  const paneId = running ? lead.pane_id : "";
  const openTerminal = paneId ? () => void navigate({ to: "/fleet", search: { agent: paneId } }) : undefined;
  const agent = leadAgentName(project.slug);
  const name = `${project.name}'s lead`;
  const startLead = () =>
    start.mutate("start", {
      onSuccess: () => toast.show({ tone: "success", title: `${project.name}: lead started` }),
      onError: (error) => toast.show({ tone: "danger", title: "The lead did not start", description: error.message }),
    });
  return (
    <section aria-label={`Chat with ${name}`} className="flex min-w-0 flex-col md:h-full md:min-h-0">
      <ChatHeader
        switcher={switcher}
        state={<ChatStatusChip state={state} hint={running ? undefined : "The lead is not running; start it to talk to it"} />}
        sessionId={running ? lead.session_id : ""}
        speak={voice ? <AgentSpeakToggle agent={agent} name={name} /> : null}
        menu={<ChatMenu model={lead.model} showThinking={showThinking} onToggleThinking={onToggleThinking} canRelaunch={running} lead={project} />}
      />
      {lead.detail && (state === "needs_terminal" || state === "failed") && <TerminalBanner state={state} detail={lead.detail} openTerminal={openTerminal} />}
      {!running ? (
        <EmptyState
          icon={MessageSquare}
          title={`${name} is off`}
          action={
            <Button variant="primary" icon={Play} onClick={startLead} disabled={start.isPending}>
              {start.isPending ? "Starting…" : "Start lead"}
            </Button>
          }
        >
          Start it to talk to it here. Its conversation is kept between starts.
        </EmptyState>
      ) : paneId ? (
        <ConversationView
          key={paneId}
          target={paneId}
          agentName={name}
          state={state}
          variant="page"
          showThinking={showThinking}
          onOpenTerminal={openTerminal}
          paneFallback={false}
          renderComposer={voice ? (props) => <VoiceComposer {...props} /> : undefined}
          playTarget={voice ? paneId : undefined}
          speaker={agent}
        />
      ) : (
        <EmptyState icon={MessageSquare} title={`Starting ${name}`}>
          {lead.detail ?? "Crowsnest is starting Claude for the project."}
        </EmptyState>
      )}
    </section>
  );
}

function ChatHeader({ switcher, state, sessionId, speak, menu }: { switcher: ReactNode; state?: ReactNode; sessionId?: string; speak?: ReactNode; menu?: ReactNode }) {
  return (
    <header className="sticky top-0 z-10 flex min-h-11 shrink-0 flex-wrap items-center gap-x-2 border-b border-border bg-surface-raised px-3 md:static">
      {switcher}
      {state}
      {sessionId && (
        <span className="hidden min-w-0 truncate font-mono text-2xs text-muted sm:inline" title="Claude session id">
          {sessionId.slice(0, 8)}
        </span>
      )}
      <div className="ml-auto flex items-center gap-1.5">
        {speak}
        {menu}
      </div>
    </header>
  );
}

function TerminalBanner({ state, detail, openTerminal }: { state: string; detail: string; openTerminal?: () => void }) {
  return (
    <Banner
      tone={state === "failed" ? "danger" : "warning"}
      role="alert"
      action={
        openTerminal && (
          <Button compact icon={SquareTerminal} onClick={openTerminal}>
            Open terminal
          </Button>
        )
      }
    >
      {detail}
    </Banner>
  );
}

/** Who Chat talks to: the orchestrator (unless it is off) or a project's lead. */
function TargetSwitcher({ value, orchestrator, projects, onChange }: { value: string; orchestrator?: OrchestratorStatus; projects: ProjectView[]; onChange: (target: string) => void }) {
  const showOrchestrator = !!orchestrator && orchestrator.state !== "disabled";
  if (projects.length === 0) return <h1 className="text-sm font-semibold text-heading">{showOrchestrator || value === ORCHESTRATOR ? "Orchestrator" : value}</h1>;
  return (
    <label className="flex min-w-0 items-center">
      <span className="sr-only">Chat with</span>
      <select
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-label="Chat with"
        data-testid="chat-target"
        className="h-11 min-w-0 max-w-[14rem] truncate rounded border border-transparent bg-transparent pr-1 text-sm font-semibold text-heading hover:border-border-strong focus-visible:outline-2 focus-visible:outline-accent md:h-8"
      >
        {showOrchestrator && <option value={ORCHESTRATOR}>Orchestrator</option>}
        {projects.map((project) => (
          <option key={project.slug} value={leadAgentName(project.slug)}>
            {project.name} lead{leadRunning(project.lead) ? "" : " (off)"}
          </option>
        ))}
      </select>
    </label>
  );
}
