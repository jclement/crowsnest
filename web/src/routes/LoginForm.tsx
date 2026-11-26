// The login form, free of routing so it can be tested on its own. It calls the API and reports
// success through onSuccess; wrong credentials and lockouts show the server's message.
import { useState, type FormEvent } from "react";
import { ApiError, endpoints, type Me } from "../lib/api";

interface LoginFormProps {
  onSuccess: (me: Me) => void;
}

export function LoginForm({ onSuccess }: LoginFormProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      onSuccess(await endpoints.login(username, password));
    } catch (caught) {
      setError(messageFor(caught));
      setPassword("");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3" noValidate={false}>
      <label className="flex flex-col gap-1 text-xs font-medium text-heading">
        Username
        <input
          name="username"
          autoComplete="username"
          autoCapitalize="none"
          required
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          className="h-11 rounded border border-border bg-surface px-3 text-sm font-normal text-heading md:h-9"
        />
      </label>
      <label className="flex flex-col gap-1 text-xs font-medium text-heading">
        Password
        <input
          name="password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          className="h-11 rounded border border-border bg-surface px-3 text-sm font-normal text-heading md:h-9"
        />
      </label>
      {error && (
        <p role="alert" className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
          {error}
        </p>
      )}
      <button
        type="submit"
        disabled={submitting || !username || !password}
        className="h-11 rounded bg-accent text-sm font-medium text-accent-fg hover:opacity-90 disabled:opacity-50 md:h-9"
      >
        {submitting ? "Signing in…" : "Sign in"}
      </button>
    </form>
  );
}

function messageFor(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return "Wrong username or password.";
    return error.message;
  }
  return "Sign-in failed. Try again.";
}
