// The fleet and agent-screen endpoints (DESIGN.md section 10): GET /fleet, GET
// /agents/{target}/screen and POST /agents/{target}/keys.
//
// A key press is bound to the screen it was rendered from: the request carries the screen hash
// the UI showed, the server re-reads the screen, and refuses with CONFLICT if it changed. That is
// what makes a dialog button safe to press from a phone a minute after the card was drawn: if the
// agent has moved on, "1" must not land in whatever is on screen now (DESIGN.md 8.2).

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
)

const (
	// herdrCallTimeout bounds each herdr call made while serving a request.
	herdrCallTimeout = 5 * time.Second
	// maxKeysBody caps POST /keys; a handful of key names never needs more.
	maxKeysBody = 4 << 10
	// maxKeys and maxKeyLength bound one key request: enough for a dialog answer or an escape
	// sequence, too little to type a command.
	maxKeys      = 16
	maxKeyLength = 32
)

// targetPattern accepts herdr agent names ([a-z][a-z0-9_-]{0,31}) and pane ids ("w1:p2").
var targetPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_-]{0,63}$`)

// interruptKeys may be sent without a screen hash: Esc and Ctrl+C are how a person stops an
// agent whose screen is changing every second, so binding them to a hash would make them useless.
var interruptKeys = []string{"esc", "escape", "ctrl+c"}

// namedKeys are the key names POST /keys accepts besides a single letter or digit (matched
// without regard to case): what answering a dialog, moving through a list and interrupting
// need, and nothing that edits or ends a shell (ctrl+d, ctrl+z). The endpoint is not a
// privilege boundary today, since any signed-in user can open a full terminal, but an
// allowlist keeps it narrow if it is ever given to a narrower principal (R1 I3).
var namedKeys = []string{"enter", "esc", "escape", "tab", "space", "backspace", "up", "down", "left", "right", "ctrl+c"}

// allowedKey reports whether key is on the allowlist.
func allowedKey(key string) bool {
	if len(key) == 1 {
		c := key[0]
		return '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
	}
	return slices.Contains(namedKeys, strings.ToLower(key))
}

// ---- Fleet ---------------------------------------------------------------------------

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	if s.deps.Fleet == nil {
		writeHerdrUnavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), herdrCallTimeout)
	defer cancel()
	current, err := s.deps.Fleet.Fleet(ctx)
	if err != nil {
		s.deps.Logger.Warn("fleet unavailable", "err", err)
		writeHerdrUnavailable(w)
		return
	}
	writeData(w, http.StatusOK, current)
}

func writeHerdrUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, codeUnavailable, "herdr is not answering; check that the session's server is running.")
}

// ---- Screen --------------------------------------------------------------------------

// ScreenResponse is GET /agents/{target}/screen.
type ScreenResponse struct {
	Target   string            `json:"target"`
	PaneID   string            `json:"pane_id"`
	Name     string            `json:"name"`   // agent name; empty for an unnamed agent or a plain pane
	Kind     string            `json:"kind"`   // claude, codex, …; empty for a plain pane
	Status   herdr.AgentStatus `json:"status"` // herdr's state
	Text     string            `json:"text"`   // visible screen, plain
	ANSI     string            `json:"ansi"`   // visible screen with colours, for a faithful render
	Hash     string            `json:"screen_hash"`
	Revision uint64            `json:"revision"`
	Dialog   fleet.Dialog      `json:"dialog"`
	Draft    fleet.Draft       `json:"draft"`
}

// screenTarget is an agent target resolved to its pane.
type screenTarget struct {
	paneID string
	agent  *herdr.Agent // nil when the target is a plain pane
}

func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	target, ok := s.resolveTarget(w, r)
	if !ok {
		return
	}
	screen, err := s.readScreen(r.Context(), r.PathValue("target"), target)
	if err != nil {
		s.herdrError(w, r, "reading the screen", err)
		return
	}
	writeData(w, http.StatusOK, screen)
}

// resolveTarget validates {target} and finds its pane, writing the error response itself.
func (s *Server) resolveTarget(w http.ResponseWriter, r *http.Request) (screenTarget, bool) {
	if s.deps.Herdr == nil {
		writeHerdrUnavailable(w)
		return screenTarget{}, false
	}
	name := r.PathValue("target")
	if !targetPattern.MatchString(name) {
		writeError(w, http.StatusBadRequest, codeValidation, "The agent target must be an agent name or a pane id.")
		return screenTarget{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), herdrCallTimeout)
	defer cancel()
	agent, err := s.deps.Herdr.AgentGet(ctx, name)
	if err == nil {
		return screenTarget{paneID: agent.PaneID, agent: &agent}, true
	}
	if !herdr.IsNotFound(err) {
		s.herdrError(w, r, "finding the agent", err)
		return screenTarget{}, false
	}
	// Not an agent: a plain pane is still a screen worth showing (and answering keys to).
	if pane, paneErr := s.deps.Herdr.PaneGet(ctx, name); paneErr == nil {
		return screenTarget{paneID: pane.ID}, true
	}
	writeError(w, http.StatusNotFound, codeNotFound, "No agent or pane called "+name+" in this session.")
	return screenTarget{}, false
}

// readScreen reads the visible screen once, with ANSI, and derives everything from that one
// read, so the hash, dialog and draft all describe the same moment.
func (s *Server) readScreen(ctx context.Context, name string, target screenTarget) (ScreenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	read, err := s.deps.Herdr.PaneRead(ctx, target.paneID, herdr.ReadOptions{Source: herdr.SourceVisible, ANSI: true})
	if err != nil {
		return ScreenResponse{}, err
	}
	text := fleet.StripANSI(read.Text)
	screen := ScreenResponse{
		Target: name, PaneID: target.paneID, Text: text, ANSI: read.Text, Hash: fleet.ScreenHash(text),
		Revision: read.Revision, Dialog: fleet.DetectDialog(text), Draft: fleet.DetectDraft(read.Text),
		Status: herdr.StatusUnknown,
	}
	if target.agent != nil {
		screen.Name, screen.Kind, screen.Status = target.agent.Name, target.agent.Agent, target.agent.AgentStatus
	}
	return screen, nil
}

// ---- Keys ----------------------------------------------------------------------------

// KeysRequest is the body of POST /agents/{target}/keys.
type KeysRequest struct {
	Keys       []string `json:"keys"`
	ScreenHash string   `json:"screen_hash"`
}

// KeysResponse confirms what was sent.
type KeysResponse struct {
	Sent       []string `json:"sent"`
	PaneID     string   `json:"pane_id"`
	ScreenHash string   `json:"screen_hash"` // the hash the keys were checked against
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	var body KeysRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxKeysBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"keys":["1"],"screen_hash":"…"}.`)
		return
	}
	if message := validateKeys(body); message != "" {
		writeError(w, http.StatusBadRequest, codeValidation, message)
		return
	}
	target, ok := s.resolveTarget(w, r)
	if !ok {
		return
	}
	name := r.PathValue("target")
	screen, err := s.readScreen(r.Context(), name, target)
	if err != nil {
		s.herdrError(w, r, "reading the screen", err)
		return
	}
	if body.ScreenHash != "" && body.ScreenHash != screen.Hash {
		writeError(w, http.StatusConflict, codeConflict, "The screen changed since it was shown; nothing was sent. Refresh and try again.")
		return
	}
	if err := s.sendKeys(r.Context(), target, body.Keys); err != nil {
		s.herdrError(w, r, "sending keys", err)
		return
	}
	s.deps.Logger.Info("sent keys", "target", name, "pane", target.paneID, "keys", body.Keys)
	if s.deps.Fleet != nil {
		s.deps.Fleet.Invalidate()
	}
	writeData(w, http.StatusOK, KeysResponse{Sent: body.Keys, PaneID: target.paneID, ScreenHash: screen.Hash})
}

