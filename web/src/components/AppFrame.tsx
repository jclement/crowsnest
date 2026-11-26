// The signed-in frame: a dark header (session, connection state, theme, user menu), the five
// views as a left rail from md and a bottom nav on mobile, the reconnect banner, and the
// footer. It owns the one event stream (lib/live.ts), which keeps every query fresh. The Inbox
// badge and the tab title count what needs Jeff: open questions plus agents at a terminal dialog.
import { useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useEffect } from "react";
import { endpoints, fixturesEnabled } from "../lib/api";
import { cn } from "../lib/cn";
import { inboxCount, needsYouCount } from "../lib/fleet";
import { useLiveQueries } from "../lib/live";
import { useFleet, useInboxSummary, useMe } from "../lib/queries";
import type { StreamStatus } from "../lib/sse";
import { ConnectionBanner } from "./ConnectionBanner";
import { Footer } from "./Footer";
import { navItems } from "./nav";
import { ThemeToggle } from "./ThemeToggle";
import { UserMenu } from "./UserMenu";

export function AppFrame() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const me = useMe();
  const stream = useLiveQueries();
  const fleet = useFleet();
  const summary = useInboxSummary();
  const needsYou = inboxCount(summary.data?.open, needsYouCount(fleet.data));

  useEffect(() => {
    document.title = needsYou > 0 ? `(${needsYou}) crowsnest` : "crowsnest";
  }, [needsYou]);

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
      <header className="box-content flex h-11 shrink-0 items-center gap-2 border-b border-chrome-border bg-chrome pl-3 pr-1 pt-[env(safe-area-inset-top)] text-chrome-fg md:h-10">
        <img src="/icons/crowsnest.svg" alt="" className="h-5 w-5" />
        <span className="hidden text-sm font-semibold tracking-tight sm:inline">crowsnest</span>
        {me.data && (
          <span
            className="min-w-0 truncate rounded bg-chrome-hover px-1.5 py-0.5 font-mono text-xs text-chrome-fg"
            title={`herdr session ${me.data.features.session}`}
          >
            {me.data.features.session}
          </span>
        )}
        {me.data?.features.demo && <span className="rounded bg-warning/20 px-1.5 py-0.5 text-xs text-warning">demo</span>}
        {fixturesEnabled() && (
          <span className="rounded bg-warning/20 px-1.5 py-0.5 text-xs text-warning" title="Data comes from lib/fixtures.ts">
            fixtures
          </span>
        )}
        <div className="ml-auto flex items-center">
          <ConnectionState status={stream.status} />
          <ThemeToggle />
          {me.data && <UserMenu me={me.data} onSignOut={() => void signOut()} />}
        </div>
      </header>
      <ConnectionBanner stream={stream} />
      <div className="flex min-h-0 flex-1">
        <nav aria-label="Views" className="hidden w-44 shrink-0 flex-col gap-0.5 border-r border-border bg-surface-raised p-2 md:flex">
          {navItems.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              activeOptions={{ exact: true, includeSearch: false }}
              className="flex h-8 items-center gap-2 rounded px-2 text-sm text-body hover:bg-hover data-[status=active]:bg-selected data-[status=active]:font-medium data-[status=active]:text-heading"
            >
              <item.icon className="h-4 w-4" aria-hidden />
              {item.label}
              {item.badge && <Badge count={needsYou} className="ml-auto" />}
            </Link>
          ))}
          <Footer stacked className="mt-auto px-2 pt-2" />
        </nav>
        <main className="flex min-w-0 flex-1 flex-col overflow-y-auto">
          {/* min-h-0 only from md: on a phone the view grows with its content and the footer
              follows it; on desktop views like Fleet fill the height and scroll inside. */}
          <div className="flex-1 md:min-h-0">
            <Outlet />
          </div>
          {/* Not under Chat on a phone: its composer sticks to the bottom, and a footer below it
              would show up under the composer at the end of the conversation. */}
          {pathname !== "/chat" && <Footer className="border-t border-border px-3 py-2 md:hidden" />}
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
            activeOptions={{ exact: true, includeSearch: false }}
            className="relative flex min-h-12 min-w-0 flex-1 flex-col items-center justify-center gap-0.5 text-2xs text-muted data-[status=active]:font-medium data-[status=active]:text-accent"
          >
            <span className="relative">
              <item.icon className="h-5 w-5" aria-hidden />
              {item.badge && <Badge count={needsYou} className="absolute -right-2.5 -top-1.5" />}
            </span>
            {item.label}
          </Link>
        ))}
      </nav>
    </div>
  );
}

/** A count pill; nothing when the count is zero. */
function Badge({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      aria-label={`${count} need you`}
      className={cn(
        "inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-status-blocked px-1 text-[10px] font-semibold leading-none text-white tabular-nums",
        className,
      )}
    >
      {count > 99 ? "99+" : count}
    </span>
  );
}

const connectionLabels: Record<StreamStatus, string> = {
  open: "Live",
  connecting: "Connecting",
  reconnecting: "Offline",
};

/** A dot and a word for the event stream: live updates on, or not. */
function ConnectionState({ status }: { status: StreamStatus }) {
  return (
    <span
      role="status"
      title={status === "open" ? "Live updates connected" : "Live updates not connected"}
      className="mr-1 inline-flex items-center gap-1.5 px-1.5 text-xs text-chrome-muted"
    >
      <span
        aria-hidden
        className={cn(
          "h-2 w-2 rounded-full",
          status === "open" ? "bg-status-done" : status === "reconnecting" ? "bg-warning" : "animate-shimmer bg-chrome-muted",
        )}
      />
      <span className="hidden sm:inline">{connectionLabels[status]}</span>
      <span className="sr-only sm:hidden">{connectionLabels[status]}</span>
    </span>
  );
}
