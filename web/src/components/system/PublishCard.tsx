// "Publish to your tailnet" (DESIGN.md 3.5.1): the System page's card and the first-run
// checklist's step. Before: the exact https://…ts.net address it will create and what that means
// (anyone on the tailnet who can reach this machine gets the sign-in page), then Publish. After:
// the address as a link, a QR code for the phone (drawn by the server from the URL, no network),
// Stop publishing, and the state: publishing, stopped, or failed with the reason and the fix.
// Hidden when Tailscale is absent or logged out and publishing is off.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleStop, ExternalLink, Globe } from "lucide-react";
import type { ReactNode } from "react";
import { ApiError } from "../../lib/api";
import { publishPoll, safeTailscaleLink, tailnetEndpoints, tailnetKeys, type PublishState, type PublishView } from "../../lib/tailnet";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip, type ChipTone } from "../ui/Chip";
import { useToast } from "../ui/toast-context";

const stateChip: Record<PublishState, { label: string; tone: ChipTone }> = {
  unavailable: { label: "not available", tone: "neutral" },
  off: { label: "not published", tone: "neutral" },
  stopped: { label: "stopped", tone: "neutral" },
  starting: { label: "starting", tone: "accent" },
  publishing: { label: "publishing", tone: "success" },
  failed: { label: "failed", tone: "danger" },
  refused: { label: "cannot publish", tone: "warning" },
  external: { label: "published elsewhere", tone: "neutral" },
};

function errorText(error: unknown): string {
  return error instanceof ApiError || error instanceof Error ? error.message : String(error);
}

/**
 * The card. `bare` drops the Card frame (the checklist puts it in its own list); `fallback` is
 * shown instead while the card is hidden (Tailscale absent or logged out).
 */
export function PublishCard({ bare = false, fallback = null }: { bare?: boolean; fallback?: ReactNode }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const view = useQuery({
    queryKey: tailnetKeys.view,
    queryFn: tailnetEndpoints.view,
    refetchInterval: (query) => publishPoll(query.state.data?.state),
  });
  const onSuccess = (data: PublishView) => queryClient.setQueryData(tailnetKeys.view, data);
  const publish = useMutation({
    mutationFn: tailnetEndpoints.publish,
    onSuccess,
    onError: (error) => toast.show({ tone: "danger", title: "Could not publish to your tailnet", description: errorText(error) }),
  });
  const stop = useMutation({
    mutationFn: tailnetEndpoints.stop,
    onSuccess,
    onError: (error) => toast.show({ tone: "danger", title: "Could not stop publishing", description: errorText(error) }),
  });
  const data = view.data;
  if (!data || !data.show) return fallback;
  const chip = stateChip[data.state];
  const body = <PublishBody view={data} busy={publish.isPending || stop.isPending} onPublish={() => publish.mutate()} onStop={() => stop.mutate()} />;
  if (bare) return body;
  return (
    <Card
      aria-label="Publish to your tailnet"
      title={
        <>
          <Globe className="h-3.5 w-3.5" aria-hidden /> Publish to your tailnet
        </>
      }
      actions={<Chip tone={chip.tone}>{chip.label}</Chip>}
    >
      {body}
    </Card>
  );
}

interface BodyProps {
  view: PublishView;
  busy: boolean;
  onPublish: () => void;
  onStop: () => void;
}

function PublishBody({ view, busy, onPublish, onStop }: BodyProps) {
  const live = view.state === "starting" || view.state === "publishing" || view.state === "failed";
  return (
    <div className="flex flex-col gap-2 text-xs" data-state={view.state}>
      {view.state === "publishing" && view.url && <Published view={view} />}
      {(view.state === "off" || view.state === "stopped") && view.url && <Offer view={view} />}
      {view.state === "starting" && <p className="text-body">Starting tailscale serve for {view.url ? <span className="font-mono">{view.url}</span> : "this machine"}…</p>}
      {view.state === "external" && view.url && (
        <p className="text-body">
          Already reachable at{" "}
          <a className="break-all font-mono text-accent underline" href={view.url} target="_blank" rel="noopener noreferrer">
            {view.url}
          </a>
          .
        </p>
      )}
      {view.reason && <Problem view={view} />}
      {view.state === "failed" && view.retry_at && <p className="text-2xs text-muted">Trying again at {new Date(view.retry_at).toLocaleTimeString()}.</p>}
      {view.note && <p className="text-2xs text-muted">{view.note}</p>}
      {view.locked && <p className="text-2xs text-muted">{view.locked}.</p>}
      <div className="flex flex-wrap gap-2">
        {(view.state === "off" || view.state === "stopped") && (
          <Button variant="primary" icon={Globe} onClick={onPublish} disabled={busy || (view.locked !== undefined && view.locked !== "")}>
            {busy ? "Publishing…" : "Publish"}
          </Button>
        )}
        {(live || (view.enabled && view.state === "refused")) && (
          <Button icon={CircleStop} onClick={onStop} disabled={busy}>
            {busy ? "Stopping…" : live ? "Stop publishing" : "Turn off"}
          </Button>
        )}
      </div>
    </div>
  );
}

/** Before: exactly what it creates, and what that means. */
function Offer({ view }: { view: PublishView }) {
  return (
    <>
      <p className="text-body">
        This creates <span className="break-all font-mono font-medium text-heading">{view.url}</span> with <code>tailscale serve</code>, for as long as crowsnest
        runs, and crowsnest does it again at every start until you stop it.
      </p>
      <p className="text-muted">
        Anyone on your tailnet who can reach this machine gets crowsnest's sign-in page there; the password still stands in front of everything. Nothing is
        published to the internet.
      </p>
      {view.port_note && <p className="text-muted">{view.port_note}</p>}
    </>
  );
}

/** After: the link and the QR code for the phone. */
function Published({ view }: { view: PublishView }) {
  return (
    <div className="flex flex-wrap items-start gap-3">
      {view.qr && <img src={view.qr} alt={`QR code for ${view.url}`} width={128} height={128} className="h-32 w-32 rounded border border-border bg-white p-1 [image-rendering:pixelated]" />}
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <a className="inline-flex items-center gap-1 break-all font-mono text-sm text-accent underline" href={view.url} target="_blank" rel="noopener noreferrer">
          {view.url} <ExternalLink className="h-3.5 w-3.5 shrink-0" aria-hidden />
        </a>
        <p className="text-muted">Scan it with your phone's camera, on a phone signed in to your tailnet. It stops when crowsnest stops.</p>
        {view.port_note && <p className="text-muted">{view.port_note}</p>}
      </div>
    </div>
  );
}

function Problem({ view }: { view: PublishView }) {
  const link = safeTailscaleLink(view.fix_url);
  return (
    <div role={view.state === "failed" ? "alert" : undefined} className="flex flex-col gap-1">
      <p className={view.state === "failed" ? "text-danger" : "text-body"}>{view.reason}</p>
      {view.fix && <p className="text-muted">{view.fix}</p>}
      {link && (
        <a className="inline-flex items-center gap-1 break-all text-accent underline" href={link} target="_blank" rel="noopener noreferrer">
          {link} <ExternalLink className="h-3.5 w-3.5 shrink-0" aria-hidden />
        </a>
      )}
    </div>
  );
}
