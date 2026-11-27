// Day-thirty routes (DESIGN.md 4.17 to 4.21, 10): backups, storage and clean-up, the update
// notice, the summary another machine reads, scoped tokens, the machine list, and the Tailscale
// allowlist. The work is in internal/backup, internal/retention, internal/machines and
// internal/tsauth; these handlers decode, check and answer.
//
//	GET    /api/v1/backup                     a fresh backup as a download (Settings, Download backup)
//	GET    /api/v1/updated                    {from, to, release_url} after an update, else null
//	POST   /api/v1/updated/seen               the owner has seen it
//	GET    /api/v1/restore/hold               the pause a restore left ({at, reason}), else null
//	POST   /api/v1/restore/resume             lift it
//	GET    /api/v1/storage                    the Storage card
//	POST   /api/v1/storage/cleanup            {dry_run, confirm:[j_…]} → the report
//	GET    /api/v1/summary                    this instance's headline figures; a session or a read token
//	GET    /api/v1/tokens, POST {name}        scoped tokens; POST answers the token once
//	DELETE /api/v1/tokens/{id}                revoke
//	GET    /api/v1/machines, POST {name,url,token}, DELETE /api/v1/machines/{id}
//	GET    /api/v1/auth/tailscale             the option, the serve check, this request's login, the list
//	PUT    /api/v1/auth/tailscale             {logins:[…]}: a password session only
//	POST   /api/v1/auth/tailscale/allow-me    add this request's own tailnet login: a password session only

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/machines"
	"github.com/jclement/crowsnest/internal/retention"
	"github.com/jclement/crowsnest/internal/tsauth"
)

// maxOpsBody bounds the small JSON bodies here.
const maxOpsBody = 16 << 10

// Ops is what the day-thirty routes need from the server's wiring (cmd/crowsnest/ops.go).
type Ops interface {
	// BackupName is the download's file name; WriteBackup streams a fresh backup.
	BackupName() string
	WriteBackup(ctx context.Context, w io.Writer) error
	// Updated is the "what changed" notice after an update, nil when there is none.
	Updated(ctx context.Context) *UpdateNotice
	DismissUpdated(ctx context.Context) error
	Storage(ctx context.Context) (retention.Storage, error)
	Cleanup(ctx context.Context, dryRun bool, confirm []string) (retention.Report, error)
	Summary(ctx context.Context) (Summary, error)
	// ArchivedJobs is the set of archived job ids; SearchJobs finds job ids by text.
	ArchivedJobs(ctx context.Context) (map[int64]bool, error)
	SearchJobs(ctx context.Context, query string, archivedOnly bool) ([]int64, error)
	// RestoreHold is the pause a restore left, nil when none; ResumeAfterRestore lifts it.
	RestoreHold(ctx context.Context) *RestoreHold
	ResumeAfterRestore(ctx context.Context) error
}

// RestoreHold is GET /restore/hold: the instance was restored and waits for the human.
type RestoreHold struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
}

// UpdateNotice says crowsnest was updated since the owner last looked.
type UpdateNotice struct {
	From       string `json:"from"`
	To         string `json:"to"`
	At         string `json:"at"`
	ReleaseURL string `json:"release_url"`
}

// Summary is GET /summary: what another machine's Machines list shows for this one.
type Summary struct {
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Attention    int            `json:"attention"` // open questions + pending approvals + jobs that need you
	Questions    int            `json:"questions"`
	Blocking     int            `json:"blocking"`
	Approvals    int            `json:"approvals"`
	NeedsYou     int            `json:"needs_you"`
	Running      int            `json:"running"`
	Jobs         map[string]int `json:"jobs"` // live jobs by state
	Orchestrator string         `json:"orchestrator"`
	Paused       bool           `json:"paused"`
	Stats        *SummaryStats  `json:"stats,omitempty"`
	At           string         `json:"at"`
}

// SummaryStats are the header's machine figures.
type SummaryStats struct {
	CPU      float64 `json:"cpu"`
	MemUsed  uint64  `json:"mem_used"`
	MemTotal uint64  `json:"mem_total"`
	Load1    float64 `json:"load1"`
}

