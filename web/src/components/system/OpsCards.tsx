// Day-thirty cards for the System page (DESIGN.md 4.17 to 4.21, 12.9): the update notice, the
// Storage card with a clean-up preview, the Machines list with this instance's read tokens, and
// the Sign in with Tailscale allowlist. Each card owns its query; the server does the work.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, ExternalLink, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { ApiError } from "../../lib/api";
import { backupURL, formatBytes, opsEndpoints, opsKeys, type CleanupReport, type Machine } from "../../lib/ops";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { useToast } from "../ui/toast-context";

const inputClass = "h-11 w-full min-w-0 rounded border border-border bg-surface px-3 text-sm text-heading md:h-9";

function errorText(error: unknown): string {
  return error instanceof ApiError || error instanceof Error ? error.message : String(error);
}

function day(iso?: string): string {
  return iso ? new Date(iso).toLocaleDateString() : "-";
}

// ---- Update notice -------------------------------------------------------------

/** "Updated from vA to vB: what changed", until dismissed. */
export function UpdatedNotice() {
  const queryClient = useQueryClient();
  const notice = useQuery({ queryKey: opsKeys.updated, queryFn: opsEndpoints.updated, staleTime: 5 * 60_000 });
  const dismiss = useMutation({ mutationFn: opsEndpoints.dismissUpdated, onSuccess: () => queryClient.setQueryData(opsKeys.updated, null) });
  if (!notice.data) return null;
  return (
    <Banner
      tone="success"
      action={
        <Button compact variant="ghost" onClick={() => dismiss.mutate()}>
          Dismiss
        </Button>
      }
    >
      Updated from <span className="font-mono">{notice.data.from}</span> to <span className="font-mono">{notice.data.to}</span>.{" "}
      {notice.data.release_url ? (
        <a className="underline" href={notice.data.release_url} target="_blank" rel="noopener noreferrer">
          What changed
        </a>
      ) : (
        "A development build: see the commit log for what changed."
      )}
    </Banner>
  );
}

// ---- Storage --------------------------------------------------------------------

/** What uses how much, the last clean-up, and "Clean up now" with a preview first. */
export function StorageCard() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const storage = useQuery({ queryKey: opsKeys.storage, queryFn: opsEndpoints.storage });
  const [preview, setPreview] = useState<CleanupReport | null>(null);
  const [confirm, setConfirm] = useState<string[]>([]);
  const plan = useMutation({ mutationFn: () => opsEndpoints.cleanup(true), onSuccess: (report) => (setPreview(report), setConfirm([])) });
  const run = useMutation({
    mutationFn: () => opsEndpoints.cleanup(false, confirm),
    onSuccess: (report) => {
      setPreview(null);
      void queryClient.invalidateQueries({ queryKey: opsKeys.storage });
      toast.show({ tone: "success", title: "Cleaned up", description: describe(report) });
    },
    onError: (error) => toast.show({ tone: "danger", title: "Clean-up failed", description: errorText(error) }),
  });
  return (
    <Card
      title="Storage"
      actions={
        <a href={backupURL} download className="inline-flex min-h-11 items-center gap-1.5 rounded border border-border-strong px-2 text-xs font-medium text-heading hover:bg-hover md:min-h-7">
          <Download className="h-4 w-4" aria-hidden /> Download backup
        </a>
      }
    >
      {storage.isError && <p className="text-xs text-danger">Could not measure storage. {errorText(storage.error)}</p>}
      {storage.data && (
        <div className="flex flex-col gap-3">
          <ul className="grid gap-x-4 gap-y-1 text-xs sm:grid-cols-2">
            {storage.data.areas.map((area) => (
              <li key={area.name} className="flex justify-between gap-2">
                <span className="text-muted">{area.label}</span>
                <span className="font-mono tabular-nums text-body">
                  {formatBytes(area.bytes)}
                  {area.count > 0 && ` · ${area.count}`}
                  {area.oldest && ` · since ${day(area.oldest)}`}
                </span>
              </li>
            ))}
          </ul>
          <details className="text-xs">
            <summary className="cursor-pointer text-muted">Rows in the database</summary>
            <ul className="mt-1 grid gap-x-4 gap-y-0.5 sm:grid-cols-2">
              {storage.data.tables.map((table) => (
                <li key={table.table} className="flex justify-between gap-2">
                  <span className="text-muted">{table.label}</span>
                  <span className="font-mono tabular-nums">
                    {table.rows}
                    {table.oldest && ` · oldest ${day(table.oldest)}`}
                  </span>
                </li>
              ))}
            </ul>
          </details>
          <p className="text-2xs text-muted">
            Settled jobs leave the boards after {storage.data.policy.ArchiveAfterDays || "never"}
            {storage.data.policy.ArchiveAfterDays ? " days" : ""}; settled history is deleted after {storage.data.policy.DeleteAfterDays || "never"}
            {storage.data.policy.DeleteAfterDays ? " days" : ""} (Settings, Backups and storage). Last clean-up:{" "}
            {storage.data.last_run ? new Date(storage.data.last_run.at).toLocaleString() : "not yet"}. {storage.data.worktrees}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button compact icon={Trash2} onClick={() => plan.mutate()} disabled={plan.isPending}>
              {plan.isPending ? "Looking…" : "Clean up now…"}
            </Button>
          </div>
          {preview && <CleanupPreview report={preview} confirm={confirm} setConfirm={setConfirm} onRun={() => run.mutate()} running={run.isPending} onCancel={() => setPreview(null)} />}
        </div>
      )}
    </Card>
  );
}

