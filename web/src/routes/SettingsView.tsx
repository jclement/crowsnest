// The Settings view (`/settings`, R8): one door to configuration. It replaced nine: the Settings,
// Appearance, Account, Notifications, Voice, Instructions and Limits sheets, the account menu's
// entries for them, and System's Storage and tailnet cards. Four groups of sections:
//
//   This device    Appearance, Notifications, Voice (kept in this browser, or for this device)
//   This instance  Account and name, Limits (the throttle now, and its settings), Instance (the
//                  server's settings), Backups and storage, Tailnet, System (a link: its live
//                  status, the doctor and the machines stay a view of their own)
//   Agents         Agent settings (models, engine, permission mode, Remote Control), Instructions
//                  (global, each role, each project), Schedules
//   Safety         Approval rules, Guard (protected hosts and branches), Pause and Do not disturb
//
// Each section has an anchor (`/settings#limits`): the palette's old sheet targets, the manual's
// `app:sheet/<name>` buttons and every old link land on their section through PaletteProvider's
// openSheet (lib/settingsSections.ts maps a sheet to its section). A section list sits at the
// left from md and at the top on a phone. Pause and Do not disturb stay switches in the header's
// account menu; this view only says where they are and what they do.
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { ChevronRight, SlidersHorizontal } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { AccountSection } from "../components/account/AccountSheet";
import { AppearanceBody } from "../components/appearance/AppearanceSheet";
import { LimitsBody } from "../components/control/LimitsSheet";
import { HelpButton } from "../components/help/HelpButton";
import { InstructionsPanel } from "../components/instructions/InstructionsSheet";
import { usePaletteMaybe } from "../components/palette/palette-context";
import { NotificationSettingsBody } from "../components/push/NotificationSettings";
import { StorageCard, TailscaleCard } from "../components/system/OpsCards";
import { PublishCard } from "../components/system/PublishCard";
import { NetworkSettings, ServerSettings } from "../components/system/SettingsSheet";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { SkeletonRows } from "../components/ui/Skeleton";
import { VoiceSettingsBody } from "../components/voice/VoiceSettings";
import { useMe, useSettingsPage } from "../lib/queries";
import { browserPushEnvironment } from "../lib/push";
import { settingsGroups, type SettingsSectionId } from "../lib/settingsSections";
import type { SettingsPage } from "../lib/system";

