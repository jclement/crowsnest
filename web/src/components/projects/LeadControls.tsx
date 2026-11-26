// LeadControls: a project's lead, as a card on the project page (DESIGN.md 21.2): its state, model
// and Remote Control note, and what can be done with it: Start or Stop, Restart and New
// conversation (both confirmed, as the orchestrator's are), Poke now, Chat with it (Chat's target
// switcher, /chat?target=lead-<slug>) and its terminal (Fleet, ?agent=<pane>). An archived
// project's lead cannot start.
import { useNavigate } from "@tanstack/react-router";
import { BellRing, MessageSquare, MessageSquarePlus, Play, RotateCw, Square, SquareTerminal } from "lucide-react";
import { useState } from "react";
import { pokeToast, type PokeResult } from "../../lib/journal";
import { leadAgentName, leadChip, leadConfirmations, leadRunning, type LeadAction, type ProjectView } from "../../lib/projects";
import { useLeadAction } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

type Confirmable = "restart" | "new" | "stop";

const doneWords: Record<Exclude<LeadAction, "poke">, string> = {
  start: "Lead started",
  stop: "Lead stopped",
  restart: "Lead restarted",
  new: "New conversation started",
};

export function LeadControls({ project }: { project: ProjectView }) {
  const lead = project.lead;
  const running = leadRunning(lead);
  const chip = leadChip(lead);
  const action = useLeadAction(project.slug);
  const toast = useToast();
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState<Confirmable | null>(null);
  const words = leadConfirmations(project.name);

  const run = (which: LeadAction) =>
    action.mutate(which, {
      onSuccess: (result) => {
        setConfirming(null);
        toast.show(which === "poke" ? pokeToast(result as PokeResult) : { tone: "success", title: doneWords[which] });
      },
      onError: (error) => toast.show({ tone: "danger", title: "The lead did not change", description: error.message }),
    });

  return (
    <Card
      title={
        <>
          Lead
          <Chip tone={chip.tone}>
            <span data-testid="lead-state">{chip.label}</span>
          </Chip>
        </>
      }
      actions={<span className="font-mono text-2xs text-muted">{lead.name || leadAgentName(project.slug)}</span>}
    >
      <div className="flex flex-col gap-2 text-xs">
        <p className="text-body">
          The lead is a standing Claude for this project: it holds jobs given "to the lead", plans, and hands work to job agents.
          {lead.model && (
            <>
              {" "}
              Model <span className="font-mono text-heading">{lead.model}</span>.
            </>
          )}
        </p>
        {lead.detail && <p className="text-warning">{lead.detail}</p>}
        {lead.remote_control_note && (
          <p className="text-muted" data-testid="remote-control-note">
            Remote Control{lead.remote_control ? ` (${lead.remote_control})` : ""}: {lead.remote_control_note}
          </p>
        )}
        <div className="flex flex-wrap gap-1.5">
          {running ? (
            <Button compact icon={Square} onClick={() => setConfirming("stop")} disabled={action.isPending}>
              Stop lead
            </Button>
          ) : (
            <Button compact variant="primary" icon={Play} onClick={() => run("start")} disabled={action.isPending || project.archived}>
              {action.isPending && action.variables === "start" ? "Starting…" : "Start lead"}
            </Button>
          )}
          <Button compact icon={MessageSquare} onClick={() => void navigate({ to: "/chat", search: { target: leadAgentName(project.slug) } })} disabled={project.archived}>
            Chat with lead
          </Button>
          {running && lead.pane_id && (
            <Button compact variant="ghost" icon={SquareTerminal} onClick={() => void navigate({ to: "/fleet", search: { agent: lead.pane_id, view: "terminal" } })}>
              Terminal
            </Button>
          )}
          {running && (
            <>
              <Button compact variant="ghost" icon={BellRing} onClick={() => run("poke")} disabled={action.isPending}>
                Poke now
              </Button>
              <Button compact variant="ghost" icon={RotateCw} onClick={() => setConfirming("restart")} disabled={action.isPending}>
                Restart
              </Button>
              <Button compact variant="ghost" icon={MessageSquarePlus} onClick={() => setConfirming("new")} disabled={action.isPending}>
                New conversation
              </Button>
            </>
          )}
        </div>
        {project.archived && <p className="text-muted">Archived projects take no work; unarchive it to start its lead.</p>}
      </div>
      <Sheet open={confirming !== null} onClose={() => setConfirming(null)} title={confirming ? words[confirming].title : ""} size="md">
        {confirming && (
          <div className="flex flex-col gap-4 p-4">
            <p className="text-sm text-body">{words[confirming].body}</p>
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setConfirming(null)}>
                Cancel
              </Button>
              <Button variant="danger" onClick={() => run(confirming)} disabled={action.isPending}>
                {action.isPending ? "Working…" : words[confirming].button}
              </Button>
            </div>
          </div>
        )}
      </Sheet>
    </Card>
  );
}