function describe(report: CleanupReport): string {
  const rows = report.sweeps.reduce((sum, sweep) => sum + sweep.count, 0);
  return `${report.archive} jobs archived, ${report.delete_jobs + report.confirmed} deleted, ${rows} rows of old history, ${report.state_dirs} job folders.`;
}

interface PreviewProps {
  report: CleanupReport;
  confirm: string[];
  setConfirm: (ids: string[]) => void;
  onRun: () => void;
  running: boolean;
  onCancel: () => void;
}

/** The dry run: what would go, and the held jobs that go only when ticked. */
function CleanupPreview({ report, confirm, setConfirm, onRun, running, onCancel }: PreviewProps) {
  const toggle = (id: string) => setConfirm(confirm.includes(id) ? confirm.filter((other) => other !== id) : [...confirm, id]);
  return (
    <div className="flex flex-col gap-2 rounded border border-border bg-surface-sunken p-3 text-xs" aria-label="Clean-up preview">
      <p className="font-medium text-heading">This clean-up would:</p>
      <ul className="list-disc pl-5">
        <li>archive {report.archive} settled jobs (hidden from the boards, still searchable)</li>
        <li>delete {report.delete_jobs} settled jobs that held no unlanded work, with their timelines</li>
        {report.sweeps
          .filter((sweep) => sweep.count > 0)
          .map((sweep) => (
            <li key={sweep.name}>
              delete {sweep.count} × {sweep.label.toLowerCase()}
            </li>
          ))}
        <li>
          remove {report.state_dirs} job folders ({formatBytes(report.state_bytes)})
        </li>
      </ul>
      {report.held.length > 0 && (
        <fieldset className="flex flex-col gap-1">
          <legend className="font-medium text-heading">Kept unless you tick them: they may hold work nobody has landed</legend>
          {report.held.map((job) => (
            <label key={job.id} className="flex min-h-11 items-start gap-2 md:min-h-0">
              <input type="checkbox" className="mt-0.5" checked={confirm.includes(job.id)} onChange={() => toggle(job.id)} />
              <span>
                <span className="font-mono">{job.id}</span> {job.title} <span className="text-muted">({job.reason}; settled {day(job.settled_at)})</span>
              </span>
            </label>
          ))}
        </fieldset>
      )}
      <div className="flex flex-wrap gap-2">
        <Button compact variant="danger" onClick={onRun} disabled={running}>
          {running ? "Cleaning up…" : confirm.length > 0 ? `Clean up, and delete ${confirm.length} ticked` : "Clean up"}
        </Button>
        <Button compact variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

// ---- Machines ---------------------------------------------------------------------

/** The other crowsnest instances, their headline figures, and this one's read tokens. */
export function MachinesCard() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const machines = useQuery({ queryKey: opsKeys.machines, queryFn: opsEndpoints.machines, refetchInterval: 60_000 });
  const [form, setForm] = useState({ name: "", url: "", token: "" });
  const add = useMutation({
    mutationFn: () => opsEndpoints.addMachine(form),
    onSuccess: () => (setForm({ name: "", url: "", token: "" }), void queryClient.invalidateQueries({ queryKey: opsKeys.machines })),
    onError: (error) => toast.show({ tone: "danger", title: "Could not add the machine", description: errorText(error) }),
  });
  const remove = useMutation({
    mutationFn: (id: number) => opsEndpoints.removeMachine(id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: opsKeys.machines }),
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    add.mutate();
  };
  return (
    <Card title="Machines">
      <div className="flex flex-col gap-3">
        <p className="text-2xs text-muted">
          One crowsnest per machine, each with its own herdr. Add the others here with a read token minted on each (its System page, Machines): this list shows
          what needs you there and links to it. Nothing here controls another machine.
        </p>
        {machines.isError && <p className="text-xs text-danger">{errorText(machines.error)}</p>}
        {machines.data && machines.data.length === 0 && <p className="text-xs text-muted">No other machines yet.</p>}
        <ul className="flex flex-col gap-2">
          {machines.data?.map((machine) => (
            <MachineRow key={machine.id} machine={machine} onRemove={() => remove.mutate(machine.id)} />
          ))}
        </ul>
        <form onSubmit={submit} className="grid gap-2 sm:grid-cols-[1fr_2fr_2fr_auto]" aria-label="Add a machine">
          <input className={inputClass} placeholder="name" aria-label="Machine name" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} />
          <input
            className={inputClass}
            placeholder="https://bedev.tail1234.ts.net"
            aria-label="Machine address"
            autoCapitalize="none"
            spellCheck={false}
            value={form.url}
            onChange={(event) => setForm({ ...form, url: event.target.value })}
          />
          <input
            className={inputClass}
            placeholder="cnk_… (read token from that machine)"
            aria-label="Read token"
            type="password"
            autoComplete="off"
            value={form.token}
            onChange={(event) => setForm({ ...form, token: event.target.value })}
          />
          <Button type="submit" compact disabled={add.isPending || !form.name || !form.url || !form.token}>
            Add
          </Button>
        </form>
        <TokensSection />
      </div>
    </Card>
  );
}