func (s *Server) registerOpsRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/backup", s.requireAuth(http.HandlerFunc(s.handleBackup)))
	mux.Handle("GET /api/v1/updated", s.requireAuth(http.HandlerFunc(s.handleUpdated)))
	mux.Handle("POST /api/v1/updated/seen", s.requireAuth(http.HandlerFunc(s.handleUpdatedSeen)))
	mux.Handle("GET /api/v1/restore/hold", s.requireAuth(http.HandlerFunc(s.handleRestoreHold)))
	mux.Handle("POST /api/v1/restore/resume", s.requireAuth(http.HandlerFunc(s.handleRestoreResume)))
	mux.Handle("GET /api/v1/storage", s.requireAuth(http.HandlerFunc(s.handleStorage)))
	mux.Handle("POST /api/v1/storage/cleanup", s.requireAuth(http.HandlerFunc(s.handleCleanup)))
	mux.Handle("GET /api/v1/summary", s.requireAuthOrToken(machines.ScopeRead, http.HandlerFunc(s.handleSummary)))
	mux.Handle("GET /api/v1/tokens", s.requireAuth(http.HandlerFunc(s.handleTokens)))
	mux.Handle("POST /api/v1/tokens", s.requireAuth(http.HandlerFunc(s.handleTokenMint)))
	mux.Handle("DELETE /api/v1/tokens/{id}", s.requireAuth(http.HandlerFunc(s.handleTokenRevoke)))
	mux.Handle("GET /api/v1/machines", s.requireAuth(http.HandlerFunc(s.handleMachines)))
	mux.Handle("POST /api/v1/machines", s.requireAuth(http.HandlerFunc(s.handleMachineAdd)))
	mux.Handle("DELETE /api/v1/machines/{id}", s.requireAuth(http.HandlerFunc(s.handleMachineRemove)))
	mux.Handle("GET /api/v1/auth/tailscale", s.requireAuth(http.HandlerFunc(s.handleTailscale)))
	mux.Handle("PUT /api/v1/auth/tailscale", s.requireAuth(http.HandlerFunc(s.handleTailscaleSet)))
	mux.Handle("POST /api/v1/auth/tailscale/allow-me", s.requireAuth(http.HandlerFunc(s.handleTailscaleAllowMe)))
}

func (s *Server) ops(w http.ResponseWriter) Ops {
	if s.deps.Ops == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "This is not available on this server.")
	}
	return s.deps.Ops
}

// ---- Backups, updates, storage --------------------------------------------------

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	ops := s.ops(w)
	if ops == nil {
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+ops.BackupName()+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := ops.WriteBackup(r.Context(), w); err != nil {
		// The headers are gone; a cut-off body is what the browser sees, and the log says why.
		s.deps.Logger.Error("writing a backup download", "err", err)
	}
}

func (s *Server) handleUpdated(w http.ResponseWriter, r *http.Request) {
	if ops := s.ops(w); ops != nil {
		writeData(w, http.StatusOK, ops.Updated(r.Context()))
	}
}

func (s *Server) handleUpdatedSeen(w http.ResponseWriter, r *http.Request) {
	ops := s.ops(w)
	if ops == nil {
		return
	}
	if err := ops.DismissUpdated(r.Context()); err != nil {
		s.internalError(w, r, "dismissing the update notice", err)
		return
	}
	writeData(w, http.StatusOK, nil)
}

func (s *Server) handleRestoreHold(w http.ResponseWriter, r *http.Request) {
	if ops := s.ops(w); ops != nil {
		writeData(w, http.StatusOK, ops.RestoreHold(r.Context()))
	}
}

func (s *Server) handleRestoreResume(w http.ResponseWriter, r *http.Request) {
	ops := s.ops(w)
	if ops == nil {
		return
	}
	if err := ops.ResumeAfterRestore(r.Context()); err != nil {
		s.internalError(w, r, "resuming after a restore", err)
		return
	}
	writeData(w, http.StatusOK, nil)
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	ops := s.ops(w)
	if ops == nil {
		return
	}
	storage, err := ops.Storage(r.Context())
	if err != nil {
		s.internalError(w, r, "measuring storage", err)
		return
	}
	writeData(w, http.StatusOK, storage)
}

