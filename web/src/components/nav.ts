// The views, in nav order (DESIGN.md 12 and 22). Both the phone's bottom nav and the desktop rail
// read this, so they agree. Four primary destinations (R4 Fable 1.4, Codex C4): Now (home: what
// needs you, every project, since you left), Projects, the Inbox (the badge) and Chat. The rest
// are one tap further: on a phone under "More" (a sheet), on the desktop rail below a divider
// in the same order. The Jobs board stays a view of its own; Agents (the herdr tree and the session
// terminal) is the recovery and debugging tool, reachable from any agent's "Open terminal" as well. Help (the
// manual) and Settings are last.
import { FolderKanban, Gauge, Inbox, LifeBuoy, ListTodo, MessageSquare, Settings, Ship, type LucideIcon } from "lucide-react";

export type NavPath = "/" | "/jobs" | "/projects" | "/chat" | "/inbox" | "/agents" | "/help" | "/settings";

export interface NavItem {
  to: NavPath;
  label: string;
  icon: LucideIcon;
  /** Shows the attention queue's count as a badge. */
  badge?: "needsYou";
}

/** The four on the phone's bottom bar and at the top of the desktop rail. */
export const primaryNav: NavItem[] = [
  { to: "/", label: "Now", icon: Gauge },
  { to: "/projects", label: "Projects", icon: FolderKanban },
  { to: "/inbox", label: "Inbox", icon: Inbox, badge: "needsYou" },
  { to: "/chat", label: "Chat", icon: MessageSquare },
];

/** Under "More" on a phone; below the divider on the desktop rail. */
export const moreNav: NavItem[] = [
  { to: "/jobs", label: "Jobs", icon: ListTodo },
  // The herdr tree and the session terminal, one view (R8: Fleet and Terminal were two).
  { to: "/agents", label: "Agents", icon: Ship },
  // The built-in manual (docs/manual): how crowsnest works and how to do things with it.
  { to: "/help", label: "Help", icon: LifeBuoy },
  // R10 F-F8 / O-F8: Settings was only behind the account icon; it is the last of More.
  { to: "/settings", label: "Settings", icon: Settings },
];

/** Every view, in order. */
export const navItems: NavItem[] = [...primaryNav, ...moreNav];

/** True when pathname is one of the More views (the phone's More button shows as current). */
export function inMore(pathname: string): boolean {
  return moreNav.some((item) => pathname === item.to || pathname.startsWith(item.to + "/"));
}
