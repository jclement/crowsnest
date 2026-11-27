// The Settings view's sections (routes/SettingsView.tsx, R8) and the one map from the old doors to
// them: every sheet a palette item, a manual `app:sheet/<name>` button or the account menu used to
// open for configuration lands on its section of /settings instead (PaletteProvider's openSheet).

export type SettingsSectionId =
  | "appearance"
  | "notifications"
  | "voice"
  | "account"
  | "limits"
  | "instance"
  | "backups"
  | "tailnet"
  | "system"
  | "agents"
  | "instructions"
  | "schedules"
  | "approvals"
  | "guard"
  | "pause";

export interface SettingsSection {
  id: SettingsSectionId;
  title: string;
  /** The manual section its "?" opens. */
  help?: { topic: string; what: string };
}

export interface SettingsGroup {
  title: string;
  sections: SettingsSection[];
}

/** The groups and their sections, in order; Voice only with talk mode on. */
export function settingsGroups(voice: boolean): SettingsGroup[] {
  const device: SettingsSection[] = [
    { id: "appearance", title: "Appearance", help: { topic: "how-to/appearance", what: "themes and modes" } },
    { id: "notifications", title: "Notifications", help: { topic: "how-to/phone#turn-on-notifications", what: "notifications" } },
  ];
  if (voice) device.push({ id: "voice", title: "Voice", help: { topic: "how-to/voice#speak-mode-vs-the-mic", what: "speak mode and the mic" } });
  return [
    { title: "This device", sections: device },
    {
      title: "This instance",
      sections: [
        { id: "account", title: "Account and name" },
        { id: "limits", title: "Limits", help: { topic: "concepts/budgets-limits-pause#limits-on-running-jobs", what: "the running-job limits" } },
        { id: "instance", title: "Budgets, upkeep and notices", help: { topic: "reference/settings", what: "every setting and flag" } },
        { id: "backups", title: "Backups and storage" },
        { id: "tailnet", title: "Tailnet" },
        { id: "system", title: "System" },
      ],
    },
    {
      title: "Agents",
      sections: [
        { id: "agents", title: "Agent settings" },
        { id: "instructions", title: "Instructions and roles", help: { topic: "concepts/instruction-layers", what: "instruction layers" } },
        { id: "schedules", title: "Schedules" },
      ],
    },
    {
      title: "Safety",
      sections: [
        { id: "approvals", title: "Approval rules" },
        { id: "guard", title: "Protected hosts and branches" },
        { id: "pause", title: "Pause and do not disturb", help: { topic: "concepts/budgets-limits-pause#the-pause", what: "the pause" } },
      ],
    },
  ];
}

/** True when id names a section of the Settings view (Voice included, which needs talk mode). */
export function isSettingsSection(id: string): id is SettingsSectionId {
  return settingsGroups(true).some((group) => group.sections.some((section) => section.id === id));
}

/** The sheets that were doors to configuration, and the section each now lands on. */
export const SHEET_SECTIONS: Record<string, SettingsSectionId> = {
  settings: "instance",
  appearance: "appearance",
  notifications: "notifications",
  voice: "voice",
  account: "account",
  instructions: "instructions",
  limits: "limits",
};

/** The section an old sheet name lands on, or undefined for a sheet that is not configuration. */
export function sectionForSheet(sheet: string): SettingsSectionId | undefined {
  return SHEET_SECTIONS[sheet];
}
