// The first-run setup page (/setup). Crowsnest prints a link with a one-time token in its log;
// opening it lands here with the token prefilled. After setup the server stores the owner, signs
// this browser in and restarts itself as the full server (herdr, the orchestrator), which takes
// a few seconds: the page waits for /health to say it is ready, then opens the app.
import { useEffect, useState } from "react";
import { Footer } from "../components/Footer";
import { ThemeToggle } from "../components/ThemeToggle";
import { endpoints } from "../lib/api";
import { captureSetupToken } from "../lib/setupToken";
import { SetupForm } from "./SetupForm";

const POLL_MS = 500;

export function SetupPage({ defaultName, minLength }: { defaultName: string; minLength: number }) {
  const [token] = useState(captureSetupToken);
  const [doneAs, setDoneAs] = useState<string | null>(null);

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 bg-chrome pl-3 pr-1 text-chrome-fg md:h-10">
        <img src="/icons/crowsnest.svg" alt="" className="h-5 w-5" />
        <span className="text-sm font-semibold">crowsnest</span>
        <div className="ml-auto">
          <ThemeToggle />
        </div>
      </header>
      <main className="flex flex-1 items-start justify-center p-4 md:items-center">
        <div className="w-full max-w-sm">
          {doneAs === null ? (
            <>
              <h1 className="text-base font-semibold text-heading">Set up crowsnest</h1>
              <p className="mb-4 mt-1 text-xs text-muted">
                Choose the username and password for this crowsnest. Anyone who has them has a shell on this machine, so make the
                password a long one.
              </p>
              <SetupForm initialToken={token} defaultName={defaultName} minLength={minLength} onDone={setDoneAs} />
            </>
          ) : (
            <Starting username={doneAs} />
          )}
        </div>
      </main>
      <Footer className="px-3 py-2 text-center" />
    </div>
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
            window.location.replace("/");
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
        Signed in as <span className="font-medium text-heading">{username}</span>. Crowsnest is starting its herdr session and orchestrator; this page
        opens the app when it is ready.
      </p>
      {slow && <p className="text-xs text-warning">This is taking a while. Check crowsnest&apos;s log if it does not come up.</p>}
    </div>
  );
}