// cleanupRequest is POST /storage/cleanup. DryRun defaults to true: a body that forgets it
// previews rather than deletes.
type cleanupRequest struct {
	DryRun  *bool    `json:"dry_run"`
	Confirm []string `json:"confirm"`
}

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	ops := s.ops(w)
	if ops == nil {
		return
	}
	var body cleanupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOpsBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"dry_run": true} or {"dry_run": false, "confirm": ["j_12"]}.`)
		return
	}
	dryRun := body.DryRun == nil || *body.DryRun
	for _, id := range body.Confirm {
		if !strings.HasPrefix(id, "j_") || len(id) > 24 {
			writeError(w, http.StatusBadRequest, codeValidation, "confirm lists job ids such as j_12.")
			return
		}
	}
	report, err := ops.Cleanup(r.Context(), dryRun, body.Confirm)
	if err != nil {
		s.internalError(w, r, "cleaning up", err)
		return
	}
	writeData(w, http.StatusOK, report)
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ops := s.ops(w)
	if ops == nil {
		return
	}
	summary, err := ops.Summary(r.Context())
	if err != nil {
		s.internalError(w, r, "building the summary", err)
		return
	}
	writeData(w, http.StatusOK, summary)
}

// requireAuthOrToken lets a request through with a session, or with a bearer token of scope
// (machines.Service.Check). A token opens only the routes wrapped in this, never anything else.
func (s *Server) requireAuthOrToken(scope string, next http.Handler) http.Handler {
	withSession := s.requireAuth(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || s.deps.Machines == nil {
			withSession.ServeHTTP(w, r)
			return
		}
		if !s.deps.Machines.Check(r.Context(), strings.TrimSpace(bearer), scope) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "That token is not valid here (revoked, or minted on another machine).")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- Tokens and machines ----------------------------------------------------------

func (s *Server) machineService(w http.ResponseWriter) *machines.Service {
	if s.deps.Machines == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Machines are not available on this server.")
	}
	return s.deps.Machines
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	service := s.machineService(w)
	if service == nil {
		return
	}
	tokens, err := service.Tokens(r.Context())
	if err != nil {
		s.internalError(w, r, "listing tokens", err)
		return
	}
	writeList(w, nonNil(tokens), ListMeta{Total: len(tokens)})
}

// mintResponse carries the token once.
type mintResponse struct {
	Token  string         `json:"token"`
	Record machines.Token `json:"record"`
}

func (s *Server) handleTokenMint(w http.ResponseWriter, r *http.Request) {
	service := s.machineService(w)
	if service == nil {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOpsBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"name": "bedev"}.`)
		return
	}
	secret, token, err := service.Mint(r.Context(), body.Name)
	if err != nil {
		s.writeMachineError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, mintResponse{Token: secret, Record: token})
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	service := s.machineService(w)
	if service == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "A token id is a number.")
		return
	}
	ok, err := service.Revoke(r.Context(), id)
	if err != nil {
		s.internalError(w, r, "revoking a token", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No live token has that id.")
		return
	}
	writeData(w, http.StatusOK, nil)
}

func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	service := s.machineService(w)
	if service == nil {
		return
	}
	list, err := service.List(r.Context())
	if err != nil {
		s.internalError(w, r, "listing machines", err)
		return
	}
	writeList(w, nonNil(list), ListMeta{Total: len(list)})
}

