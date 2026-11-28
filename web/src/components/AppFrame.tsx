// The signed-in frame: a dark header (session, connection state, speak mode when voice is on,
// theme, user menu) with the now-playing strip under it, the five
// views as a left rail from md and a bottom nav on mobile, the reconnect banner, and the
// footer, the command palette (⌘K; components/palette) and the needs-you tray (components/tray). It owns the one event stream (lib/live.ts), which keeps every query fresh. The nav
// badge and the tab title show the attention queue's one number (counts.total from the server,
// DESIGN.md 22), the same number the tray, the palette, the Inbox and the Now view show.
import { useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate, useRouter, useRouterState } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { Activity, MoreHorizontal } from "lucide-react";
import { endpoints, fixturesEnabled } from "../lib/api";
import { cn } from "../lib/cn";
import { pageTitle } from "../lib/fleet";
import { listenForNavigation, registerServiceWorker } from "../lib/push";
import { useLiveQueries } from "../lib/live";
import { useAttention, useMe } from "../lib/queries";
import { playEarcon } from "../lib/voice/earcons";
import { speakModeOn } from "../lib/voice/speak";
import { useNoStrayFileDrops } from "./attach/useAttachments";
import { Heartbeat } from "./brand/Heartbeat";
import { Wordmark } from "./brand/Brand";
import { ConnectionBanner } from "./ConnectionBanner";
import { LimitBanner } from "./control/LimitBanner";
import { PauseBanner } from "./control/PauseBanner";
import { usePresenceHeartbeat } from "./control/usePresenceHeartbeat";
import { Footer } from "./Footer";
import { inMore, moreNav, primaryNav, type NavItem } from "./nav";
import { Sheet } from "./ui/Sheet";
import { useBrowserBeat } from "../lib/browserBeat";
import { PaletteButton } from "./palette/PaletteButton";
import { NewJobButton } from "./jobs/NewJobButton";
import { MachineStrip } from "./system/MachineStats";
import { HeavyChip } from "./control/HeavySlots";
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
  const attention = useAttention();
  const needsYou = attention.data?.counts.total ?? 0;
  const [moreOpen, setMoreOpen] = useState(false);
  useBrowserBeat();
  const voice = me.data?.features.voice ?? false;
  useSyncPlaybackRate(voice);

  const name = me.data?.features.name;
  useEffect(() => {
    document.title = pageTitle(needsYou, name);
  }, [needsYou, name]);

  // With speak mode on, something new joining the queue is an earcon (R4 Fable 4.1): Jeff on a
  // walk hears that something needs him without looking. Only a rise counts, never the first load.
  const previousNeeds = useRef<number | null>(null);
  useEffect(() => {
    if (!attention.data) return;
    if (previousNeeds.current !== null && needsYou > previousNeeds.current && speakModeOn()) playEarcon("needs-you");
    previousNeeds.current = needsYou;
  }, [needsYou, attention.data]);

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

  // The standalone diff is the viewer's full screen: no header, nav, tray or footer (DESIGN.md 6.21).
  if (pathname === "/diff") {
    return (
      <PaletteProvider onSignOut={() => void signOut()}>
        <div className="flex h-full flex-col">
          <ConnectionBanner stream={stream} />
          <main className="min-h-0 flex-1">
            <Outlet />
          </main>
        </div>
      </PaletteProvider>
    );
  }

  return (
    <PaletteProvider onSignOut={() => void signOut()}>
    <div className="flex h-full flex-col">
      <header className="box-content flex h-11 shrink-0 items-center gap-2 border-b border-chrome-border bg-chrome pl-3 pr-1 pt-[env(safe-area-inset-top)] text-chrome-fg md:h-10">
        <Wordmark compact />
        {me.data?.features.name && (
          <span className="min-w-0 max-w-[40%] truncate text-sm font-semibold text-chrome-fg" title={`This crowsnest is ${me.data.features.name}`}>
            {me.data.features.name}
          </span>
        )}
        {me.data && (
          <span
            className="hidden min-w-0 truncate rounded bg-chrome-hover px-1.5 py-0.5 font-mono text-xs text-chrome-fg sm:inline"
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
        <MachineStrip className="ml-auto" />
        <HeavyChip />
        <NewJobButton className="md:hidden" />
        <PaletteButton className="sm:mr-1" />
        <div className="flex items-center">
          <Heartbeat status={stream.status} />
          {voice && <SpeakToggle />}
          {/* On a phone its room goes to New job and the machine name (R10 F-F3, Fable polish):
              the mode is in Settings → Appearance and the palette there. */}
          <span className="hidden sm:contents">
            <ThemeToggle />
          </span>
          {me.data && <UserMenu me={me.data} onSignOut={() => void signOut()} />}
        </div>
      </header>
      <ConnectionBanner stream={stream} />
      <PauseBanner />
      <LimitBanner />
      {voice && <NowPlaying />}
      <div className="flex min-h-0 flex-1">
        <nav aria-label="Views" className="hidden w-44 shrink-0 flex-col gap-0.5 border-r border-border bg-surface-raised p-2 md:flex">
          {primaryNav.map((item) => (
            <RailLink key={item.to} item={item} count={needsYou} />
          ))}
          <p className="mt-3 px-2 pb-0.5 text-2xs font-semibold uppercase tracking-wide text-muted">More</p>
          {moreNav.map((item) => (
            <RailLink key={item.to} item={item} count={needsYou} />
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
          {pathname !== "/chat" && pathname !== "/decide" && <Footer className="border-t border-border px-3 py-2 md:hidden" />}
        </main>
        {/* Docked beside the view, in the layout: it never floats over content (R8). */}
        <TrayPanel />
      </div>
      <TrayBar />
      <nav
        aria-label="Views"
        className="flex shrink-0 border-t border-border bg-surface-raised pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {primaryNav.map((item) => (
          <Link
            key={item.to}
            to={item.to}
            activeOptions={{ exact: item.to === "/", includeSearch: false }}
            className={bottomItemClass}
          >
            <span className="relative">
              <item.icon className="h-5 w-5" aria-hidden />
              {item.badge && <Badge count={needsYou} className="absolute -right-2.5 -top-1.5" />}
            </span>
            {item.label}
          </Link>
        ))}
        <button
          type="button"
          onClick={() => setMoreOpen(true)}
          aria-haspopup="dialog"
          data-status={inMore(pathname) ? "active" : undefined}
          className={bottomItemClass}
        >
          <MoreHorizontal className="h-5 w-5" aria-hidden />
          More
        </button>
      </nav>
      <Sheet open={moreOpen} onClose={() => setMoreOpen(false)} title="More">
        <ul aria-label="More views" className="divide-y divide-border">
          {[...moreNav, { to: "/system" as const, label: "System", icon: Activity }].map((item) => (
            <li key={item.to}>
              <Link
                to={item.to}
                onClick={() => setMoreOpen(false)}
                className="flex min-h-12 items-center gap-3 px-3 text-sm text-heading hover:bg-hover data-[status=active]:font-medium data-[status=active]:text-accent"
              >
                <item.icon className="h-5 w-5 text-muted" aria-hidden />
                {item.label}
              </Link>
            </li>
          ))}
        </ul>
      </Sheet>
    </div>
    </PaletteProvider>
  );
}

const bottomItemClass =
  "relative flex min-h-12 min-w-0 flex-1 flex-col items-center justify-center gap-0.5 text-2xs text-muted data-[status=active]:font-medium data-[status=active]:text-accent";

/** One view on the desktop rail. */
function RailLink({ item, count }: { item: NavItem; count: number }) {
  return (
    <Link
      to={item.to}
      activeOptions={{ exact: item.to === "/", includeSearch: false }}
      className="flex h-8 items-center gap-2 rounded px-2 text-sm text-body hover:bg-hover data-[status=active]:bg-selected data-[status=active]:font-medium data-[status=active]:text-heading data-[status=active]:shadow-[inset_2px_0_0_var(--color-accent)]"
    >
      <item.icon className="h-4 w-4" aria-hidden />
      {item.label}
      {item.badge && <Badge count={count} className="ml-auto" />}
    </Link>
  );
}

/** A count pill; nothing when the count is zero. */
function Badge({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      aria-label={`${count} need you`}
      className={cn(
        "inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-status-blocked px-1 text-[10px] font-semibold leading-none text-status-fg tabular-nums",
        className,
      )}
    >
      {count > 99 ? "99+" : count}
    </span>
  );
}
