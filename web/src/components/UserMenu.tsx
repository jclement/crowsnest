// The user menu in the header: who is signed in, System (the doctor's checks and versions),
// Settings (what can change while crowsnest runs), Account (password and instance name),
// Notifications (the Web Push sheet), Instructions (the layers crowsnest adds to every agent it
// starts, DESIGN.md 21.3), Voice (the
// voice, speed and model of spoken replies, when voice is on) and "Sign out". With --no-auth there is no one to sign out, so the menu says so instead.
import { useNavigate } from "@tanstack/react-router";
import { Activity, AudioLines, Bell, BellOff, BookText, CircleUser, KeyRound, LogOut, Palette, PauseCircle, PlayCircle, SlidersHorizontal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { Me } from "../lib/api";
import { AccountSheet } from "./account/AccountSheet";
import { DndSheet } from "./control/DndSheet";
import { PauseSheet } from "./control/PauseSheet";
import { usePause, useSetPause } from "../lib/queries";
import { SettingsSheet } from "./system/SettingsSheet";
import { AppearanceSheet } from "./appearance/AppearanceSheet";
import { InstructionsSheet } from "./instructions/InstructionsSheet";
import { NotificationSettings } from "./push/NotificationSettings";
import { VoiceSettingsSheet } from "./voice/VoiceSettings";
import { IconButton } from "./ui/Button";

export function UserMenu({ me, onSignOut }: { me: Me; onSignOut: () => void }) {
  const [open, setOpen] = useState(false);
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const [voiceOpen, setVoiceOpen] = useState(false);
  const [accountOpen, setAccountOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [appearanceOpen, setAppearanceOpen] = useState(false);
  const navigate = useNavigate();
  const [instructionsOpen, setInstructionsOpen] = useState(false);
  const [pauseOpen, setPauseOpen] = useState(false);
  const [dndOpen, setDndOpen] = useState(false);
  const pause = usePause();
  const setPause = useSetPause();
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
              setSettingsOpen(true);
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            <SlidersHorizontal className="h-4 w-4 text-muted" aria-hidden />
            Settings
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              setAppearanceOpen(true);
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            <Palette className="h-4 w-4 text-muted" aria-hidden />
            Appearance
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              setAccountOpen(true);
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            <KeyRound className="h-4 w-4 text-muted" aria-hidden />
            Account
          </button>
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
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              setInstructionsOpen(true);
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            <BookText className="h-4 w-4 text-muted" aria-hidden />
            Instructions
          </button>
          {me.features.voice && (
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false);
                setVoiceOpen(true);
              }}
              className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
            >
              <AudioLines className="h-4 w-4 text-muted" aria-hidden />
              Voice
            </button>
          )}
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              if (pause.data?.paused) setPause.mutate({ paused: false });
              else setPauseOpen(true);
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            {pause.data?.paused ? <PlayCircle className="h-4 w-4 text-muted" aria-hidden /> : <PauseCircle className="h-4 w-4 text-muted" aria-hidden />}
            {pause.data?.paused ? "Resume automation" : "Pause automation…"}
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              setDndOpen(true);
            }}
            className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9"
          >
            <BellOff className="h-4 w-4 text-muted" aria-hidden />
            Do not disturb…
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
      <AccountSheet open={accountOpen} onClose={() => setAccountOpen(false)} />
      <SettingsSheet
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        onOpen={(sheet) => {
          setSettingsOpen(false);
          if (sheet === "account") setAccountOpen(true);
          if (sheet === "notifications") setNotificationsOpen(true);
          if (sheet === "voice") setVoiceOpen(true);
          if (sheet === "appearance") setAppearanceOpen(true);
        }}
      />
      <AppearanceSheet open={appearanceOpen} onClose={() => setAppearanceOpen(false)} />
      <NotificationSettings open={notificationsOpen} onClose={() => setNotificationsOpen(false)} />
      <InstructionsSheet open={instructionsOpen} onClose={() => setInstructionsOpen(false)} />
      <PauseSheet open={pauseOpen} onClose={() => setPauseOpen(false)} />
      <DndSheet open={dndOpen} onClose={() => setDndOpen(false)} />
      {me.features.voice && <VoiceSettingsSheet open={voiceOpen} onClose={() => setVoiceOpen(false)} />}
    </div>
  );
}
