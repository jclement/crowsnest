// The user menu in the header: who is signed in, Notifications (the Web Push sheet) and "Sign
// out". With --no-auth there is no one to sign out, so the menu says so instead.
import { Bell, CircleUser, LogOut } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { Me } from "../lib/api";
import { NotificationSettings } from "./push/NotificationSettings";
import { IconButton } from "./ui/Button";

export function UserMenu({ me, onSignOut }: { me: Me; onSignOut: () => void }) {
  const [open, setOpen] = useState(false);
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => event.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={rootRef} className="relative">
      <IconButton icon={CircleUser} label="Account" tone="chrome" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)} />
      {open && (
        <div
          role="menu"
          className="absolute right-0 top-full z-30 mt-1 w-56 overflow-hidden rounded-md border border-border bg-surface py-1 text-body shadow-xl"
        >
          <div className="border-b border-border px-3 pb-2 pt-1.5">
            <p className="text-2xs uppercase tracking-wide text-muted">{me.features.auth ? "Signed in as" : "Login"}</p>
            <p className="truncate text-sm font-medium text-heading">
              {me.features.auth ? me.username : "Off (--no-auth)"}
            </p>
            <p className="mt-0.5 truncate font-mono text-2xs text-muted">session {me.features.session}</p>
          </div>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              setNotificationsOpen(true);
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            <Bell className="h-4 w-4 text-muted" aria-hidden />
            Notifications
          </button>
          {me.features.auth && (
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false);
                onSignOut();
              }}
              className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
            >
              <LogOut className="h-4 w-4 text-muted" aria-hidden />
              Sign out
            </button>
          )}
        </div>
      )}
      <NotificationSettings open={notificationsOpen} onClose={() => setNotificationsOpen(false)} />
    </div>
  );
}
