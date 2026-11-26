// The Account sheet, opened from the account menu: who is signed in and where that login comes
// from, changing the password (it needs the current one and signs every other browser out), and
// the instance name. A login set by --username/CROWSNEST_PASSWORD, or a name set by --name, is
// managed outside crowsnest and shown read-only with where to change it.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { ApiError, endpoints, queryKeys, type Account } from "../../lib/api";
import { passwordStrength } from "../../lib/password";
import { StrengthMeter } from "../../routes/SetupForm";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

const inputClass = "h-11 w-full rounded border border-border bg-surface px-3 text-sm font-normal text-heading md:h-9";
const labelClass = "flex flex-col gap-1 text-xs font-medium text-heading";

export function AccountSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const account = useQuery({ queryKey: queryKeys.account, queryFn: endpoints.account, enabled: open });
  return (
    <Sheet open={open} onClose={onClose} title="Account" size="md">
      <div className="flex flex-col gap-5 overflow-y-auto p-4">
        {account.isPending && <p className="text-xs text-muted">Loading…</p>}
        {account.isError && <p className="text-xs text-danger">Could not load the account. {(account.error as Error).message}</p>}
        {account.data && <AccountBody account={account.data} />}
      </div>
    </Sheet>
  );
}

const sourceLabels: Record<Account["source"], string> = {
  database: "Chosen at setup",
  config: "Managed outside crowsnest",
  none: "Login off",
};

export function AccountBody({ account }: { account: Account }) {
  return (
    <>
      <section className="flex flex-col gap-1">
        <h3 className="text-2xs uppercase tracking-wide text-muted">Login</h3>
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium text-heading">{account.source === "none" ? "Nobody (--no-auth)" : account.username}</span>
          <Chip>{sourceLabels[account.source]}</Chip>
        </div>
      </section>
      <section className="flex flex-col gap-2">
        <h3 className="text-2xs uppercase tracking-wide text-muted">Password</h3>
        {account.source === "database" && <PasswordForm username={account.username} minLength={account.min_password_length} />}
        {account.source === "config" && (
          <p className="text-xs text-muted">
            This login is set by <code>--username</code> and <code>CROWSNEST_PASSWORD</code> (or a password file). Change it there and restart crowsnest.
          </p>
        )}
        {account.source === "none" && <p className="text-xs text-muted">Crowsnest runs with --no-auth, so there is no password.</p>}
      </section>
      <section className="flex flex-col gap-2">
        <h3 className="text-2xs uppercase tracking-wide text-muted">Instance name</h3>
        <NameForm account={account} />
      </section>
    </>
  );
}

function PasswordForm({ username, minLength }: { username: string; minLength: number }) {
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const change = useMutation({ mutationFn: () => endpoints.changePassword(current, next) });
  const strength = passwordStrength(next, username, minLength);
  const mismatch = confirm !== "" && confirm !== next;
  const ready = current !== "" && strength.strength !== "short" && confirm === next && !change.isPending;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!ready) return;
    setError(null);
    // mutateAsync(...).then, not mutate's callbacks (DESIGN.md decision 51).
    change
      .mutateAsync()
      .then(() => {
        setCurrent("");
        setNext("");
        setConfirm("");
        toast.show({ tone: "success", title: "Password changed", description: "Every other browser is signed out." });
      })
      .catch((caught: unknown) => {
        setError(caught instanceof ApiError ? caught.message : "Could not change the password. Try again.");
        setCurrent("");
      });
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <label className={labelClass}>
        Current password
        <input type="password" name="current-password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} className={inputClass} />
      </label>
      <label className={labelClass}>
        New password
        <input
          type="password"
          name="new-password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          aria-describedby="account-strength"
          className={inputClass}
        />
      </label>
      <StrengthMeter id="account-strength" score={strength.score} label={next ? strength.label : ""} hint={strength.hint} />
      <label className={labelClass}>
        New password again
        <input
          type="password"
          name="confirm-password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          aria-invalid={mismatch}
          className={inputClass}
        />
      </label>
      {mismatch && <p className="-mt-2 text-2xs text-danger">The two passwords differ.</p>}
      {error && (
        <p role="alert" className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
          {error}
        </p>
      )}
      <p className="text-2xs text-muted">Changing it signs every other browser out; this one stays signed in.</p>
      <div>
        <Button type="submit" variant="primary" disabled={!ready}>
          {change.isPending ? "Changing…" : "Change password"}
        </Button>
      </div>
    </form>
  );
}

function NameForm({ account }: { account: Account }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [name, setName] = useState(account.name);
  const [error, setError] = useState<string | null>(null);
  const save = useMutation({ mutationFn: () => endpoints.setName(name.trim()) });

  if (account.name_managed) {
    return (
      <p className="text-xs text-muted">
        <span className="font-medium text-heading">{account.name}</span>, set by <code>--name</code> or <code>CROWSNEST_NAME</code>. Change it there and
        restart crowsnest.
      </p>
    );
  }
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    save
      .mutateAsync()
      .then(() => {
        void queryClient.invalidateQueries({ queryKey: queryKeys.me });
        void queryClient.invalidateQueries({ queryKey: queryKeys.account });
        toast.show({ tone: "success", title: "Name saved", description: "Notifications and Remote Control use it from the next start." });
      })
      .catch((caught: unknown) => setError(caught instanceof ApiError ? caught.message : "Could not save the name."));
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <label className={labelClass}>
        Name
        <input name="instance-name" maxLength={32} autoCapitalize="none" spellCheck={false} value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
      </label>
      {error && (
        <p role="alert" className="text-xs text-danger">
          {error}
        </p>
      )}
      <div>
        <Button type="submit" disabled={save.isPending || name.trim() === "" || name.trim() === account.name}>
          {save.isPending ? "Saving…" : "Save name"}
        </Button>
      </div>
    </form>
  );
}
