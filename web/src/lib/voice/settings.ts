// The voice settings (DESIGN.md 13.3): queries for the settings sheet, and the hook that keeps the
// player's playback rate in step with the saved speed. ElevenLabs speaks at most 1.2x; a faster
// choice is the server's 1.2 played faster here, which is also instant (no new synthesis).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { endpoints, type VoiceSettingsChange } from "../api";
import { player } from "./player";

export const voiceKeys = {
  settings: ["voice", "settings"] as const,
  voices: ["voice", "voices"] as const,
};

/** The settings in force. Only call when the server has voice on (every /voice route 404s else). */
export function useVoiceSettings(enabled = true) {
  return useQuery({ queryKey: voiceKeys.settings, queryFn: endpoints.voiceSettings, enabled, staleTime: 60_000 });
}

/** The account's voices; the server caches them for five minutes, so this does too. */
export function useVoices(enabled: boolean) {
  return useQuery({ queryKey: voiceKeys.voices, queryFn: endpoints.voices, enabled, staleTime: 5 * 60_000, retry: false });
}

export function useSaveVoiceSettings() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (change: VoiceSettingsChange) => endpoints.saveVoiceSettings(change),
    onSuccess: (saved) => {
      client.setQueryData(voiceKeys.settings, saved);
      player.rate = saved.playback_rate;
    },
  });
}

/** Keeps the player's rate on the saved speed. Mount once, where voice is on (the app frame). */
export function useSyncPlaybackRate(enabled: boolean) {
  const settings = useVoiceSettings(enabled);
  const rate = settings.data?.playback_rate;
  useEffect(() => {
    if (rate) player.rate = rate;
  }, [rate]);
}
