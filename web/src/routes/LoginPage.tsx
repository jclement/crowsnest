// The login page: a tagline, the form, and the footer. After signing in it returns to the page
// that sent the user here (only same-site paths, never an absolute URL).
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { FirstLight } from "../components/brand/FirstLight";
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
    <FirstLight tagline={tagline} className="max-w-xs">
      <h1 className="mb-3 font-mono text-xs font-semibold uppercase tracking-wider text-heading">Sign in</h1>
      <LoginForm onSuccess={signedIn} />
    </FirstLight>
  );
}
