// The command palette's owner: the global shortcuts (lib/hotkeys.ts: ⌘K / Ctrl K, ?, g j…), the
// open state, focus coming back to where it was on close, the recents list, and the sheets the
// palette can open (shortcuts, journal, schedule, spend, pause…; the configuration ones, such as
// settings, appearance, notifications, voice, instructions and limits, land on their section of the
// Settings view, R8). AppFrame mounts it once around the views. With voice on (an ElevenLabs key)
// the palette also offers "Speak replies: on/off", wired to lib/voice/speak.ts, and "Voice
// settings".
//
// The key listener sits on the window in the capture phase, so a terminal (xterm stops the
// events it handles) or a view's own handler never sees the chord first.
import { FOCUS_TRAY_EVENT } from "../../lib/needsYou";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { useNavigate } from "@tanstack/react-router";
import { chordLabel, focusContext, hotkeyFor, initialSequence, isMacPlatform, type SequenceState } from "../../lib/hotkeys";
import { cycleThemeName } from "../../lib/theme";
import { findTheme } from "../../themes";
import { useToast } from "../ui/toast-context";
import { loadRecents, pushRecent, saveRecents, type PaletteSheet } from "../../lib/palette";
import { sectionForSheet } from "../../lib/settingsSections";
import { JournalSheet } from "../journal/JournalPanel";
import { ScheduleSheet } from "../schedule/SchedulePanel";
import { SpendSheet } from "../spend/SpendSheet";
import { useMe } from "../../lib/queries";
import { useSpeakMode } from "../../lib/voice/speak";
import { CommandPalette } from "./CommandPalette";
import { PaletteContext, type PaletteApi } from "./palette-context";
import { ShortcutsSheet } from "./ShortcutsSheet";
import { HelpSheet } from "../help/HelpSheet";
import { DigestsSheet } from "../control/DigestsSheet";
import { PauseSheet } from "../control/PauseSheet";
import { SweepSheet } from "../control/SweepSheet";
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
  const toast = useToast();
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
  // The contextual help sheet: a manual section over the current view (components/help).
  const [helpTopic, setHelpTopic] = useState<string | null>(null);
  // It stacks over whatever sheet is open (Schedule, Settings…), so reading help loses nothing.
  const openHelp = useCallback((topic: string) => setHelpTopic(topic), []);
  // A sheet that was a door to configuration lands on its section of the Settings view instead (R8:
  // one door); the palette's own sheets (Schedule, Spend, Journal…) still open here.
  const openSheet = useCallback(
    (next: PaletteSheet) => {
      setHelpTopic(null);
      const section = sectionForSheet(next);
      if (section) {
        setSheet(null);
        void navigate({ to: "/settings", hash: section });
        return;
      }
      setSheet(next);
    },
    [navigate],
  );

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
      else if (action.kind === "cycleTheme") {
        const id = cycleThemeName();
        toast.show({ title: `Theme: ${findTheme(id)?.name ?? id}`, key: "theme" });
      }
      else if (action.kind === "tray") window.dispatchEvent(new Event(FOCUS_TRAY_EVENT));
      else void navigate({ to: action.to, search: {} } as unknown as Parameters<typeof navigate>[0]);
    };
    window.addEventListener("keydown", onKey, { capture: true });
    return () => window.removeEventListener("keydown", onKey, { capture: true });
  }, [mac, navigate, openPalette, closePalette, toast]);

  // The page behind does not scroll while the palette is up (it is full screen on a phone).
  useEffect(() => {
    if (!open) return;
    const { overflow } = document.body.style;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = overflow;
    };
  }, [open]);

  const run = useRunCommand({ openSheet, signOut: onSignOut, setSpeak });
  const onRan = useCallback((itemId: string) => {
    setRecents((current) => {
      const next = pushRecent(current, itemId);
      saveRecents(next);
      return next;
    });
  }, []);

  const api = useMemo<PaletteApi>(
    () => ({ openPalette, openShortcuts, openSheet, openHelp, chord: chordLabel(mac), mac }),
    [openPalette, openShortcuts, openSheet, openHelp, mac],
  );

  return (
    <PaletteContext.Provider value={api}>
      {children}
      {open && (
        <CommandPalette inputRef={inputRef} onClose={closePalette} run={run} recents={recents} onRan={onRan} mac={mac} speak={speak} voiceSettings={voice} />
      )}
      <ShortcutsSheet open={sheet === "shortcuts"} onClose={() => setSheet(null)} mac={mac} />
      <HelpSheet topic={helpTopic} onClose={() => setHelpTopic(null)} />
      <JournalSheet open={sheet === "journal"} onClose={() => setSheet(null)} />
      <ScheduleSheet open={sheet === "schedule"} onClose={() => setSheet(null)} />
      <SpendSheet open={sheet === "spend"} onClose={() => setSheet(null)} />
      <PauseSheet open={sheet === "pause"} onClose={() => setSheet(null)} />
      <DigestsSheet open={sheet === "digests"} onClose={() => setSheet(null)} />
      <SweepSheet
        open={sheet === "worktrees"}
        onClose={() => setSheet(null)}
        onOpenJob={(id) => {
          setSheet(null);
          void navigate({ to: "/", search: { job: id } } as unknown as Parameters<typeof navigate>[0]);
        }}
      />
    </PaletteContext.Provider>
  );
}