// validateKeys returns a message for the user, or "" when the request is acceptable.
func validateKeys(body KeysRequest) string {
	if len(body.Keys) == 0 || len(body.Keys) > maxKeys {
		return "Send between 1 and 16 keys."
	}
	for _, key := range body.Keys {
		if len(key) > maxKeyLength || !allowedKey(key) {
			return "Each key is a letter, a digit, or one of " + strings.Join(namedKeys, ", ") + "."
		}
	}
	if body.ScreenHash == "" && !onlyInterrupts(body.Keys) {
		return "Include the screen_hash of the screen these keys answer; only esc and ctrl+c may be sent without one."
	}
	return ""
}

func onlyInterrupts(keys []string) bool {
	for _, key := range keys {
		if !slices.Contains(interruptKeys, strings.ToLower(key)) {
			return false
		}
	}
	return true
}

func (s *Server) sendKeys(ctx context.Context, target screenTarget, keys []string) error {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	if target.agent != nil {
		err := s.deps.Herdr.AgentSendKeys(ctx, target.paneID, keys)
		// agent.send_keys works only for agents herdr started by name (agent.start). A Claude
		// someone started by hand in a pane is detected and reported (and can sit at a dialog),
		// but herdr answers agent_not_ready for it, so the press goes to the pane instead: the
		// same bytes into the same terminal, already checked against the screen hash. Found by
		// the M9 real-herdr e2e suite, where every press on such a dialog was a 502.
		if errors.Is(err, herdr.ErrAgentNotReady) || errors.Is(err, herdr.ErrAgentNotFound) {
			return s.deps.Herdr.PaneSendKeys(ctx, target.paneID, keys)
		}
		return err
	}
	return s.deps.Herdr.PaneSendKeys(ctx, target.paneID, keys)
}

// herdrError maps a herdr failure to a response: a vanished target is 404, a rejected request
// (an unknown key name) is 400, anything else means herdr is unwell. herdr's own message is
// logged, never sent: it can carry paths and internals, and the response says what to do
// rather than what the code hit (R1 I2).
func (s *Server) herdrError(w http.ResponseWriter, r *http.Request, doing string, err error) {
	var herdrErr *herdr.Error
	switch {
	case herdr.IsNotFound(err):
		writeError(w, http.StatusNotFound, codeNotFound, "That agent or pane is gone.")
	case errors.As(err, &herdrErr) && herdrErr.Code == herdr.CodeInvalidRequest:
		s.deps.Logger.Warn("herdr rejected a request", "doing", doing, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadRequest, codeValidation, "herdr did not accept that request; the server log says why.")
	case errors.As(err, &herdrErr):
		s.deps.Logger.Warn("herdr call failed", "doing", doing, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadGateway, codeUnavailable, "herdr refused to do that; the server log says why. Open the terminal to see the pane.")
	default:
		s.deps.Logger.Warn("herdr call failed", "doing", doing, "path", r.URL.Path, "err", err)
		writeHerdrUnavailable(w)
	}
}
