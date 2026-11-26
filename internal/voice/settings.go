// Voice settings chosen in the web app (DESIGN.md 13.3): which ElevenLabs voice speaks, how fast,
// and with which model. They are kept in the database (`kv`, one JSON value) and take effect on
// the next text spoken; nothing stored means the configured defaults (--elevenlabs-voice, normal
// speed, the fast model).
//
// Speed is split in two, because ElevenLabs' own `speed` setting covers less than the range the
// sheet offers: the API is asked for as much of it as it allows (APISpeedMin..APISpeedMax, where
// the voice still sounds natural) and the browser makes up the rest with the audio element's
// playbackRate (pitch preserved). So 1.5x is the API's 1.2 played at 1.25.

package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
)

// The speed range the settings sheet offers, as a multiple of the voice's normal pace.
const (
	SpeedMin     = 0.8
	SpeedMax     = 1.5
	SpeedDefault = 1.0
	// APISpeedMin and APISpeedMax are what ElevenLabs' voice_settings.speed accepts. The reference
	// gives no range; the real API (2026-11-26) refused 1.3 with 400 invalid_voice_settings,
	// "expected to be greater or equal to 0.7 and less or equal to 1.2", and accepted 1.2.
	APISpeedMin = 0.7
	APISpeedMax = 1.2
)

// Model choices: a short name the browser sends, and the ElevenLabs model behind it.
const (
	ModelFast    = "fast"
	ModelQuality = "quality"
	// TTSQualityModel is the higher-quality, slower model: the API's own default.
	TTSQualityModel = "eleven_multilingual_v2"
)

// TTSModels maps the settings' model names to ElevenLabs model ids.
var TTSModels = map[string]string{ModelFast: TTSModel, ModelQuality: TTSQualityModel}

// settingsKey is the `kv` key the settings are stored under.
const settingsKey = "voice.settings"

// voiceIDPattern is what a voice id looks like: ElevenLabs' are 20 letters and digits.
var voiceIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

// ValidVoiceID reports whether id can be a voice id (it goes into a URL path).
func ValidVoiceID(id string) bool { return voiceIDPattern.MatchString(id) }

// SettingsStore keeps the settings; *store.Store is one.
type SettingsStore interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
}

