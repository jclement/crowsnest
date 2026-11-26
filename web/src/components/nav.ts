// The four views, in nav order. Both the mobile bottom nav and the desktop rail read this.
import { Inbox, MessageSquare, Ship, SquareTerminal, type LucideIcon } from "lucide-react";

export interface NavItem {
  to: "/" | "/inbox" | "/fleet" | "/terminal";
  label: string;
  icon: LucideIcon;
}

export const navItems: NavItem[] = [
  { to: "/", label: "Chat", icon: MessageSquare },
  { to: "/inbox", label: "Inbox", icon: Inbox },
  { to: "/fleet", label: "Fleet", icon: Ship },
  { to: "/terminal", label: "Terminal", icon: SquareTerminal },
];
