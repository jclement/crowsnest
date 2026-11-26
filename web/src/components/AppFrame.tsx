// The signed-in frame: a dark h-10 header, the four views as a left rail from md and a bottom
// nav on mobile, the connection banner, and the footer. It owns the one event stream: `hello`
// (every connect) invalidates all queries, because the server never replays missed events.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { LogOut } from "lucide-react";
import { endpoints, queryKeys } from "../lib/api";
import { useEventStream } from "../lib/sse";
import { ConnectionBanner } from "./ConnectionBanner";
import { Footer } from "./Footer";
import { navItems } from "./nav";
import { ThemeToggle } from "./ThemeToggle";

export function AppFrame() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const me = useQuery({ queryKey: queryKeys.me, queryFn: endpoints.me });
  const status = useEventStream({ hello: () => void queryClient.invalidateQueries() });

  const signOut = async () => {
    try {
      await endpoints.logout();
    } finally {
      queryClient.clear();
      await navigate({ to: "/login", search: { redirect: undefined } });
    }
  };

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10 shrink-0 items-center gap-2 bg-chrome pl-3 pr-1 text-chrome-fg pt-[env(safe-area-inset-top)] box-content">
        <img src="/icons/crowsnest.svg" alt="" className="h-5 w-5" />
        <span className="text-sm font-semibold tracking-tight">crowsnest</span>
        {me.data && (
          <span className="truncate rounded bg-chrome-hover px-1.5 py-0.5 font-mono text-xs text-chrome-muted" title="herdr session">
            {me.data.features.session}
          </span>
        )}
        {me.data?.features.demo && <span className="rounded bg-warning/20 px-1.5 py-0.5 text-xs text-warning">demo</span>}
        <div className="ml-auto flex items-center">
          <ThemeToggle />
          {me.data?.features.auth && (
            <button
              type="button"
              onClick={signOut}
              title={`Sign out ${me.data.username}`}
              aria-label="Sign out"
              className="inline-flex h-10 w-10 items-center justify-center rounded text-chrome-muted hover:bg-chrome-hover hover:text-chrome-fg"
            >
              <LogOut className="h-4 w-4" />
            </button>
          )}
        </div>
      </header>
      <ConnectionBanner status={status} />
      <div className="flex min-h-0 flex-1">
        <nav aria-label="Views" className="hidden w-44 shrink-0 flex-col gap-0.5 border-r border-border bg-surface-raised p-2 md:flex">
          {navItems.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              activeOptions={{ exact: true }}
              className="flex h-9 items-center gap-2 rounded px-2 text-sm text-body hover:bg-hover data-[status=active]:bg-selected data-[status=active]:font-medium data-[status=active]:text-heading"
            >
              <item.icon className="h-4 w-4" aria-hidden />
              {item.label}
            </Link>
          ))}
          <Footer className="mt-auto px-2 pt-2" />
        </nav>
        <main className="flex min-w-0 flex-1 flex-col overflow-y-auto">
          <div className="min-h-0 flex-1">
            <Outlet />
          </div>
          <Footer className="border-t border-border px-3 py-2 md:hidden" />
        </main>
      </div>
      <nav
        aria-label="Views"
        className="flex shrink-0 border-t border-border bg-surface-raised pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {navItems.map((item) => (
          <Link
            key={item.to}
            to={item.to}
            activeOptions={{ exact: true }}
            className="flex min-h-11 flex-1 flex-col items-center justify-center gap-0.5 text-xs text-muted data-[status=active]:text-accent"
          >
            <item.icon className="h-5 w-5" aria-hidden />
            {item.label}
          </Link>
        ))}
      </nav>
    </div>
  );
}
