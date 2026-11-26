// The command palette's owner: the global shortcuts (lib/hotkeys.ts: ⌘K / Ctrl K, ?, g j…), the
// open state, focus coming back to where it was on close, the recents list, and the sheets the
// palette can open (shortcuts, journal, notifications, voice). AppFrame mounts it once around the
// views. With voice on (an ElevenLabs key) the palette also offers "Speak replies: on/off", wired
// to lib/voice/speak.ts, and "Voice settings".
//
// The key listener sits on the window in the capture phase, so a terminal (xterm stops the
// events it handles) or a view's own handler never sees the chord first.
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { useNavigate } from "@tanstack/react-router";
import { chordLabel, focusContext, hotkeyFor, initialSequence, isMacPlatform, type SequenceState } from "../../lib/hotkeys";
import { loadRecents, pushRecent, saveRecents, type PaletteSheet } from "../../lib/palette";
import { JournalSheet } from "../journal/JournalPanel";
import { ScheduleSheet } from "../schedule/SchedulePanel";
import { SpendSheet } from "../spend/SpendSheet";
import { NotificationSettings } from "../push/NotificationSettings";
import { VoiceSettingsSheet } from "../voice/VoiceSettings";
import { useMe } from "../../lib/queries";
import { useSpeakMode } from "../../lib/voice/speak";
import { CommandPalette } from "./CommandPalette";
import { PaletteContext, type PaletteApi } from "./palette-context";
import { ShortcutsSheet } from "./ShortcutsSheet";
import { useRunCommand } from "./useRunCommand";

export function PaletteProvider({ onSignOut, children }: { onSignOut: () => void; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [sheet, setSheet] = useState<PaletteSheet | null>(null);
  const [recents, setRecents] = useState<string[]>(loadRecents);
  const mac = useMemo(() => isMacPlatform(), []);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const opener = useRef<HTMLElement | null>(null);
  const sequence = useRef<SequenceState>(initialSequence);
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  }, [open]);
  const navigate = useNavigate();
  const voice = useMe().data?.features.voice ?? false;
  const [speakOn, setSpeak] = useSpeakMode();
  const speak = useMemo(() => (voice ? { on: speakOn } : null), [voice, speakOn]);

  const openPalette = useCallback(() => {
    if (openRef.current) {
      inputRef.current?.focus();
      return;
    }
    const active = document.activeElement;
    opener.current = active instanceof HTMLElement && active !== document.body ? active : null;
    // Render now and focus inside the same user gesture, so a phone raises its keyboard.
    openRef.current = true;
    flushSync(() => setOpen(true));
    inputRef.current?.focus();
  }, []);

  const closePalette = useCallback(() => {
    openRef.current = false;
    setOpen(false);
    const back = opener.current;
    opener.current = null;
    // After the palette has left the DOM, and before anything it ran moves focus elsewhere.
    requestAnimationFrame(() => {
      if (back?.isConnected && document.activeElement === document.body) back.focus({ preventScroll: true });
    });
  }, []);

  const openShortcuts = useCallback(() => setSheet("shortcuts"), []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const { action, state } = hotkeyFor(event, focusContext(event.target, mac), sequence.current, performance.now());
      sequence.current = state;
      if (!action) return;
      if (action.kind === "palette") {
        event.preventDefault();
        event.stopPropagation();
        if (openRef.current) closePalette();
        else openPalette();
        return;
      }
      if (openRef.current) return;
      event.preventDefault();
      if (action.kind === "help") setSheet("shortcuts");
      else void navigate({ to: action.to, search: {} } as unknown as Parameters<typeof navigate>[0]);
    };
    window.addEventListener("keydown", onKey, { capture: true });
    return () => window.removeEventListener("keydown", onKey, { capture: true });
  }, [mac, navigate, openPalette, closePalette]);

  // The page behind does not scroll while the palette is up (it is full screen on a phone).
  useEffect(() => {
    if (!open) return;
    const { overflow } = document.body.style;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = overflow;
    };
  }, [open]);

  const run = useRunCommand({ openSheet: setSheet, signOut: onSignOut, setSpeak });
  const onRan = useCallback((itemId: string) => {
    setRecents((current) => {
      const next = pushRecent(current, itemId);
      saveRecents(next);
      return next;
    });
  }, []);

  const api = useMemo<PaletteApi>(() => ({ openPalette, openShortcuts, chord: chordLabel(mac), mac }), [openPalette, openShortcuts, mac]);

  return (
    <PaletteContext.Provider value={api}>
      {children}
      {open && (
        <CommandPalette inputRef={inputRef} onClose={closePalette} run={run} recents={recents} onRan={onRan} mac={mac} speak={speak} voiceSettings={voice} />
      )}
      <ShortcutsSheet open={sheet === "shortcuts"} onClose={() => setSheet(null)} mac={mac} />
      <JournalSheet open={sheet === "journal"} onClose={() => setSheet(null)} />
      <ScheduleSheet open={sheet === "schedule"} onClose={() => setSheet(null)} />
      <SpendSheet open={sheet === "spend"} onClose={() => setSheet(null)} />
      <NotificationSettings open={sheet === "notifications"} onClose={() => setSheet(null)} />
      {voice && <VoiceSettingsSheet open={sheet === "voice"} onClose={() => setSheet(null)} />}
    </PaletteContext.Provider>
  );
}
