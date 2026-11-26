// The settings sheet's voice routes (DESIGN.md 13.3) and Chat's play button: which voice, how
// fast and with which model replies are spoken, the account's voices to choose from, a preview of
// a choice before it is saved, and a past reply's SAY line made playable on demand.
//
// Every text the browser can make crowsnest speak is still built here, on the server: a preview
// is a fixed sentence and a reply's SAY line is read from the transcript by message id, so the
// browser never supplies the words.

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/transcript"
	"github.com/jclement/crowsnest/internal/voice"
)

// VoiceModelChoice is one entry of the model picker.
type VoiceModelChoice struct {
	ID      string `json:"id"`       // fast or quality: what PUT takes
	ModelID string `json:"model_id"` // the ElevenLabs model behind it
	Label   string `json:"label"`
}

// voiceModelChoices are the two models offered, fastest first.
var voiceModelChoices = []VoiceModelChoice{
	{ID: voice.ModelFast, ModelID: voice.TTSModels[voice.ModelFast], Label: "Fast (lowest latency)"},
	{ID: voice.ModelQuality, ModelID: voice.TTSModels[voice.ModelQuality], Label: "Higher quality (slower to start)"},
}

// VoiceSettingsResponse is GET and PUT /voice/settings.
type VoiceSettingsResponse struct {
	VoiceID string `json:"voice_id"`
	// VoiceSource says where the voice comes from: "settings" (chosen here), or the configuration
	// that set the default (--elevenlabs-voice, CROWSNEST_ELEVENLABS_VOICE, the config file, or
	// "default" for crowsnest's built-in stock voice).
	VoiceSource    string             `json:"voice_source"`
	DefaultVoiceID string             `json:"default_voice_id"`
	Speed          float64            `json:"speed"`
	SpeedMin       float64            `json:"speed_min"`
	SpeedMax       float64            `json:"speed_max"`
	PlaybackRate   float64            `json:"playback_rate"` // the browser's share of speed
	Model          string             `json:"model"`
	Models         []VoiceModelChoice `json:"models"`
}

// VoiceSettingsRequest is the body of PUT /voice/settings and POST /voice/preview. An empty
// voice id means the configured default; a zero speed means 1.0; an empty model means fast.
type VoiceSettingsRequest struct {
	VoiceID string  `json:"voice_id"`
	Speed   float64 `json:"speed"`
	Model   string  `json:"model"`
}

func (s *Server) voiceSettingsResponse(settings voice.Effective) VoiceSettingsResponse {
	source := "settings"
	if !settings.VoiceStored {
		source = s.deps.Voice.DefaultVoiceSource()
	}
	return VoiceSettingsResponse{
		VoiceID: settings.VoiceID, VoiceSource: source, DefaultVoiceID: s.deps.Voice.DefaultVoice(),
		Speed: settings.Speed, SpeedMin: voice.SpeedMin, SpeedMax: voice.SpeedMax, PlaybackRate: settings.PlaybackRate(),
		Model: settings.Model, Models: voiceModelChoices,
	}
}

func (s *Server) handleVoiceSettings(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, s.voiceSettingsResponse(s.deps.Voice.Settings(r.Context())))
}

func (s *Server) handleSaveVoiceSettings(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeVoiceSettings(w, r)
	if !ok {
		return
	}
	saved, err := s.deps.Voice.SaveSettings(r.Context(), voice.Settings(body))
	if err != nil {
		if errors.Is(err, voice.ErrBadSettings) {
			s.voiceError(w, r, "saving voice settings", err)
			return
		}
		s.internalError(w, r, "saving voice settings", err)
		return
	}
	s.deps.Logger.Info("voice settings changed", "voice", saved.VoiceID, "speed", saved.Speed, "model", saved.Model)
	writeData(w, http.StatusOK, s.voiceSettingsResponse(saved))
}

