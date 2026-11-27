// Settings → Voice (DESIGN.md 13.3), a section of the Settings view when voice is on: which
// ElevenLabs voice speaks replies (the account's voices, listed by the server), how fast (0.8x to
// 1.5x), and with which model (fast, or higher quality but slower to start). Preview plays a
// sample sentence with the choices before they are saved; Save stores them in crowsnest's
// database, and they apply from the next reply. The sheet says where the voice comes from when
// nothing is stored: the --elevenlabs-voice flag, its environment variable, the config file, or
// crowsnest's stock default.
//
// Agents (per-agent voices): each standing agent (the orchestrator and each project's lead) is
// listed with its own "speak replies" switch (per browser, off by default; the header toggle is
// the master mute) and its own ElevenLabs voice (PUT /voice/agents/{agent}, saved at once; the
// default is the instance voice above), so the human tells them apart by voice. Job agents'
// `say` lines share one switch and speak in the instance voice.
import { Loader2, Play, RotateCcw, Save } from "lucide-react";
import { useState } from "react";
import { endpoints, type VoiceInfo, type VoiceSettings as Settings } from "../../lib/api";
import { player } from "../../lib/voice/player";
import { useSaveVoiceSettings, useVoiceSettings, useVoices } from "../../lib/voice/settings";
import { useAgentVoices, useSetAgentVoice } from "../../lib/queries";
import { JOB_AGENTS } from "../../lib/voice/queue";
import { useAgentSpeak } from "../../lib/voice/speak";
import { Switch } from "../ui/Switch";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { SkeletonRows } from "../ui/Skeleton";
import { useToast } from "../ui/toast-context";

/** The voice settings: the instance voice, and each standing agent's. */
export function VoiceSettingsBody({ onSaved = () => {} }: { onSaved?: () => void }) {
  const settings = useVoiceSettings();
  if (settings.isPending) return <SkeletonRows rows={4} label="Loading the voice settings" />;
  if (settings.isError) {
    return (
      <Banner tone="danger" role="alert" className="m-4 rounded">
        Could not load the voice settings: {settings.error.message}
      </Banner>
    );
  }
  return (
    <>
      <VoiceForm saved={settings.data} onSaved={onSaved} />
      <AgentVoicesSection />
    </>
  );
}

/** Each standing agent: its speak switch and its own voice. */
function AgentVoicesSection() {
  const agents = useAgentVoices(true);
  const voices = useVoices(true);
  const set = useSetAgentVoice();
  const toast = useToast();
  const choose = (agent: string, voiceId: string) =>
    set.mutate(
      { agent, voiceId },
      {
        onSuccess: () => toast.show({ tone: "success", title: "Voice saved", description: "Its next reply uses it." }),
        onError: (error) => toast.show({ tone: "danger", title: "Not saved", description: error.message }),
      },
    );
  return (
    <section aria-label="Agents" className="flex flex-col gap-2 border-t border-border p-4 text-sm">
      <h3 className="font-medium text-heading">Agents</h3>
      <p className="text-2xs text-muted">
        Speak is per agent and per browser; the header's speaker is the mute for all of them. Give each agent its own voice to tell them apart.
      </p>
      {agents.isPending ? (
        <SkeletonRows rows={2} label="Loading the agents" />
      ) : agents.isError ? (
        <Banner tone="warning" className="rounded">
          Could not list the agents: {agents.error.message}
        </Banner>
      ) : (
        <ul className="flex flex-col divide-y divide-border rounded border border-border">
          {agents.data.agents.map((entry) => (
            <li key={entry.agent} className="flex flex-wrap items-center gap-2 px-2 py-1">
              <AgentSwitch agent={entry.agent} label={entry.label} />
              <select
                aria-label={`${entry.label} voice`}
                value={entry.voice_id}
                onChange={(event) => choose(entry.agent, event.target.value)}
                disabled={set.isPending || voices.isPending}
                className="ml-auto h-11 min-w-0 max-w-full rounded border border-border-strong bg-surface px-2 text-xs text-heading md:h-7"
              >
                <option value="">Instance voice ({nameOf(voices.data, agents.data.default_voice)})</option>
                {(voices.data ?? []).map((voice) => (
                  <option key={voice.voice_id} value={voice.voice_id}>
                    {voiceLabel(voice)}
                  </option>
                ))}
              </select>
            </li>
          ))}
          <li className="flex flex-wrap items-center gap-2 px-2 py-1">
            <AgentSwitch agent="" label="Job agents (say)" />
            <span className="ml-auto text-2xs text-muted">Instance voice</span>
          </li>
        </ul>
      )}
    </section>
  );
}

function AgentSwitch({ agent, label }: { agent: string; label: string }) {
  const [on, setOn] = useAgentSpeak(agent || JOB_AGENTS);
  return <Switch checked={on} onChange={setOn} label={`Speak ${label}'s replies`} text={label} />;
}

