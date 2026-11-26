// The six views, in nav order (DESIGN.md sections 12 and 21). Both the mobile bottom nav and the
// desktop rail read this. Jobs is the default route; Projects sits beside it because a project
// is where jobs are filed.
import { FolderKanban, Inbox, ListTodo, MessageSquare, Ship, SquareTerminal, type LucideIcon } from "lucide-react";

export interface NavItem {
  to: "/" | "/projects" | "/chat" | "/inbox" | "/fleet" | "/terminal";
  label: string;
  icon: LucideIcon;
  /** Shows the needs-you count as a badge. */
  badge?: "needsYou";
}

export const navItems: NavItem[] = [
  { to: "/", label: "Jobs", icon: ListTodo },
  { to: "/projects", label: "Projects", icon: FolderKanban },
  { to: "/chat", label: "Chat", icon: MessageSquare },
  { to: "/inbox", label: "Inbox", icon: Inbox, badge: "needsYou" },
  { to: "/fleet", label: "Fleet", icon: Ship },
  { to: "/terminal", label: "Terminal", icon: SquareTerminal },
];
