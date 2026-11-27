// ApprovalCard: one tool call waiting for the human (DESIGN.md 8.7), rendered by tool: a Bash
// command in a code block with its directory, an Edit/Write/MultiEdit as a diff, a WebFetch as its
// URL, an MCP call as server, tool and arguments, anything else as indented JSON; then a
// plain-language risk line, an optional note (returned to the agent with a deny) and the buttons:
// Allow once, Deny, Open terminal.
//
// A dangerous call has no one-tap allow: Allow opens a second confirmation that names what is
// dangerous, and only "Yes, allow" sends it (the server refuses an unconfirmed one too). There is
// no "always allow" here: widening an agent's permanent permissions is a terminal action.
//
// Keyboard: y allows and n denies, but only while the card itself has focus (tab to it, or the
// tray's shortcut). A key pressed anywhere else, or inside the note, never answers anything; y
// on a dangerous card opens the confirmation, it does not allow.
import { AlertTriangle, Check, FileDiff, Globe, Plug, ShieldAlert, SquareTerminal, Terminal, Wrench, X } from "lucide-react";
import { forwardRef, useId, useState, type KeyboardEvent, type ReactNode } from "react";
import { ApiError } from "../../lib/api";
import {
  bashCommand,
  editFiles,
  mcpParts,
  outcome,
  prettyInput,
  requester,
  riskLabels,
  riskLine,
  toolKind,
  type Approval,
  type Risk,
} from "../../lib/approvals";
import { cn } from "../../lib/cn";
import { relativeTime } from "../../lib/format";
import { useDecideApproval } from "../../lib/queries";
import { DiffView } from "../inbox/DiffView";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Chip, type ChipTone } from "../ui/Chip";
import { useToast } from "../ui/toast-context";

interface ApprovalCardProps {
  approval: Approval;
  onOpenTerminal?: () => void;
  /** Compact: the tray's form (a shorter code block, the diff unified, no footnote). */
  compact?: boolean;
  /** Called once a decision is accepted (the tray closes the item). */
  onDecided?: (approval: Approval) => void;
  className?: string;
}

const riskTone: Record<Risk, ChipTone> = { low: "success", elevated: "warning", dangerous: "danger" };

export const ApprovalCard = forwardRef<HTMLElement, ApprovalCardProps>(function ApprovalCard(
  { approval, onOpenTerminal, compact, onDecided, className },
  ref,
) {
  const decide = useDecideApproval(approval.id);
  const toast = useToast();
  const noteId = useId();
  const titleId = useId();
  const [note, setNote] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pending = approval.status === "pending";
  const dangerous = approval.risk === "dangerous";

  const send = (allow: boolean, confirm = false) => {
    setError(null);
    decide.mutateAsync({ decision: allow ? "allow" : "deny", note: note.trim() || undefined, confirm: confirm || undefined }).then(
      (decided) => {
        toast.show({ tone: allow ? "success" : "info", title: allow ? "Allowed" : "Denied", description: decided.summary });
        setConfirming(false);
        onDecided?.(decided);
      },
      (failure: unknown) => {
        if (failure instanceof ApiError && failure.code === "CONFIRM_REQUIRED") {
          setConfirming(true);
          return;
        }
        setError(failure instanceof Error ? failure.message : String(failure));
      },
    );
  };
  const allow = () => (dangerous ? setConfirming(true) : send(true));

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    // Only when the card itself is focused: never from the note, a button, or anywhere else.
    if (!pending || event.target !== event.currentTarget || event.metaKey || event.ctrlKey || event.altKey || event.repeat) return;
    if (event.key === "y" || event.key === "Y") {
      event.preventDefault();
      allow();
    } else if (event.key === "n" || event.key === "N") {
      event.preventDefault();
      send(false);
    }
  };

  return (
    <section
      ref={ref}
      tabIndex={pending ? 0 : -1}
      onKeyDown={onKeyDown}
      aria-labelledby={titleId}
      data-approval={approval.id}
      data-risk={approval.risk}
      className={cn(
        "min-w-0 rounded-md border bg-surface outline-none focus-visible:ring-2 focus-visible:ring-accent",
        dangerous ? "border-danger/50" : approval.risk === "elevated" ? "border-warning/50" : "border-border",
        className,
      )}
    >
      <header className={cn("flex items-start gap-2 border-b px-3 py-2", dangerous ? "border-danger/30 bg-danger/5" : "border-border bg-surface-raised")}>
        <ToolIcon tool={approval.tool_name} />
        <div className="min-w-0 flex-1">
          <h3 id={titleId} className="break-words text-sm font-semibold text-heading">
            {requester(approval)} wants to {verb(approval.tool_name)}
          </h3>
          <p className="flex flex-wrap items-center gap-1 pt-0.5 text-2xs text-muted">
            <Chip tone={riskTone[approval.risk]}>
              {approval.risk !== "low" && <AlertTriangle className="h-3 w-3" aria-hidden />}
              {riskLabels[approval.risk]}
            </Chip>
            <Chip mono>{approval.tool_name}</Chip>
            {approval.job_id && <Chip mono>{approval.job_id}</Chip>}
            <span>{relativeTime(approval.created_at)}</span>
          </p>
        </div>
      </header>
      <div className="flex flex-col gap-2.5 p-3">
        <CallBody approval={approval} compact={compact} />
        <p
          data-testid="risk-line"
          className={cn("flex items-start gap-1.5 text-xs", dangerous ? "font-medium text-danger" : approval.risk === "elevated" ? "text-warning" : "text-muted")}
        >
          {approval.risk !== "low" && <ShieldAlert className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden />}
          <span>{riskLine(approval)}</span>
        </p>
        {!pending ? (
          <Banner tone={approval.status === "allowed" ? "success" : "info"} className="rounded">
            {outcome(approval)}
          </Banner>
        ) : confirming ? (
          <div role="alertdialog" aria-labelledby={`${titleId}-confirm`} className="flex flex-col gap-2 rounded border border-danger/50 bg-danger/5 p-2.5">
            <p id={`${titleId}-confirm`} className="text-sm font-semibold text-danger">
              This is dangerous. Allow it anyway?
            </p>
            <p className="text-xs text-body">{(approval.risk_reasons.join(". ") || "It matched a dangerous rule") + ". It runs as you, now, once."}</p>
            <div className="flex flex-wrap gap-2">
              <Button variant="danger" icon={Check} disabled={decide.isPending} onClick={() => send(true, true)}>
                Yes, allow it
              </Button>
              <Button variant="secondary" disabled={decide.isPending} onClick={() => setConfirming(false)}>
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <>
            <div className="flex flex-col gap-1">
              <label htmlFor={noteId} className="text-2xs text-muted">
                Note to the agent (optional; sent with a deny)
              </label>
              <textarea
                id={noteId}
                value={note}
                onChange={(event) => setNote(event.target.value)}
                rows={compact ? 1 : 2}
                maxLength={2000}
                placeholder="e.g. use printf instead"
                className="min-h-11 w-full resize-y rounded border border-border-strong bg-surface px-2 py-1.5 text-sm text-heading placeholder:text-muted md:min-h-8 md:text-xs"
              />
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button variant={dangerous ? "warning" : "primary"} icon={Check} disabled={decide.isPending} onClick={allow}>
                {dangerous ? "Allow…" : "Allow once"}
              </Button>
              <Button variant="secondary" icon={X} disabled={decide.isPending} onClick={() => send(false)}>
                Deny
              </Button>
              {onOpenTerminal && (
                <Button variant="ghost" icon={SquareTerminal} onClick={onOpenTerminal}>
                  Open terminal
                </Button>
              )}
            </div>
          </>
        )}
        {error && (
          <Banner tone="danger" role="alert" className="rounded">
            {error}
          </Banner>
        )}
        {!compact && pending && (
          <p className="text-2xs text-muted">
            The same request is also on the agent&apos;s terminal; whichever is answered first wins. &ldquo;Always allow&rdquo; is
            only offered in the terminal.
          </p>
        )}
      </div>
    </section>
  );
});