func decodeVoiceSettings(w http.ResponseWriter, r *http.Request) (VoiceSettingsRequest, bool) {
	var body VoiceSettingsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"voice_id":"","speed":1.1,"model":"fast"}.`)
		return body, false
	}
	return body, true
}

func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	voices, err := s.deps.Voice.Voices(r.Context())
	if err != nil {
		s.voiceError(w, r, "listing voices", err)
		return
	}
	writeData(w, http.StatusOK, voices)
}

// VoicePreviewResponse carries the sample's id and the rate to play it at.
type VoicePreviewResponse struct {
	TextID       string  `json:"text_id"`
	PlaybackRate float64 `json:"playback_rate"`
}

func (s *Server) handleVoicePreview(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeVoiceSettings(w, r)
	if !ok {
		return
	}
	request := voice.Settings(body)
	if err := request.Validate(); err != nil {
		s.voiceError(w, r, "previewing a voice", err)
		return
	}
	how := s.deps.Voice.Settings(r.Context())
	if request.VoiceID != "" {
		how.VoiceID = request.VoiceID
	}
	if request.Speed != 0 {
		how.Speed = request.Speed
	}
	if request.Model != "" {
		how.Model = request.Model
	}
	writeData(w, http.StatusOK, VoicePreviewResponse{TextID: s.deps.Voice.Preview(how), PlaybackRate: how.PlaybackRate()})
}

// ---- A reply's SAY line, on demand -----------------------------------------------------

// ReplySayRequest is the body of POST /voice/reply.
type ReplySayRequest struct {
	Target    string `json:"target"`
	MessageID string `json:"message_id"`
}

// ReplySayResponse is a playable SAY line.
type ReplySayResponse struct {
	TextID string `json:"text_id"`
	Text   string `json:"text"`
}

func (s *Server) handleReplySay(w http.ResponseWriter, r *http.Request) {
	if s.deps.Conversations == nil {
		writeHerdrUnavailable(w)
		return
	}
	var body ReplySayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil || !targetPattern.MatchString(body.Target) || body.MessageID == "" || len(body.MessageID) > 200 {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"target":"orchestrator","message_id":"…"}.`)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), herdrCallTimeout)
	defer cancel()
	page, err := s.deps.Conversations.Messages(ctx, body.Target, "", transcript.MaxPageLimit)
	switch {
	case errors.Is(err, transcript.ErrNoAgent) || herdr.IsNotFound(err):
		writeError(w, http.StatusNotFound, codeNotFound, "No agent called "+body.Target+" in this session.")
		return
	case err != nil:
		s.herdrError(w, r, "reading the conversation", err)
		return
	}
	for _, message := range page.Messages {
		if message.ID != body.MessageID {
			continue
		}
		if message.Kind != transcript.KindAssistant {
			break
		}
		id, sentence, ok := s.deps.Voice.ReplySayFor(message.Text, s.standingName(ctx, body.Target))
		if !ok {
			break
		}
		writeData(w, http.StatusOK, ReplySayResponse{TextID: id, Text: sentence})
		return
	}
	writeError(w, http.StatusNotFound, codeNotFound, "That reply has no SAY line to play, or is too old to find.")
}

// ---- Per-agent voices (DESIGN.md 13.5) ----------------------------------------------------

// AgentVoicesResponse is GET /voice/agents: every standing agent (the orchestrator, then each
// project's lead) with the voice it speaks in ("" for the instance's).
type AgentVoicesResponse struct {
	Agents       []AgentVoice `json:"agents"`
	DefaultVoice string       `json:"default_voice"`
}

// AgentVoice is one standing agent's voice.
type AgentVoice struct {
	Agent   string `json:"agent"`
	Label   string `json:"label"`
	VoiceID string `json:"voice_id"`
}

func (s *Server) handleAgentVoices(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, s.agentVoices(r.Context()))
}

func (s *Server) agentVoices(ctx context.Context) AgentVoicesResponse {
	voices := s.deps.Voice.AgentVoices(ctx)
	out := AgentVoicesResponse{Agents: []AgentVoice{{Agent: orch.Name, Label: "Orchestrator", VoiceID: voices[orch.Name]}}, DefaultVoice: s.deps.Voice.Settings(ctx).VoiceID}
	if s.deps.Projects != nil {
		if list, err := s.deps.Projects.List(ctx, false); err == nil {
			for _, project := range list {
				name := projects.AgentName(project.Slug)
				out.Agents = append(out.Agents, AgentVoice{Agent: name, Label: project.Name, VoiceID: voices[name]})
			}
		}
	}
	return out
}

func (s *Server) handleSetAgentVoice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		VoiceID string `json:"voice_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"voice_id":"…"}; an empty id means the instance's voice.`)
		return
	}
	if _, err := s.deps.Voice.SetAgentVoice(r.Context(), r.PathValue("agent"), body.VoiceID); err != nil {
		if errors.Is(err, voice.ErrBadSettings) {
			writeError(w, http.StatusBadRequest, codeValidation, err.Error())
			return
		}
		s.internalError(w, r, "storing an agent's voice", err)
		return
	}
	writeData(w, http.StatusOK, s.agentVoices(r.Context()))
}

// standingName is the agent name of target (a name or a pane id) when it is a standing agent,
// "" otherwise: a reply played from Chat is then spoken in that agent's own voice.
func (s *Server) standingName(ctx context.Context, target string) string {
	if voice.ValidAgent(target) {
		return target
	}
	if s.deps.Herdr == nil {
		return ""
	}
	agent, err := s.deps.Herdr.AgentGet(ctx, target)
	if err != nil || !voice.ValidAgent(agent.Name) {
		return ""
	}
	return agent.Name
}