// Settings is what is stored. Zero fields mean "the default".
type Settings struct {
	VoiceID string  `json:"voice_id,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
	Model   string  `json:"model,omitempty"`
}

// Effective is the settings in force, with the defaults filled in.
type Effective struct {
	VoiceID string
	// VoiceStored is true when the voice came from the settings rather than the configuration.
	VoiceStored bool
	Speed       float64
	Model       string // ModelFast or ModelQuality
}

// SpeakOptions is how ElevenLabs is asked to say a text under these settings.
func (e Effective) SpeakOptions() SpeakOptions {
	return SpeakOptions{VoiceID: e.VoiceID, ModelID: TTSModels[e.Model], Speed: APISpeed(e.Speed)}
}

// PlaybackRate is what the browser plays the audio at, on top of the API's speed.
func (e Effective) PlaybackRate() float64 { return PlaybackRate(e.Speed) }

// APISpeed is the share of speed ElevenLabs is asked for.
func APISpeed(speed float64) float64 {
	if speed == 0 {
		speed = SpeedDefault
	}
	return clampAPISpeed(speed)
}

// PlaybackRate is the rest of speed, for the browser: speed divided by what the API was asked.
func PlaybackRate(speed float64) float64 {
	if speed == 0 {
		speed = SpeedDefault
	}
	return math.Round(speed/APISpeed(speed)*1000) / 1000
}

func clampAPISpeed(speed float64) float64 {
	return math.Round(math.Min(APISpeedMax, math.Max(APISpeedMin, speed))*100) / 100
}

// ErrBadSettings is a settings change the sheet should not have sent.
var ErrBadSettings = errors.New("voice: invalid settings")

// Validate checks a settings change. An empty voice id means "back to the default".
func (s Settings) Validate() error {
	if s.VoiceID != "" && !ValidVoiceID(s.VoiceID) {
		return fmt.Errorf("%w: a voice id is letters and digits", ErrBadSettings)
	}
	if s.Speed != 0 && (s.Speed < SpeedMin || s.Speed > SpeedMax || math.IsNaN(s.Speed)) {
		return fmt.Errorf("%w: speed must be from %.1f to %.1f", ErrBadSettings, SpeedMin, SpeedMax)
	}
	if s.Model != "" && TTSModels[s.Model] == "" {
		return fmt.Errorf("%w: model must be %q or %q", ErrBadSettings, ModelFast, ModelQuality)
	}
	return nil
}

// Settings returns the settings in force.
func (s *Service) Settings(ctx context.Context) Effective {
	stored := s.storedSettings(ctx)
	effective := Effective{VoiceID: s.client.DefaultVoice(), Speed: SpeedDefault, Model: ModelFast}
	if stored.VoiceID != "" {
		effective.VoiceID, effective.VoiceStored = stored.VoiceID, true
	}
	if stored.Speed != 0 {
		effective.Speed = stored.Speed
	}
	if stored.Model != "" {
		effective.Model = stored.Model
	}
	return effective
}

// SaveSettings stores a validated change. It replaces the whole stored value: the sheet always
// sends every field.
func (s *Service) SaveSettings(ctx context.Context, settings Settings) (Effective, error) {
	if err := settings.Validate(); err != nil {
		return Effective{}, err
	}
	if s.settings == nil {
		return Effective{}, errors.New("voice: no settings store")
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return Effective{}, fmt.Errorf("encoding the voice settings: %w", err)
	}
	if err := s.settings.SetKV(ctx, settingsKey, string(raw)); err != nil {
		return Effective{}, fmt.Errorf("storing the voice settings: %w", err)
	}
	return s.Settings(ctx), nil
}

// storedSettings reads the stored value; anything unreadable counts as nothing stored, so a bad
// row can never stop crowsnest from speaking.
func (s *Service) storedSettings(ctx context.Context) Settings {
	if s.settings == nil {
		return Settings{}
	}
	raw, err := s.settings.GetKV(ctx, settingsKey)
	if err != nil || raw == "" {
		return Settings{}
	}
	var stored Settings
	if json.Unmarshal([]byte(raw), &stored) != nil || stored.Validate() != nil {
		s.logger.Warn("ignoring unreadable voice settings")
		return Settings{}
	}
	return stored
}

// ---- Per-agent voices (DESIGN.md 13.5) ---------------------------------------------------

// agentVoicesKey is the kv key of the per-agent voices: {"lead-alpha": "<voice id>", …}.
const agentVoicesKey = "voice.agents"

// agentPattern is what a standing agent's name looks like: the orchestrator or a project lead.
var agentPattern = regexp.MustCompile(`^(orchestrator|lead-[a-z0-9][a-z0-9-]{0,31})$`)

// ValidAgent reports whether name is a standing agent's name a voice can be given to.
func ValidAgent(name string) bool { return agentPattern.MatchString(name) }

// AgentVoices returns the voice chosen for each standing agent that has one; an agent not in
// the map speaks in the instance's voice. Unreadable or invalid entries are dropped.
func (s *Service) AgentVoices(ctx context.Context) map[string]string {
	out := map[string]string{}
	if s.settings == nil {
		return out
	}
	raw, err := s.settings.GetKV(ctx, agentVoicesKey)
	if err != nil || raw == "" {
		return out
	}
	var stored map[string]string
	if json.Unmarshal([]byte(raw), &stored) != nil {
		return out
	}
	for agent, voice := range stored {
		if ValidAgent(agent) && ValidVoiceID(voice) {
			out[agent] = voice
		}
	}
	return out
}

// SetAgentVoice gives a standing agent its own voice; an empty voiceID puts it back on the
// instance's voice. It returns the map in force.
func (s *Service) SetAgentVoice(ctx context.Context, agent, voiceID string) (map[string]string, error) {
	if !ValidAgent(agent) {
		return nil, fmt.Errorf("%w: %q is not the orchestrator or a project lead", ErrBadSettings, agent)
	}
	if voiceID != "" && !ValidVoiceID(voiceID) {
		return nil, fmt.Errorf("%w: a voice id is letters and digits", ErrBadSettings)
	}
	if s.settings == nil {
		return nil, errors.New("voice: no settings store")
	}
	voices := s.AgentVoices(ctx)
	delete(voices, agent)
	if voiceID != "" {
		voices[agent] = voiceID
	}
	raw, err := json.Marshal(voices)
	if err != nil {
		return nil, fmt.Errorf("encoding the agents' voices: %w", err)
	}
	if err := s.settings.SetKV(ctx, agentVoicesKey, string(raw)); err != nil {
		return nil, fmt.Errorf("storing the agents' voices: %w", err)
	}
	return voices, nil
}
