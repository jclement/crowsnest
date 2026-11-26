// The login page: a tagline, the form, and the footer. After signing in it returns to the page
// that sent the user here (only same-site paths, never an absolute URL).
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Footer } from "../components/Footer";
import { ThemeToggle } from "../components/ThemeToggle";
import { queryKeys, type Me } from "../lib/api";
import { safeRedirect } from "../lib/format";
import { randomTagline } from "../lib/taglines";
import { LoginForm } from "./LoginForm";

export function LoginPage({ redirect }: { redirect?: string }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [tagline] = useState(randomTagline);

  const signedIn = (me: Me) => {
    queryClient.setQueryData(queryKeys.me, me);
    void navigate({ href: safeRedirect(redirect), replace: true });
  };

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex h-10 shrink-0 items-center gap-2 bg-chrome pl-3 pr-1 text-chrome-fg">
        <img src="/icons/crowsnest.svg" alt="" className="h-5 w-5" />
        <span className="text-sm font-semibold">crowsnest</span>
        <div className="ml-auto">
          <ThemeToggle />
        </div>
      </header>
      <main className="flex flex-1 items-center justify-center p-4">
        <div className="w-full max-w-xs">
          <h1 className="text-base font-semibold text-heading">Sign in</h1>
          <p className="mb-4 mt-1 text-xs italic text-muted">{tagline}</p>
          <LoginForm onSuccess={signedIn} />
        </div>
      </main>
      <Footer className="px-3 py-2 text-center" />
    </div>
  );
}
