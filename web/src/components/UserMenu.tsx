// The user menu in the header: who is signed in, System (the machine, the doctor's checks and
// versions), Settings (R8: the one door to configuration, routes/SettingsView.tsx, which replaced
// the Settings, Appearance, Account, Notifications, Instructions and Voice entries), the Pause and
// Do not disturb switches, and "Sign out". With --no-auth there is no one to sign out, so the menu
// says so instead.
import { useNavigate } from "@tanstack/react-router";
import { Activity, BellOff, CircleUser, LogOut, PauseCircle, SlidersHorizontal, type LucideIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { Me } from "../lib/api";
import { DndSheet } from "./control/DndSheet";
import { PauseSheet } from "./control/PauseSheet";
import { usePause, usePresence, useSetDND, useSetPause } from "../lib/queries";
import { IconButton } from "./ui/Button";
import { useToast } from "./ui/toast-context";

export function UserMenu({ me, onSignOut }: { me: Me; onSignOut: () => void }) {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const [pauseOpen, setPauseOpen] = useState(false);
  const [dndOpen, setDndOpen] = useState(false);
  const pause = usePause();
  const setPause = useSetPause();
  const presence = usePresence(open);
  const setDND = useSetDND();
  const toast = useToast();
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
          className="absolute right-0 top-full z-40 mt-1 w-56 overflow-hidden rounded-md border border-border bg-surface py-1 text-body shadow-xl"
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
              void navigate({ to: "/system" });
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            <Activity className="h-4 w-4 text-muted" aria-hidden />
            System
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              void navigate({ to: "/settings" });
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            <SlidersHorizontal className="h-4 w-4 text-muted" aria-hidden />
            Settings
          </button>
          {/* Pause and Do not disturb stay here as switches (R8): one tap from every view. Turning
              one on asks its reason or its end time; turning it off is immediate. */}
          <MenuSwitch
            icon={PauseCircle}
            label="Pause automation"
            on={!!pause.data?.paused}
            onToggle={() => {
              setOpen(false);
              if (pause.data?.paused) setPause.mutate({ paused: false }, { onSuccess: () => toast.show({ tone: "success", title: "Automation resumed" }) });
              else setPauseOpen(true);
            }}
          />
          <MenuSwitch
            icon={BellOff}
            label="Do not disturb"
            on={!!presence.data?.dnd_until}
            onToggle={() => {
              setOpen(false);
              if (presence.data?.dnd_until) setDND.mutate("", { onSuccess: () => toast.show({ tone: "success", title: "Do not disturb is off" }) });
              else setDndOpen(true);
            }}
          />
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
      <PauseSheet open={pauseOpen} onClose={() => setPauseOpen(false)} />
      <DndSheet open={dndOpen} onClose={() => setDndOpen(false)} />
    </div>
  );
}

/** A menu row that is a switch: its state shown, a tap flips it. */
function MenuSwitch({ icon: Icon, label, on, onToggle }: { icon: LucideIcon; label: string; on: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      role="menuitemcheckbox"
      aria-checked={on}
      onClick={onToggle}
      className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
    >
      <Icon className="h-4 w-4 text-muted" aria-hidden />
      <span className="flex-1">{label}</span>
      <span aria-hidden className={`relative inline-flex h-4 w-7 shrink-0 rounded-full border border-border-strong ${on ? "bg-accent" : "bg-surface-sunken"}`}>
        <span className={`absolute top-0.5 h-2.5 w-2.5 rounded-full bg-surface transition-[left] ${on ? "left-3.5" : "left-0.5"}`} />
      </span>
    </button>
  );
}
