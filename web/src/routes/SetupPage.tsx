// The first-run setup page (/setup). Crowsnest prints a link with a one-time token in its log;
// opening it lands here with the token prefilled. After setup the server stores the owner, signs
// this browser in and restarts itself as the full server (herdr, the orchestrator), which takes
// a few seconds: the page waits for /health to say it is ready, then opens the app.
import { useEffect, useState } from "react";
import { FirstLight } from "../components/brand/FirstLight";
import { endpoints } from "../lib/api";
import { captureSetupToken } from "../lib/setupToken";
import { SetupForm } from "./SetupForm";

const POLL_MS = 500;

export function SetupPage({ defaultName, minLength }: { defaultName: string; minLength: number }) {
  const [token] = useState(captureSetupToken);
  const [doneAs, setDoneAs] = useState<string | null>(null);

  return (
    <FirstLight>
      {doneAs === null ? (
        <>
          <h1 className="font-mono text-xs font-semibold uppercase tracking-wider text-heading">Set up crowsnest</h1>
          <p className="mb-4 mt-1 text-xs text-muted">
            Choose the username and password for this crowsnest. Anyone who has them has a shell on this machine, so make the
            password a long one.
          </p>
          <SetupForm initialToken={token} defaultName={defaultName} minLength={minLength} onDone={setDoneAs} />
        </>
      ) : (
        <Starting username={doneAs} />
      )}
    </FirstLight>
  );
}

/** Waits for the full server, then opens the app (a full load, so nothing from setup lingers). */
function Starting({ username }: { username: string }) {
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    let stopped = false;
    const started = Date.now();
    const poll = async () => {
      while (!stopped) {
        try {
          const health = await endpoints.health();
          if (!health.setup_required) {
            window.location.replace("/welcome");
            return;
          }
        } catch {
          // The setup server has closed and the full one is not listening yet.
        }
        if (Date.now() - started > 20_000) setSlow(true);
        await new Promise((resolve) => setTimeout(resolve, POLL_MS));
      }
    };
    void poll();
    return () => {
      stopped = true;
    };
  }, []);
  return (
    <div role="status" className="flex flex-col gap-2">
      <h1 className="text-base font-semibold text-heading">Starting crowsnest…</h1>
      <p className="text-xs text-muted">
        Signed in as <span className="font-medium text-heading">{username}</span>. Crowsnest is starting up; this page opens a short checklist when it
        is ready.
      </p>
      {slow && <p className="text-xs text-warning">This is taking a while. Check crowsnest&apos;s log if it does not come up.</p>}
    </div>
  );
}
