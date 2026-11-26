// The five views, in nav order (DESIGN.md section 12). Both the mobile bottom nav and the
// desktop rail read this. Jobs is the default route.
import { Inbox, ListTodo, MessageSquare, Ship, SquareTerminal, type LucideIcon } from "lucide-react";

export interface NavItem {
  to: "/" | "/chat" | "/inbox" | "/fleet" | "/terminal";
  label: string;
  icon: LucideIcon;
  /** Shows the needs-you count as a badge. */
  badge?: "needsYou";
}

export const navItems: NavItem[] = [
  { to: "/", label: "Jobs", icon: ListTodo },
  { to: "/chat", label: "Chat", icon: MessageSquare },
  { to: "/inbox", label: "Inbox", icon: Inbox, badge: "needsYou" },
  { to: "/fleet", label: "Fleet", icon: Ship },
  { to: "/terminal", label: "Terminal", icon: SquareTerminal },
];