func (s *Server) handleMachineAdd(w http.ResponseWriter, r *http.Request) {
	service := s.machineService(w)
	if service == nil {
		return
	}
	var input machines.AddInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOpsBody)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"name": "bedev", "url": "https://bedev.tail1234.ts.net", "token": "cnk_…"}.`)
		return
	}
	machine, err := service.Add(r.Context(), input)
	if err != nil {
		s.writeMachineError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, machine)
}

func (s *Server) handleMachineRemove(w http.ResponseWriter, r *http.Request) {
	service := s.machineService(w)
	if service == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "A machine id is a number.")
		return
	}
	ok, err := service.Remove(r.Context(), id)
	if err != nil {
		s.internalError(w, r, "removing a machine", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No machine has that id.")
		return
	}
	writeData(w, http.StatusOK, nil)
}

func (s *Server) writeMachineError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, machines.ErrValidation) {
		writeError(w, http.StatusBadRequest, codeValidation, strings.TrimPrefix(err.Error(), machines.ErrValidation.Error()+": "))
		return
	}
	s.internalError(w, r, "machines", err)
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// ---- Sign in with Tailscale -------------------------------------------------------

// tailscaleLoginHeader is the header tailscale serve fills with the tailnet user's login.
const tailscaleLoginHeader = "Tailscale-User-Login"

// trustedTailscaleLogin is the tailnet login r carries, believed only when it can only have come
// from this machine's tailscale serve (tsauth's package comment): the option on, a loopback TCP
// peer, and a Host that a serve config on this machine proxies to crowsnest's port. "" otherwise.
func (s *Server) trustedTailscaleLogin(r *http.Request) string {
	gate := s.deps.Tailscale
	if !gate.Enabled() || !peerAddr(r).IsLoopback() {
		return ""
	}
	login := tsauth.NormalizeLogin(r.Header.Get(tailscaleLoginHeader))
	if login == "" || !gate.ServesHost(r.Context(), r.Host) {
		return ""
	}
	return login
}

// signInWithTailscale issues a session for an allowed tailnet login and returns it, or false.
func (s *Server) signInWithTailscale(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	login := s.trustedTailscaleLogin(r)
	if login == "" || !s.deps.Tailscale.Allowed(r.Context(), login) {
		return auth.Session{}, false
	}
	token, session, err := s.deps.Auth.NewSession(r.Context(), auth.TailscaleAgentPrefix+login+" "+r.UserAgent(), sessionHost(r))
	if err != nil {
		s.deps.Logger.Warn("signing in with Tailscale", "err", err)
		return auth.Session{}, false
	}
	setSessionCookie(w, r, token, session.ExpiresAt)
	session.TailscaleLogin = login
	s.deps.Logger.Info("signed in with Tailscale", "login", login)
	return session, true
}

// tailscaleSessionValid reports whether a session signed in with Tailscale may still be used for
// r: it must still arrive through serve for the same login, and the login must still be allowed
// (so removing someone from the list, or turning the option off, ends their sessions at once).
func (s *Server) tailscaleSessionValid(r *http.Request, session auth.Session) bool {
	if session.TailscaleLogin == "" {
		return true
	}
	login := s.trustedTailscaleLogin(r)
	return login == session.TailscaleLogin && s.deps.Tailscale.Allowed(r.Context(), login)
}

// TailscaleResponse is GET /auth/tailscale.
type TailscaleResponse struct {
	Enabled         bool     `json:"enabled"`          // --tailscale-auth
	ServeConfigured bool     `json:"serve_configured"` // a serve config proxies to this port
	CurrentLogin    string   `json:"current_login"`    // this request's tailnet login, when believed
	ViaTailscale    bool     `json:"via_tailscale"`    // this session was signed in with Tailscale
	Allowed         []string `json:"allowed"`
}

func (s *Server) tailscaleState(r *http.Request) (TailscaleResponse, error) {
	gate := s.deps.Tailscale
	session, _ := r.Context().Value(sessionContextKey{}).(auth.Session)
	state := TailscaleResponse{Enabled: gate.Enabled(), CurrentLogin: s.trustedTailscaleLogin(r), ViaTailscale: session.TailscaleLogin != "", Allowed: []string{}}
	if !gate.Enabled() {
		return state, nil
	}
	state.ServeConfigured = gate.ServeConfigured(r.Context())
	list, err := gate.List(r.Context())
	if err != nil {
		return state, err
	}
	state.Allowed = list
	return state, nil
}

func (s *Server) handleTailscale(w http.ResponseWriter, r *http.Request) {
	state, err := s.tailscaleState(r)
	if err != nil {
		s.internalError(w, r, "reading the Tailscale allowlist", err)
		return
	}
	writeData(w, http.StatusOK, state)
}

// tailscaleEditable answers the request itself when the allowlist may not be changed from it:
// the option is off, or this session was itself signed in with Tailscale (the list is the
// password owner's to keep; a tailnet login must not widen it).
func (s *Server) tailscaleEditable(w http.ResponseWriter, r *http.Request) bool {
	if !s.deps.Tailscale.Enabled() {
		writeError(w, http.StatusConflict, codeConflict, "Sign in with Tailscale is off. Start crowsnest with --tailscale-auth (CROWSNEST_TAILSCALE_AUTH=true) to use it.")
		return false
	}
	if session, _ := r.Context().Value(sessionContextKey{}).(auth.Session); session.TailscaleLogin != "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only a browser signed in with the password can change who may sign in with Tailscale.")
		return false
	}
	return true
}

func (s *Server) handleTailscaleSet(w http.ResponseWriter, r *http.Request) {
	if !s.tailscaleEditable(w, r) {
		return
	}
	var body struct {
		Logins []string `json:"logins"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOpsBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"logins": ["you@github"]}.`)
		return
	}
	if _, err := s.deps.Tailscale.SetList(r.Context(), body.Logins); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	s.handleTailscale(w, r)
}

func (s *Server) handleTailscaleAllowMe(w http.ResponseWriter, r *http.Request) {
	if !s.tailscaleEditable(w, r) {
		return
	}
	login := s.trustedTailscaleLogin(r)
	if login == "" {
		writeError(w, http.StatusConflict, codeConflict, "This browser did not come through tailscale serve on this machine, so there is no tailnet login to add. Open crowsnest at its https://…ts.net address and try again.")
		return
	}
	list, err := s.deps.Tailscale.List(r.Context())
	if err != nil {
		s.internalError(w, r, "reading the Tailscale allowlist", err)
		return
	}
	if _, err := s.deps.Tailscale.SetList(r.Context(), append(list, login)); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	s.handleTailscale(w, r)
}
