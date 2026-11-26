// Wiring for talk mode (M11): the voice service exists only when an ElevenLabs API key is set.
// Without one every /voice route answers 404, `say` is published as a plain event that nothing
// plays, and the orchestrator's replies are not parsed for SAY lines.

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/voice"
)

// startVoice builds the voice service, or returns nil when talk mode is off.
func startVoice(cfg config.Config, settings voice.SettingsStore, events *hub.Hub, logger *slog.Logger) (*voice.Service, error) {
	if !cfg.VoiceEnabled() {
		return nil, nil
	}
	client, err := voice.NewClient(voice.ClientOptions{BaseURL: cfg.ElevenLabsURL, APIKey: cfg.ElevenLabsAPIKey, VoiceID: cfg.ElevenLabsVoice})
	if err != nil {
		return nil, fmt.Errorf("setting up talk mode: %w", err)
	}
	service := voice.NewService(voice.Options{Client: client, Settings: settings, DefaultVoiceSource: cfg.ElevenLabsVoiceSource, Hub: events, Logger: logger})
	chosen := service.Settings(context.Background())
	logger.Info("talk mode on", "elevenlabs_url", cfg.ElevenLabsURL, "voice", chosen.VoiceID, "voice_chosen_in_app", chosen.VoiceStored,
		"speed", chosen.Speed, "tts_model", voice.TTSModels[chosen.Model], "stt_model", voice.STTModel)
	return service, nil
}

// speakerOf hands the agent socket the service as its Speaker, or a nil interface when talk mode
// is off (a nil *voice.Service inside an interface would not compare equal to nil).
func speakerOf(service *voice.Service) agentapi.Speaker {
	if service == nil {
		return nil
	}
	return service
}
