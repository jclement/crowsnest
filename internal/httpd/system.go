// The System page, Settings and the guided first run (DESIGN.md 10, 12.x):
//
//	GET  /api/v1/system[?recheck=1]   the doctor report as the running server sees it, plus its live view
//	GET  /api/v1/system/diagnostics   the same as redacted text for an issue (no secrets, ~ for home)
//	POST /api/v1/system/demo-state    --demo only: switch the scripted machine (Playwright)
//	GET  /api/v1/settings             every setting the app may change, its value and source
//	PUT  /api/v1/settings/{name}      {value} or {clear:true}; refused when a flag or env var set it
//	                                  (tailscale-serve: also refused to a session signed in with Tailscale)
//	GET  /api/v1/onboarding[?recheck=1] the first-run checklist
//	POST /api/v1/onboarding/start     {engine?}: finish the checklist, start the orchestrator
//	POST /api/v1/onboarding/shell     {command}: a plain shell pane in the orchestrator's folder
//	                                  with a fix command typed into it (claude to sign in)
//
// Everything here needs the session, like every other route.

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/system"
)

// maxSettingBody caps a settings or onboarding request.
const maxSettingBody = 16 << 10

// System is what the routes need of internal/system.
type System interface {
	Report(ctx context.Context, recheck bool) doctor.Report
	Settings(ctx context.Context) (system.SettingsPage, error)
	UpdateSetting(ctx context.Context, name, value string, clear bool) (system.SettingView, error)
	Onboarding(ctx context.Context, recheck bool) system.OnboardingView
	StartOnboarding(ctx context.Context, engine string) error
	SetEnv(env doctor.Env)
	OnboardingPending(ctx context.Context) bool
	// Config is the configuration in force now, settings saved in the app included.
	Config() config.Config
}

// ShellOpener opens a plain shell pane and types command into it, returning the pane.
type ShellOpener func(ctx context.Context, command string) (string, error)

func (s *Server) registerSystemRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/system", s.requireAuth(http.HandlerFunc(s.handleSystem)))
	mux.Handle("GET /api/v1/system/diagnostics", s.requireAuth(http.HandlerFunc(s.handleDiagnostics)))
	mux.Handle("POST /api/v1/system/demo-state", s.requireAuth(http.HandlerFunc(s.handleDemoState)))
	mux.Handle("GET /api/v1/settings", s.requireAuth(http.HandlerFunc(s.handleSettings)))
	mux.Handle("PUT /api/v1/settings/{name}", s.requireAuth(http.HandlerFunc(s.handleUpdateSetting)))
	mux.Handle("GET /api/v1/onboarding", s.requireAuth(http.HandlerFunc(s.handleOnboarding)))
	mux.Handle("POST /api/v1/onboarding/start", s.requireAuth(http.HandlerFunc(s.handleStartOnboarding)))
	mux.Handle("POST /api/v1/onboarding/shell", s.requireAuth(http.HandlerFunc(s.handleOpenShell)))
}

func (s *Server) system(w http.ResponseWriter) System {
	if s.deps.System == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "This server does not report its system state.")
	}
	return s.deps.System
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	service := s.system(w)
	if service == nil {
		return
	}
	writeData(w, http.StatusOK, service.Report(r.Context(), r.URL.Query().Get("recheck") == "1"))
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	service := s.system(w)
	if service == nil {
		return
	}
	home, _ := os.UserHomeDir()
	text := doctor.Diagnostics(service.Report(r.Context(), r.URL.Query().Get("recheck") == "1"), home)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(text)) //nolint:gosec // text/plain with nosniff (securityHeaders): never rendered as HTML
}

type demoStateRequest struct {
	State string `json:"state"`
}

func (s *Server) handleDemoState(w http.ResponseWriter, r *http.Request) {
	service := s.system(w)
	if service == nil {
		return
	}
	if !s.deps.Config.Demo {
		writeError(w, http.StatusNotFound, codeNotFound, "Only a --demo server has a scripted machine.")
		return
	}
	var body demoStateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingBody)).Decode(&body); err != nil || !doctor.ValidDemoState(body.State) {
		writeError(w, http.StatusBadRequest, codeValidation, "Send {\"state\": …} with one of the demo states.")
		return
	}
	service.SetEnv(doctor.DemoEnv(body.State))
	writeData(w, http.StatusOK, map[string]string{"state": body.State})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	service := s.system(w)
	if service == nil {
		return
	}
	page, err := service.Settings(r.Context())
	if err != nil {
		s.internalError(w, r, "listing settings", err)
		return
	}
	writeData(w, http.StatusOK, page)
}

// passwordSettingEditable answers the request itself when a session signed in with Tailscale asks
// to change a setting that acts outward or grants (R9-S13: the same list a restore sets aside,
// config.RestoreSetsAside): the guard's protected hosts and branches, the permission mode, the
// review gates, Remote Control, publishing. A tailnet identity is a convenience for reading and
// answering; widening what agents may do, or who reaches crowsnest, needs the password.
func (s *Server) passwordSettingEditable(w http.ResponseWriter, r *http.Request, name string) bool {
	session, _ := r.Context().Value(sessionContextKey{}).(auth.Session)
	if _, sensitive := config.RestoreSetsAside(name); sensitive && session.TailscaleLogin != "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only a browser signed in with the password can change this setting: it widens what agents may do or who reaches crowsnest.")
		return false
	}
	return true
}

type updateSettingRequest struct {
	Value string `json:"value"`
	Clear bool   `json:"clear"`
}