/** Where the default voice comes from, in words. */
function sourceText(saved: Settings): string {
  switch (saved.voice_source) {
    case "settings":
      return "Chosen here and stored in crowsnest's database.";
    case "default":
      return "Crowsnest's stock voice: nothing is chosen here, and no --elevenlabs-voice is set.";
    default:
      return `The default from ${saved.voice_source}; choosing one here overrides it.`;
  }
}

function VoiceForm({ saved, onSaved }: { saved: Settings; onSaved: () => void }) {
  const voices = useVoices(true);
  const save = useSaveVoiceSettings();
  const toast = useToast();
  // "" means "the configured default".
  const [voiceId, setVoiceId] = useState(saved.voice_source === "settings" ? saved.voice_id : "");
  const [speed, setSpeed] = useState(saved.speed);
  const [model, setModel] = useState(saved.model);
  const [previewing, setPreviewing] = useState(false);
  const change = { voice_id: voiceId, speed, model };

  const preview = () => {
    player.unlock(); // inside the click, before any await
    setPreviewing(true);
    endpoints
      .previewVoice(change)
      .then(
        (result) => player.playNow({ textId: result.text_id, text: "Voice preview", rate: result.playback_rate }),
        (error: unknown) => toast.show({ tone: "danger", title: "Could not preview", description: error instanceof Error ? error.message : undefined }),
      )
      .finally(() => setPreviewing(false));
  };
  const submit = () =>
    save.mutate(change, {
      onSuccess: () => {
        toast.show({ tone: "success", title: "Voice saved", description: "Replies use it from now on." });
        onSaved();
      },
      onError: (error) => toast.show({ tone: "danger", title: "Not saved", description: error.message }),
    });

  return (
    <div className="flex flex-col gap-4 p-4 text-sm">
      <label className="flex flex-col gap-1">
        <span className="font-medium text-heading">Voice</span>
        {voices.isError ? (
          <Banner tone="warning" role="status" className="rounded">
            Could not list the account's voices ({voices.error.message}). The current voice is {saved.voice_id}.
          </Banner>
        ) : (
          <select
            value={voiceId}
            onChange={(event) => setVoiceId(event.target.value)}
            disabled={voices.isPending}
            aria-label="Voice"
            className="min-h-11 rounded border border-border-strong bg-surface px-2 text-sm text-heading md:min-h-9"
          >
            <option value="">Default ({nameOf(voices.data, saved.default_voice_id)})</option>
            {(voices.data ?? []).map((voice) => (
              <option key={voice.voice_id} value={voice.voice_id}>
                {voiceLabel(voice)}
              </option>
            ))}
          </select>
        )}
        <span className="text-2xs text-muted" data-testid="voice-source">
          {sourceText(saved)}
        </span>
      </label>

      <label className="flex flex-col gap-1">
        <span className="flex items-baseline justify-between font-medium text-heading">
          Speed <span className="font-mono text-xs tabular-nums text-body">{speed.toFixed(2)}×</span>
        </span>
        <input
          type="range"
          min={saved.speed_min}
          max={saved.speed_max}
          step={0.05}
          value={speed}
          onChange={(event) => setSpeed(Number(event.target.value))}
          aria-label="Speed"
          className="h-11 accent-[var(--color-accent)] md:h-6"
        />
        <span className="text-2xs text-muted">
          ElevenLabs speaks up to 1.2×; faster than that is played faster here, which keeps the pitch.
        </span>
      </label>

      <fieldset className="flex flex-col gap-1">
        <legend className="mb-1 font-medium text-heading">Model</legend>
        {saved.models.map((option) => (
          <label key={option.id} className="flex min-h-11 items-center gap-2 md:min-h-7">
            <input type="radio" name="voice-model" value={option.id} checked={model === option.id} onChange={() => setModel(option.id)} />
            <span>{option.label}</span>
            <span className="font-mono text-2xs text-muted">{option.model_id}</span>
          </label>
        ))}
      </fieldset>

      <div className="flex flex-wrap justify-end gap-2">
        <Button
          variant="ghost"
          icon={RotateCcw}
          onClick={() => {
            setVoiceId("");
            setSpeed(1);
            setModel("fast");
          }}
        >
          Defaults
        </Button>
        <Button icon={previewing ? Loader2 : Play} onClick={preview} disabled={previewing}>
          Preview
        </Button>
        <Button variant="primary" icon={Save} onClick={submit} disabled={save.isPending}>
          Save
        </Button>
      </div>
    </div>
  );
}

function voiceLabel(voice: VoiceInfo): string {
  const labels = voice.labels ?? {};
  const detail = [labels.accent, labels.gender, labels.age?.replace(/_/g, " ")].filter(Boolean).join(", ");
  return detail ? `${voice.name} (${detail})` : voice.name;
}

function nameOf(voices: VoiceInfo[] | undefined, id: string): string {
  return voices?.find((voice) => voice.voice_id === id)?.name ?? id;
}
