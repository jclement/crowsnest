// Speak mode in the app frame (DESIGN.md 13.2), shown only when the server has voice on:
//
// - SpeakToggle, in the header on every view: spoken replies on or off for this browser,
//   remembered. Switching on is the user gesture that unlocks audio.
// - NowPlaying, a thin strip under the header on every view: what is being said, with Stop; or
//   Play when the browser refused to start audio without a tap (after a reload with speak mode
//   remembered as on, the first reply needs one tap).
import { Square, Volume2, VolumeX } from "lucide-react";
import { player, usePlayer } from "../../lib/voice/player";
import { useSpeakMode } from "../../lib/voice/speak";
import { cn } from "../../lib/cn";
import { Button } from "../ui/Button";

export function SpeakToggle() {
  const [on, setOn] = useSpeakMode();
  const label = on ? "Speak replies: on" : "Speak replies: off";
  return (
    <button
      type="button"
      aria-pressed={on}
      aria-label={label}
      title={on ? "Replies are spoken aloud. Click to stop." : "Speak replies aloud (the orchestrator's one-line summary)."}
      onClick={() => setOn(!on)}
      data-testid="speak-toggle"
      className={cn(
        "inline-flex h-11 shrink-0 items-center gap-1.5 rounded px-2.5 text-xs font-medium transition-colors md:h-8",
        on ? "bg-accent text-accent-fg hover:brightness-110" : "text-chrome-muted hover:bg-chrome-hover hover:text-chrome-fg",
      )}
    >
      {on ? <Volume2 aria-hidden className="h-4 w-4" /> : <VolumeX aria-hidden className="h-4 w-4" />}
      <span className="hidden sm:inline">{on ? "Speaking" : "Speak"}</span>
    </button>
  );
}

/** The now-playing strip; nothing when nothing is playing. */
export function NowPlaying() {
  const snapshot = usePlayer();
  if (!snapshot.current) return null;
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="player-bar"
      className="flex min-h-9 shrink-0 items-center gap-2 border-b border-border bg-surface-raised px-3 py-1 text-xs"
    >
      <Volume2 aria-hidden className="h-4 w-4 shrink-0 text-accent" />
      <p className="min-w-0 flex-1 truncate text-heading" title={snapshot.current.text}>
        {snapshot.blocked ? "Tap Play to hear the reply." : snapshot.current.text}
        {snapshot.queued > 0 && <span className="text-muted"> (+{snapshot.queued} more)</span>}
      </p>
      {snapshot.blocked ? (
        <Button compact variant="primary" icon={Volume2} onClick={() => player.unlock()}>
          Play
        </Button>
      ) : (
        <Button compact variant="ghost" icon={Square} onClick={() => player.stop()}>
          Stop
        </Button>
      )}
    </div>
  );
}