export function SettingsView() {
  const { hash } = useLocation();
  const me = useMe();
  const voice = !!me.data?.features.voice;
  const groups = settingsGroups(voice);

  // A link to a section (#limits) scrolls to it, now and whenever the hash changes; again a moment
  // later, since the sections above it load their data and grow after the first paint.
  useEffect(() => {
    if (!hash) return;
    const scroll = () => document.getElementById(`settings-${hash}`)?.scrollIntoView({ block: "start" });
    scroll();
    const timers = [150, 500, 1200].map((ms) => window.setTimeout(scroll, ms));
    return () => timers.forEach((timer) => window.clearTimeout(timer));
  }, [hash]);

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-4 p-3 md:flex-row md:p-4">
      <nav aria-label="Settings sections" className="shrink-0 md:sticky md:top-4 md:w-48 md:self-start">
        <h1 className="mb-2 flex items-center gap-2 text-lg font-semibold text-heading">
          <SlidersHorizontal className="h-5 w-5 text-muted" aria-hidden />
          Settings
          <HelpButton topic="reference/settings" what="every setting and flag" className="self-center" />
        </h1>
        <ul className="flex flex-wrap gap-x-3 gap-y-1 md:flex-col md:gap-0">
          {groups.map((group) => (
            <li key={group.title} className="md:mb-2">
              <p className="hidden px-2 pb-0.5 text-2xs font-semibold uppercase tracking-wide text-muted md:block">{group.title}</p>
              <ul className="flex flex-wrap gap-x-3 md:flex-col md:gap-0">
                {group.sections.map((section) => (
                  <li key={section.id}>
                    <Link
                      to="/settings"
                      hash={section.id}
                      data-current={hash === section.id || undefined}
                      className="inline-flex min-h-11 items-center rounded px-2 text-sm text-accent hover:bg-hover data-[current]:font-medium md:min-h-7 md:text-body md:data-[current]:bg-selected md:data-[current]:text-heading"
                    >
                      {section.title}
                    </Link>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      </nav>
      <div className="flex min-w-0 flex-1 flex-col gap-6">
        {groups.map((group) => (
          <div key={group.title} className="flex flex-col gap-4">
            <h2 className="border-b border-border pb-1 text-xs font-semibold uppercase tracking-wide text-muted">{group.title}</h2>
            {group.sections.map((section) => (
              <SettingsSection key={section.id} id={section.id} title={section.title} help={section.help}>
                <SectionBody id={section.id} />
              </SettingsSection>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}

function SettingsSection({ id, title, help, children }: { id: SettingsSectionId; title: string; help?: { topic: string; what: string }; children: ReactNode }) {
  return (
    <section id={`settings-${id}`} aria-labelledby={`settings-${id}-title`} className="scroll-mt-4 rounded-md border border-border bg-surface">
      <header className="flex items-center gap-2 border-b border-border bg-surface-raised px-3 py-2">
        <h3 id={`settings-${id}-title`} className="text-sm font-semibold text-heading">
          {title}
        </h3>
        {help && <HelpButton topic={help.topic} what={help.what} inline={false} />}
      </header>
      <div className="flex flex-col gap-4 p-3">{children}</div>
    </section>
  );
}

/** What each section shows. */
function SectionBody({ id }: { id: SettingsSectionId }) {
  switch (id) {
    case "appearance":
      return <AppearanceBody />;
    case "notifications":
      return <NotificationSettingsBody env={browserPushEnvironment()} />;
    case "voice":
      return <VoiceSettingsBody />;
    case "account":
      return <AccountSection />;
    case "limits":
      return (
        <>
          <LimitsBody />
          <Server sections={["Throttling and review"]} />
        </>
      );
    case "instance":
      // The server's "Appearance" section (the default theme) is Appearance's own "Use for new
      // browsers", so it is not drawn twice.
      return <Server sections={["Budgets and acceptance", "Orchestrator upkeep", "Attachments", "Notices"]} network />;
    case "backups":
      return (
        <>
          <StorageCard />
          <Server sections={["Backups and storage"]} titled="Backups and storage" />
        </>
      );
    case "tailnet":
      return (
        <>
          <PublishCard />
          <TailscaleCard />
          <Server sections={["Tailnet"]} titled="Tailnet" />
        </>
      );
    case "system":
      return <SystemLink />;
    case "agents":
      return <Server sections={["Agents"]} />;
    case "instructions":
      return <InstructionsPanel />;
    case "schedules":
      return <SchedulesLink />;
    case "approvals":
      return <ApprovalRules />;
    case "guard":
      return <Server sections={["Guard"]} />;
    case "pause":
      return <PauseAndQuiet />;
  }
}

/** The server's sections named, from the one settings page every section shares. */
function Server({ sections, network, titled }: { sections: string[]; network?: boolean; titled?: string }) {
  const page = useSettingsPage();
  if (page.isPending) return <SkeletonRows rows={3} label="Loading the settings" />;
  if (page.isError) return <Banner tone="danger">Could not load the settings: {(page.error as Error).message}</Banner>;
  return <ServerBody page={page.data} sections={sections} network={network} titled={titled} />;
}

function ServerBody({ page, sections, network, titled }: { page: SettingsPage; sections: string[]; network?: boolean; titled?: string }) {
  return (
    <>
      <ServerSettings page={page} sections={sections} titled={titled} />
      {network && <NetworkSettings page={page} />}
    </>
  );
}

function SystemLink() {
  return (
    <p className="flex flex-wrap items-center gap-2 text-xs text-body">
      The machine, the doctor's checks, the versions and the machines this instance knows are live status, not settings: they are on the System view.
      <Link to="/system" className="inline-flex min-h-11 items-center gap-1 font-medium text-accent hover:underline md:min-h-0">
        Open System <ChevronRight className="h-3.5 w-3.5" aria-hidden />
      </Link>
    </p>
  );
}

function SchedulesLink() {
  const palette = usePaletteMaybe();
  return (
    <div className="flex flex-col gap-2 text-xs text-body">
      <p>
        Scheduled tasks (for the orchestrator, a lead or a job) are kept in the Schedule; a project's own are on its page. There are no instance-wide
        defaults for them: each schedule says its cadence and target.
      </p>
      <Button compact className="self-start" onClick={() => palette?.openSheet("schedule")}>
        Open the Schedule
      </Button>
    </div>
  );
}

function ApprovalRules() {
  const navigate = useNavigate();
  return (
    <div className="flex flex-col gap-2 text-xs text-body">
      <p>
        Approval rules are kept per project (and per job), so a rule written for one repository never allows anything in another. A dangerous call is
        never allowed by a rule; an elevated one only by a rule that says so. Add, preview and remove them on a project's page, under Approval rules.
      </p>
      <Button compact className="self-start" onClick={() => void navigate({ to: "/projects" })}>
        Open Projects
      </Button>
    </div>
  );
}

function PauseAndQuiet() {
  return (
    <p className="text-xs text-body">
      <span className="font-medium text-heading">Pause automation</span> (crowsnest stops acting by itself; agents keep running) and{" "}
      <span className="font-medium text-heading">Do not disturb</span> (pushes held until a time, then one summary) are switches in the account menu at
      the top right, so they are one tap from every view. Neither has defaults to set here.
    </p>
  );
}
