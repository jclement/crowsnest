// Speak mode in the app frame (DESIGN.md 13.2), shown only when the server has voice on:
//
// - SpeakToggle, in the header on every view: the master mute for this browser, remembered.
//   Switching on is the user gesture that unlocks audio.
// - AgentSpeakToggle, in an agent's Chat header (and the Voice sheet): that agent's own "speak
//   replies" switch, off by default, per browser. Lines are spoken only when both are on.
// - NowPlaying, a thin strip under the header on every view: who is speaking and what, with Skip
//   (the next queued line) and Stop (everything); or Play when the browser refused to start audio
//   without a tap (after a reload with speak mode remembered as on, the first reply needs one tap).
import { SkipForward, Square, Volume2, VolumeX } from "lucide-react";
import { useMemo } from "react";
import { useAgentVoices } from "../../lib/queries";
import { player, usePlayer } from "../../lib/voice/player";
import { agentLabel } from "../../lib/voice/queue";
import { useAgentSpeak, useSpeakMode } from "../../lib/voice/speak";
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
      title={
        on
          ? "Spoken replies are on for the agents you chose (each agent's Speak switch in Chat or the Voice sheet). Click to mute them all."
          : "Unmute spoken replies (each agent's one-line summary, for the agents you chose)."
      }
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

/** One agent's own "speak replies" switch, for its Chat header. */
export function AgentSpeakToggle({ agent, name }: { agent: string; name: string }) {
  const [on, setOn] = useAgentSpeak(agent);
  const [master] = useSpeakMode();
  const label = `Speak ${name}'s replies`;
  return (
    <button
      type="button"
      aria-pressed={on}
      aria-label={label}
      title={on ? (master ? `${name}'s replies are spoken. Click to stop.` : `${name} is chosen, but spoken replies are muted in the header.`) : `Speak ${name}'s replies aloud, in its own voice.`}
      onClick={() => setOn(!on)}
      data-testid="agent-speak-toggle"
      className={cn(
        "inline-flex h-11 shrink-0 items-center gap-1.5 rounded px-2.5 text-xs font-medium md:h-8",
        on ? (master ? "bg-accent/10 text-accent" : "bg-surface-sunken text-muted line-through") : "text-muted hover:bg-hover hover:text-heading",
      )}
    >
      {on ? <Volume2 aria-hidden className="h-4 w-4" /> : <VolumeX aria-hidden className="h-4 w-4" />}
      <span>Speak</span>
    </button>
  );
}

/** The now-playing strip; nothing when nothing is playing. */
export function NowPlaying() {
  const snapshot = usePlayer();
  const voices = useAgentVoices(true);
  const labels = useMemo(() => new Map((voices.data?.agents ?? []).map((entry) => [entry.agent, entry.label])), [voices.data]);
  if (!snapshot.current) return null;
  const who = snapshot.current.agent ? agentLabel(snapshot.current.agent, labels) : null;
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="player-bar"
      className="flex min-h-9 shrink-0 items-center gap-2 border-b border-border bg-surface-raised px-3 py-1 text-xs"
    >
      <Volume2 aria-hidden className="h-4 w-4 shrink-0 text-accent" />
      <p className="min-w-0 flex-1 truncate text-heading" title={snapshot.current.text}>
        {who && <span className="font-semibold" data-testid="speaker">{who}: </span>}
        {snapshot.blocked ? "Tap Play to hear the reply." : snapshot.current.text}
        {snapshot.queued > 0 && <span className="text-muted"> (+{snapshot.queued} more)</span>}
      </p>
      {snapshot.blocked ? (
        <Button compact variant="primary" icon={Volume2} onClick={() => player.unlock()}>
          Play
        </Button>
      ) : (
        <>
          <Button compact variant="ghost" icon={SkipForward} onClick={() => player.skip()}>
            Skip
          </Button>
          <Button compact variant="ghost" icon={Square} onClick={() => player.stop()}>
            Stop
          </Button>
        </>
      )}
    </div>
  );
}
