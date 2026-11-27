// The signed-in frame: a dark header (session, connection state, speak mode when voice is on,
// theme, user menu) with the now-playing strip under it, the five
// views as a left rail from md and a bottom nav on mobile, the reconnect banner, and the
// footer, the command palette (⌘K; components/palette) and the needs-you tray (components/tray). It owns the one event stream (lib/live.ts), which keeps every query fresh. The Inbox
// badge and the tab title count what needs Jeff: open questions, pending tool-call approvals, and agents
// at a terminal dialog (one whose pane has a pending approval counts once, as the approval).
import { useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate, useRouter, useRouterState } from "@tanstack/react-router";
import { useEffect } from "react";
import { endpoints, fixturesEnabled } from "../lib/api";
import { cn } from "../lib/cn";
import { inboxCount, needsYouCount, pageTitle } from "../lib/fleet";
import { listenForNavigation, registerServiceWorker } from "../lib/push";
import { useLiveQueries } from "../lib/live";
import { useApprovals, useFleet, useInboxSummary, useMe } from "../lib/queries";
import type { StreamStatus } from "../lib/sse";
import { useNoStrayFileDrops } from "./attach/useAttachments";
import { ConnectionBanner } from "./ConnectionBanner";
import { PauseBanner } from "./control/PauseBanner";
import { usePresenceHeartbeat } from "./control/usePresenceHeartbeat";
import { Footer } from "./Footer";
import { navItems } from "./nav";
import { PaletteButton } from "./palette/PaletteButton";
import { PaletteProvider } from "./palette/PaletteProvider";
import { ThemeToggle } from "./ThemeToggle";
import { TrayBar, TrayPanel } from "./tray/NeedsYouTray";
import { UserMenu } from "./UserMenu";
import { NowPlaying, SpeakToggle } from "./voice/SpeakMode";
import { useSyncPlaybackRate } from "../lib/voice/settings";

export function AppFrame() {
  useNoStrayFileDrops();
  usePresenceHeartbeat();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const me = useMe();
  const stream = useLiveQueries();
  const fleet = useFleet();
  const summary = useInboxSummary();
  const approvals = useApprovals("pending");
  const needsYou = inboxCount(summary.data?.open, needsYouCount(fleet.data, approvals.data)) + (approvals.data?.length ?? 0);
  const voice = me.data?.features.voice ?? false;
  useSyncPlaybackRate(voice);

  const name = me.data?.features.name;
  useEffect(() => {
    document.title = pageTitle(needsYou, name);
  }, [needsYou, name]);

  // Web Push (DESIGN.md 8.5): keep the service worker registered (it caches nothing; it only shows
  // pushes), and follow a tapped notification to its item without a reload.
  const router = useRouter();
  useEffect(() => {
    if (!fixturesEnabled()) void registerServiceWorker();
    return listenForNavigation((path) => router.history.push(path));
  }, [router]);

  const signOut = async () => {
    try {
      await endpoints.logout();
    } finally {
      queryClient.clear();
      await navigate({ to: "/login", search: { redirect: undefined } });
    }
  };

  return (
    <PaletteProvider onSignOut={() => void signOut()}>
    <div className="flex h-full flex-col">
      <header className="box-content flex h-11 shrink-0 items-center gap-2 border-b border-chrome-border bg-chrome pl-3 pr-1 pt-[env(safe-area-inset-top)] text-chrome-fg md:h-10">
        <img src="/icons/crowsnest.svg" alt="" className="h-5 w-5" />
        <span className="hidden text-sm font-semibold tracking-tight sm:inline">crowsnest</span>
        {me.data?.features.name && (
          <span className="min-w-0 max-w-[40%] truncate text-sm font-semibold text-chrome-fg" title={`This crowsnest is ${me.data.features.name}`}>
            {me.data.features.name}
          </span>
        )}
        {me.data && (
          <span
            className="min-w-0 truncate rounded bg-chrome-hover px-1.5 py-0.5 font-mono text-xs text-chrome-fg"
            title={`herdr session ${me.data.features.session}`}
          >
            {me.data.features.session}
          </span>
        )}
        {me.data?.features.demo && <span className="rounded bg-chrome-warning/20 px-1.5 py-0.5 text-xs text-chrome-warning">demo</span>}
        {fixturesEnabled() && (
          <span className="rounded bg-chrome-warning/20 px-1.5 py-0.5 text-xs text-chrome-warning" title="Data comes from lib/fixtures.ts">
            fixtures
          </span>
        )}
        <PaletteButton className="ml-auto sm:mr-1" />
        <div className="flex items-center">
          <ConnectionState status={stream.status} />
          {voice && <SpeakToggle />}
          <ThemeToggle />
          {me.data && <UserMenu me={me.data} onSignOut={() => void signOut()} />}
        </div>
      </header>
      <ConnectionBanner stream={stream} />
      <PauseBanner />
      {voice && <NowPlaying />}
      <div className="flex min-h-0 flex-1">
        <nav aria-label="Views" className="hidden w-44 shrink-0 flex-col gap-0.5 border-r border-border bg-surface-raised p-2 md:flex">
          {navItems.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              activeOptions={{ exact: item.to === "/", includeSearch: false }}
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
      <TrayBar />
      <TrayPanel />
      <nav
        aria-label="Views"
        className="flex shrink-0 border-t border-border bg-surface-raised pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {navItems.map((item) => (
          <Link
            key={item.to}
            to={item.to}
            activeOptions={{ exact: item.to === "/", includeSearch: false }}
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
    </PaletteProvider>
  );
}

/** A count pill; nothing when the count is zero. */
function Badge({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      aria-label={`${count} need you`}
      className={cn(
        "inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-status-blocked px-1 text-[10px] font-semibold leading-none text-white tabular-nums dark:text-surface",
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