func (s *Server) handleUpdateSetting(w http.ResponseWriter, r *http.Request) {
	service := s.system(w)
	if service == nil {
		return
	}
	var body updateSettingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with value, or clear: true.")
		return
	}
	if r.PathValue("name") == tailscaleServeSetting && !s.tailscaleServeEditable(w, r) {
		return // the card's rule holds in Settings too (tsserve.go)
	}
	if !s.passwordSettingEditable(w, r, r.PathValue("name")) {
		return
	}
	view, err := service.UpdateSetting(r.Context(), r.PathValue("name"), body.Value, body.Clear)
	switch {
	case errors.Is(err, system.ErrNotEditable):
		writeError(w, http.StatusConflict, codeConflict, capitalise(trimSentinel(err, system.ErrNotEditable))+".")
	case errors.Is(err, system.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeValidation, capitalise(trimSentinel(err, system.ErrInvalid)))
	case err != nil:
		s.internalError(w, r, "saving a setting", err)
	default:
		writeData(w, http.StatusOK, view)
	}
}

// trimSentinel drops "<sentinel>: " from the front of err's text.
func trimSentinel(err, sentinel error) string {
	text := err.Error()
	prefix := sentinel.Error() + ": "
	if len(text) > len(prefix) && text[:len(prefix)] == prefix {
		return text[len(prefix):]
	}
	return text
}

func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	service := s.system(w)
	if service == nil {
		return
	}
	writeData(w, http.StatusOK, service.Onboarding(r.Context(), r.URL.Query().Get("recheck") == "1"))
}

type startOnboardingRequest struct {
	Engine string `json:"engine"`
}

func (s *Server) handleStartOnboarding(w http.ResponseWriter, r *http.Request) {
	service := s.system(w)
	if service == nil {
		return
	}
	var body startOnboardingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON, optionally with engine.")
		return
	}
	err := service.StartOnboarding(r.Context(), body.Engine)
	switch {
	case errors.Is(err, system.ErrNoAgent):
		writeError(w, http.StatusConflict, codeConflict, "No agent is installed yet: install Claude Code or Codex, then re-check.")
	case errors.Is(err, system.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeValidation, capitalise(trimSentinel(err, system.ErrInvalid)))
	case err != nil:
		s.internalError(w, r, "finishing the first run", err)
	default:
		writeData(w, http.StatusOK, service.Onboarding(r.Context(), false))
	}
}

type openShellRequest struct {
	Command string `json:"command"`
}

// handleOpenShell opens a shell pane with one of the report's fix commands typed in. Only a
// command the current report offers is accepted: this is a convenience for "sign in", not a way
// to run arbitrary text (the Terminal view is that, and it is right there).
func (s *Server) handleOpenShell(w http.ResponseWriter, r *http.Request) {
	service := s.system(w)
	if service == nil {
		return
	}
	if s.deps.OpenShell == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "A shell needs herdr; install it first.")
		return
	}
	var body openShellRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with command.")
		return
	}
	if body.Command != "" && !offered(service.Report(r.Context(), false), body.Command) {
		writeError(w, http.StatusBadRequest, codeValidation, "That command is not one the checklist offers.")
		return
	}
	paneID, err := s.deps.OpenShell(r.Context(), body.Command)
	if err != nil {
		s.deps.Logger.Warn("opening a setup shell", "err", err)
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Could not open a shell: is herdr running? Re-check, then try again.")
		return
	}
	writeData(w, http.StatusOK, map[string]string{"pane_id": paneID})
}

func offered(report doctor.Report, command string) bool {
	for _, check := range report.Checks {
		if check.Command == command {
			return true
		}
	}
	return false
}

// ---- Machine stats -----------------------------------------------------------

// AgentUsage is one agent's process tree in GET /system/agents.
type AgentUsage struct {
	PaneID    string  `json:"pane_id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	Workspace string  `json:"workspace"`
	CPU       float64 `json:"cpu"`
	RSS       uint64  `json:"rss"`
	Processes int     `json:"processes"`
}

func (s *Server) registerStatsRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/system/stats", s.requireAuth(http.HandlerFunc(s.handleStats)))
	mux.Handle("GET /api/v1/system/agents", s.requireAuth(http.HandlerFunc(s.handleAgentUsage)))
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if s.deps.Stats == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "System stats are off on this server.")
		return
	}
	writeData(w, http.StatusOK, s.deps.Stats.Stats(r.Context()))
}

// handleAgentUsage attributes CPU and memory to each agent: herdr names the foreground processes
// of its pane, and the agent's cost is that process tree's. A pane herdr cannot describe is
// skipped rather than failing the list.
func (s *Server) handleAgentUsage(w http.ResponseWriter, r *http.Request) {
	if s.deps.Fleet == nil || s.deps.Herdr == nil || s.deps.Processes == nil {
		writeData(w, http.StatusOK, []AgentUsage{})
		return
	}
	fleetView, err := s.deps.Fleet.Fleet(r.Context())
	if err != nil {
		writeData(w, http.StatusOK, []AgentUsage{})
		return
	}
	out := []AgentUsage{}
	for _, agent := range fleetView.Agents {
		info, err := s.deps.Herdr.PaneProcessInfo(r.Context(), agent.PaneID)
		if err != nil {
			continue
		}
		var pids []int
		for _, proc := range info.ForegroundProcesses {
			pids = append(pids, proc.PID)
		}
		usage := s.deps.Processes.Tree(r.Context(), pids)
		out = append(out, AgentUsage{PaneID: agent.PaneID, Name: agent.Name, Kind: agent.Kind, Status: string(agent.Status), Workspace: agent.Workspace,
			CPU: usage.CPU, RSS: usage.RSS, Processes: usage.Processes})
	}
	s.deps.Processes.Prune(r.Context())
	writeData(w, http.StatusOK, out)
}

// OpenStreams is how many browsers have the event stream open: the stats sampler runs only
// while it is above zero.
func (s *Server) OpenStreams() int { return int(s.openStreams.Load()) }