function verb(tool: string): string {
  switch (toolKind(tool)) {
    case "bash":
      return "run a command";
    case "edit":
      return tool === "Write" ? "write a file" : "edit a file";
    case "fetch":
      return "fetch a web page";
    case "mcp":
      return "call an MCP tool";
    default:
      return `use ${tool}`;
  }
}

function ToolIcon({ tool }: { tool: string }) {
  const kind = toolKind(tool);
  const Icon = kind === "bash" ? Terminal : kind === "edit" ? FileDiff : kind === "fetch" ? Globe : kind === "mcp" ? Plug : Wrench;
  return <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted" aria-hidden />;
}

function Code({ children, label, compact }: { children: ReactNode; label: string; compact?: boolean }) {
  return (
    <pre
      aria-label={label}
      tabIndex={0}
      className={cn(
        "overflow-auto whitespace-pre-wrap break-words rounded border border-border bg-surface-sunken p-2 font-mono text-xs leading-snug text-heading",
        compact ? "max-h-28" : "max-h-72",
      )}
    >
      {children}
    </pre>
  );
}

/** The call itself (command, diff, URL, arguments), as the card shows it; the decision packet reuses it. */
export function CallBody({ approval, compact }: { approval: Approval; compact?: boolean }) {
  const input = approval.tool_input;
  switch (toolKind(approval.tool_name)) {
    case "bash":
      return (
        <div className="flex flex-col gap-1">
          <Code label="Command" compact={compact}>
            {bashCommand(approval)}
          </Code>
          {(approval.cwd || typeof input.description === "string") && (
            <p className="text-2xs text-muted">
              {typeof input.description === "string" && <span className="text-body">{input.description}</span>}
              {approval.cwd && (
                <>
                  {typeof input.description === "string" && " · "}in <span className="font-mono">{approval.cwd}</span>
                </>
              )}
            </p>
          )}
        </div>
      );
    case "edit":
      return <DiffView files={editFiles(approval)} layout={compact ? "unified" : undefined} />;
    case "fetch":
      return (
        <div className="flex flex-col gap-1">
          <Code label="URL" compact={compact}>
            {String(input.url ?? "")}
          </Code>
          {typeof input.prompt === "string" && <p className="text-2xs text-muted">Asking: {input.prompt}</p>}
        </div>
      );
    case "mcp": {
      const { server, tool } = mcpParts(approval.tool_name);
      return (
        <div className="flex flex-col gap-1">
          <p className="text-xs text-body">
            Server <span className="font-mono text-heading">{server}</span>, tool <span className="font-mono text-heading">{tool}</span>
          </p>
          <Code label="Arguments" compact={compact}>
            {prettyInput(input)}
          </Code>
        </div>
      );
    }
    default:
      return (
        <Code label="Input" compact={compact}>
          {prettyInput(input)}
        </Code>
      );
  }
}
