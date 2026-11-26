// The first-run setup form, free of routing so it can be tested on its own: the setup token
// (prefilled from the link's fragment), a username, the password twice with a strength hint,
// and the instance name. It reports success through onDone; the server's message is shown for
// anything it refuses (a wrong token, a lockout, a field it did not accept).
import { useState, type FormEvent } from "react";
import { ApiError, endpoints } from "../lib/api";
import { cn } from "../lib/cn";
import { passwordStrength } from "../lib/password";

interface SetupFormProps {
  initialToken: string;
  defaultName: string;
  minLength: number;
  onDone: (username: string) => void;
}

const inputClass = "h-11 rounded border border-border bg-surface px-3 text-sm font-normal text-heading md:h-9";
const labelClass = "flex flex-col gap-1 text-xs font-medium text-heading";

export function SetupForm({ initialToken, defaultName, minLength, onDone }: SetupFormProps) {
  const [token, setToken] = useState(initialToken);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [name, setName] = useState(defaultName);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const strength = passwordStrength(password, username.trim(), minLength);
  const mismatch = confirm !== "" && confirm !== password;
  const ready = token.trim() !== "" && username.trim() !== "" && strength.strength !== "short" && confirm === password && !submitting;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!ready) return;
    setSubmitting(true);
    setError(null);
    try {
      const result = await endpoints.setup({ token: token.trim(), username: username.trim(), password, name: name.trim() });
      onDone(result.username);
    } catch (caught) {
      setError(messageFor(caught));
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <label className={labelClass}>
        Setup token
        <input
          name="token"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          required
          value={token}
          onChange={(event) => setToken(event.target.value)}
          aria-describedby="setup-token-hint"
          className={cn(inputClass, "font-mono text-xs")}
        />
      </label>
      <p id="setup-token-hint" className="-mt-2 text-2xs text-muted">
        {initialToken ? "From the link you opened." : "Printed in crowsnest's log as part of the setup link (after #token=)."}
      </p>
      <label className={labelClass}>
        Username
        <input
          name="username"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          required
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          className={inputClass}
        />
      </label>
      <label className={labelClass}>
        Password
        <input
          name="password"
          type="password"
          autoComplete="new-password"
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          aria-describedby="setup-strength"
          className={inputClass}
        />
      </label>
      <StrengthMeter id="setup-strength" score={strength.score} label={password ? strength.label : ""} hint={strength.hint} />
      <label className={labelClass}>
        Password again
        <input
          name="confirm"
          type="password"
          autoComplete="new-password"
          required
          value={confirm}
          onChange={(event) => setConfirm(event.target.value)}
          aria-invalid={mismatch}
          className={inputClass}
        />
      </label>
      {mismatch && <p className="-mt-2 text-2xs text-danger">The two passwords differ.</p>}
      <label className={labelClass}>
        Instance name
        <input
          name="name"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={32}
          value={name}
          onChange={(event) => setName(event.target.value)}
          aria-describedby="setup-name-hint"
          className={inputClass}
        />
      </label>
      <p id="setup-name-hint" className="-mt-2 text-2xs text-muted">
        Shown in the header, the tab title and notifications. Letters, digits, - and _.
      </p>
      {error && (
        <p role="alert" className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
          {error}
        </p>
      )}
      <button
        type="submit"
        disabled={!ready}
        className="h-11 rounded bg-accent text-sm font-medium text-accent-fg hover:opacity-90 disabled:opacity-50 md:h-9"
      >
        {submitting ? "Setting up…" : "Set up crowsnest"}
      </button>
    </form>
  );
}

/** Four segments and a line of advice. Colour is never the only cue: the label says it too. */
export function StrengthMeter({ id, score, label, hint }: { id: string; score: number; label: string; hint: string }) {
  const tone = score >= 3 ? "bg-success" : score === 2 ? "bg-warning" : "bg-danger";
  return (
    <div id={id} className="-mt-1 flex flex-col gap-1" aria-live="polite">
      <div className="flex gap-1" aria-hidden>
        {[0, 1, 2].map((index) => (
          <span key={index} className={cn("h-1 flex-1 rounded-full", label && index < Math.max(score, 1) ? tone : "bg-border")} />
        ))}
      </div>
      <p className="text-2xs text-muted">
        {label && <span className="font-medium text-heading">{label}. </span>}
        {hint}
      </p>
    </div>
  );
}

function messageFor(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 410) return "Crowsnest is already set up. Sign in instead.";
    return error.message;
  }
  return "Setup failed. Try again.";
}