function MachineRow({ machine, onRemove }: { machine: Machine; onRemove: () => void }) {
  const summary = machine.summary;
  return (
    <li className="flex flex-wrap items-center gap-2 rounded border border-border px-2 py-1.5 text-xs">
      <a href={machine.url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-medium text-heading underline">
        {machine.name} <ExternalLink className="h-3 w-3" aria-hidden />
      </a>
      {summary ? (
        <>
          <Chip tone={summary.attention > 0 ? "warning" : "success"}>{summary.attention} need you</Chip>
          <Chip>{summary.running} running</Chip>
          {summary.paused && <Chip tone="warning">paused</Chip>}
          {summary.stats && (
            <span className="font-mono tabular-nums text-muted">
              CPU {Math.round(summary.stats.cpu)}% · load {summary.stats.load1.toFixed(1)}
            </span>
          )}
          <span className="text-muted">{summary.version}</span>
        </>
      ) : (
        <span className="text-danger">{machine.error}</span>
      )}
      <Button compact variant="ghost" className="ml-auto" onClick={onRemove} aria-label={`Remove ${machine.name}`}>
        Remove
      </Button>
    </li>
  );
}

/** This instance's read tokens: mint (shown once) and revoke. */
function TokensSection() {
  const queryClient = useQueryClient();
  const tokens = useQuery({ queryKey: opsKeys.tokens, queryFn: opsEndpoints.tokens });
  const [name, setName] = useState("");
  const [minted, setMinted] = useState<string | null>(null);
  const mint = useMutation({
    mutationFn: () => opsEndpoints.mintToken(name),
    onSuccess: (result) => (setMinted(result.token), setName(""), void queryClient.invalidateQueries({ queryKey: opsKeys.tokens })),
  });
  const revoke = useMutation({ mutationFn: (id: number) => opsEndpoints.revokeToken(id), onSuccess: () => void queryClient.invalidateQueries({ queryKey: opsKeys.tokens }) });
  return (
    <section className="flex flex-col gap-2 border-t border-border pt-3" aria-label="Read tokens">
      <h3 className="text-2xs uppercase tracking-wide text-muted">This machine's read tokens</h3>
      <p className="text-2xs text-muted">A read token opens this machine's summary (GET /api/v1/summary) and nothing else. It is shown once.</p>
      {minted && (
        <Banner tone="warning" action={<Button compact variant="ghost" onClick={() => setMinted(null)}>Done</Button>}>
          Copy it now, it is not shown again: <code className="break-all font-mono">{minted}</code>
        </Banner>
      )}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          mint.mutate();
        }}
        className="flex gap-2"
      >
        <input className={inputClass} placeholder="who will use it, such as quark" aria-label="Token name" value={name} onChange={(event) => setName(event.target.value)} />
        <Button type="submit" compact disabled={!name || mint.isPending}>
          Mint a token
        </Button>
      </form>
      {mint.isError && <p className="text-xs text-danger">{errorText(mint.error)}</p>}
      <ul className="flex flex-col gap-1 text-xs">
        {tokens.data?.map((token) => (
          <li key={token.id} className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-heading">{token.name}</span>
            <span className="font-mono text-muted">…{token.hint}</span>
            <span className="text-muted">{token.last_used_at ? `used ${new Date(token.last_used_at).toLocaleString()}` : "never used"}</span>
            {token.revoked_at ? (
              <Chip>revoked</Chip>
            ) : (
              <Button compact variant="ghost" onClick={() => revoke.mutate(token.id)} aria-label={`Revoke ${token.name}`}>
                Revoke
              </Button>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

// ---- Sign in with Tailscale -------------------------------------------------------

/** Who may sign in with their tailnet identity (with --tailscale-auth). */
export function TailscaleCard() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const state = useQuery({ queryKey: opsKeys.tailscale, queryFn: opsEndpoints.tailscale });
  const [extra, setExtra] = useState("");
  const onError = (error: unknown) => toast.show({ tone: "danger", title: "Could not change the list", description: errorText(error) });
  const save = useMutation({
    mutationFn: (logins: string[]) => opsEndpoints.setTailscale(logins),
    onSuccess: (data) => (queryClient.setQueryData(opsKeys.tailscale, data), setExtra("")),
    onError,
  });
  const allowMe = useMutation({ mutationFn: opsEndpoints.allowMe, onSuccess: (data) => queryClient.setQueryData(opsKeys.tailscale, data), onError });
  const data = state.data;
  if (!data) return null;
  return (
    <Card title="Sign in with Tailscale">
      {!data.enabled ? (
        <p className="text-xs text-muted">
          Off. Start crowsnest with <code>--tailscale-auth</code> (<code>CROWSNEST_TAILSCALE_AUTH=true</code>) to let the tailnet logins you list here sign in
          without the password, through <code>tailscale serve</code> on this machine. Password sign-in always stays.
        </p>
      ) : (
        <div className="flex flex-col gap-2 text-xs">
          <p className="text-muted">
            {data.serve_configured ? "tailscale serve proxies to this crowsnest." : "No tailscale serve config points at this crowsnest yet: nobody can sign in this way until one does."}{" "}
            {data.current_login ? (
              <>
                You came through it as <span className="font-mono">{data.current_login}</span>.
              </>
            ) : (
              "This browser did not come through tailscale serve."
            )}
          </p>
          <ul className="flex flex-col gap-1">
            {data.allowed.map((login) => (
              <li key={login} className="flex items-center gap-2">
                <span className="font-mono">{login}</span>
                {!data.via_tailscale && (
                  <Button compact variant="ghost" onClick={() => save.mutate(data.allowed.filter((other) => other !== login))} aria-label={`Remove ${login}`}>
                    Remove
                  </Button>
                )}
              </li>
            ))}
            {data.allowed.length === 0 && <li className="text-muted">Nobody yet.</li>}
          </ul>
          {data.via_tailscale ? (
            <p className="text-2xs text-muted">Only a browser signed in with the password can change this list.</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {data.current_login && !data.allowed.includes(data.current_login) && (
                <Button compact variant="primary" onClick={() => allowMe.mutate()}>
                  Allow me ({data.current_login})
                </Button>
              )}
              <form
                className="flex gap-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  save.mutate([...data.allowed, extra]);
                }}
              >
                <input className={inputClass} placeholder="another login, such as friend@github" aria-label="Tailscale login" value={extra} onChange={(event) => setExtra(event.target.value)} />
                <Button type="submit" compact disabled={!extra}>
                  Allow
                </Button>
              </form>
            </div>
          )}
        </div>
      )}
    </Card>
  );
}
