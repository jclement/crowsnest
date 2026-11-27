// Package approvals is structured permission approval (DESIGN.md 8.7). Every Claude agent
// crowsnest starts has a PermissionRequest hook, `crowsnest hook permission-request`. When Claude
// is about to ask permission for a tool call, the hook posts the call here (tool, input,
// directory, session) and long-polls until a human decides; it then prints the hook's decision
// and Claude goes on with no terminal dialog to answer.
//
// What was verified on Claude Code 2.1.332 (DESIGN 8.7 has the detail): in an interactive session
// the terminal dialog is drawn at the same time the hook starts, and whichever answers first wins.
// A hook's allow or deny closes the dialog; a terminal answer wins over a hook still waiting, and
// Claude does not kill that hook. So an approval is superseded (and the hook released, printing
// nothing) whenever the agent has evidently stopped waiting: the dialog left its screen, the tool
// ran, its turn or session ended, or the hook stopped polling (killed at its timeout). Expiry and
// supersession never decide anything: the hook prints nothing, which leaves Claude's own dialog,
// the behaviour without crowsnest.
//
// Nothing here can allow "always": the hook's decision carries no permission updates. Widening an
// agent's permanent permissions stays a terminal action.
package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/risk"
	"github.com/jclement/crowsnest/internal/termtext"
	"github.com/jclement/crowsnest/internal/store"
)

// Status is an approval's lifecycle state.
type Status string

// The statuses.
const (
	StatusPending    Status = "pending"
	StatusAllowed    Status = "allowed"
	StatusDenied     Status = "denied"
	StatusExpired    Status = "expired"
	StatusSuperseded Status = "superseded"
)

// How a decision arrived.
const (
	ViaBrowser      = "browser"
	ViaNotification = "notification"
)

const (
	// DefaultTTL is how long an approval waits for a human. The hook's own client gives up a
	// minute after it; Claude's hook timeout (agentcfg) is longer still, so the server's expiry is
	// what ends a long wait, and the terminal dialog is still there for the human.
	DefaultTTL = 12 * time.Hour
	// DefaultPollGrace: a hooked approval nobody has polled for this long has lost its hook
	// (Claude killed it at its timeout, or the agent went away): it is superseded.
	DefaultPollGrace = 45 * time.Second
	// screenGrace is how old an approval must be before a dialog missing from its pane's screen
	// counts (the dialog is drawn a moment after the hook starts).
	screenGrace = 15 * time.Second
	// screenMisses is how many looks in a row must find no dialog before the approval is
	// superseded as answered in the terminal.
	screenMisses = 2
	// maxNote caps the human's note (it becomes the deny message).
	maxNote = 2000
	// maxInput caps the stored tool input.
	maxInput = 256 << 10
)

// Errors.
var (
	ErrNotFound   = errors.New("no such approval")
	ErrNotPending = errors.New("the approval is no longer pending")
	// ErrConfirm: a dangerous approval allowed without the explicit second confirmation.
	ErrConfirm = errors.New("this is a dangerous action: allowing it needs an explicit confirmation")
	// ErrVia: an elevated or dangerous approval answered from a notification.
	ErrVia = errors.New("only low-risk approvals can be answered from a notification")
)

// Approval is one request, as the API shows it.
type Approval struct {
	ID           string          `json:"id"`
	JobID        string          `json:"job_id,omitempty"`
	PaneID       string          `json:"pane_id,omitempty"`
	AgentName    string          `json:"agent_name,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	Cwd          string          `json:"cwd,omitempty"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	Suggestions  json.RawMessage `json:"suggestions"`
	Risk         risk.Level      `json:"risk"`
	RiskReasons  []string        `json:"risk_reasons"`
	Status       Status          `json:"status"`
	DecidedVia   string          `json:"decided_via,omitempty"`
	Note         string          `json:"note,omitempty"`
	StatusReason string          `json:"status_reason,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	SettledAt    *time.Time      `json:"settled_at,omitempty"`
	ExpiresAt    time.Time       `json:"expires_at"`
	// Summary is the call in one line ("Bash: go test ./...").
	Summary string `json:"summary"`

	hookedFlag bool // a hook process waits on it (false for the demo's)
}

// Event is the hub's "approval" event.
type Event struct {
	ID     string     `json:"id"`
	Status Status     `json:"status"`
	Risk   risk.Level `json:"risk"`
	Tool   string     `json:"tool"`
	JobID  string     `json:"job_id,omitempty"`
	PaneID string     `json:"pane_id,omitempty"`
	Change string     `json:"change"` // requested, allowed, denied, expired, superseded
}

// Options configure a Service. Store is required.
type Options struct {
	Store      *store.Store
	Hub        *hub.Hub
	Logger     *slog.Logger
	Now        func() time.Time
	Classifier *risk.Classifier
	// RiskContext gives the classification what it knows about the job (its directory) and the
	// protected hosts. Nil: no job directory, no hosts.
	RiskContext func(ctx context.Context, jobID string) risk.Context
	// OnChange runs after every change (requested, allowed, denied, expired, superseded), outside
	// any lock: the job's state and pushes hang off it.
	OnChange  func(ctx context.Context, approval Approval, change string)
	TTL       time.Duration
	PollGrace time.Duration
}

// Service owns the approvals.
type Service struct {
	opts Options

	mu       sync.Mutex
	settled  map[int64]chan struct{} // closed when the approval leaves pending
	lastPoll map[int64]time.Time
	waiting  map[int64]int // long-polls in progress
	misses   map[int64]int // screen looks in a row with no dialog
}

// New builds a Service.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Classifier == nil {
		opts.Classifier = risk.Default()
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	if opts.PollGrace <= 0 {
		opts.PollGrace = DefaultPollGrace
	}
	return &Service{opts: opts, settled: map[int64]chan struct{}{}, lastPoll: map[int64]time.Time{}, waiting: map[int64]int{}, misses: map[int64]int{}}
}

// FormatID renders an approval id.
func FormatID(id int64) string { return "ap_" + strconv.FormatInt(id, 10) }

// ParseID parses ap_N.
func ParseID(id string) (int64, error) {
	number, ok := strings.CutPrefix(id, "ap_")
	if !ok {
		return 0, ErrNotFound
	}
	value, err := strconv.ParseInt(number, 10, 64)
	if err != nil || value <= 0 {
		return 0, ErrNotFound
	}
	return value, nil
}

// RequestInput is a new approval.
type RequestInput struct {
	JobID       string
	PaneID      string
	AgentName   string
	SessionID   string
	Cwd         string
	ToolName    string
	ToolInput   json.RawMessage
	Suggestions json.RawMessage
	// Hooked is true when a hook process waits on it (false only for the demo's seeded ones).
	Hooked bool
}

// Request stores a new pending approval, classified, and announces it.
func (s *Service) Request(ctx context.Context, in RequestInput) (Approval, error) {
	if strings.TrimSpace(in.ToolName) == "" || len(in.ToolName) > 200 {
		return Approval{}, fmt.Errorf("a tool name is required (at most 200 characters)")
	}
	input := compactJSON(in.ToolInput, "{}")
	if len(input) > maxInput {
		return Approval{}, fmt.Errorf("the tool input is too large to show (%d bytes)", len(input))
	}
	suggestions := compactJSON(in.Suggestions, "[]")
	if len(suggestions) > maxInput {
		suggestions = "[]"
	}
	var riskContext risk.Context
	if s.opts.RiskContext != nil {
		riskContext = s.opts.RiskContext(ctx, in.JobID)
	}
	verdict := s.opts.Classifier.Classify(in.ToolName, json.RawMessage(input), riskContext)
	reasons, _ := json.Marshal(verdict.Reasons)
	now := s.opts.Now()
	id, err := s.opts.Store.InsertApproval(ctx, store.ApprovalRecord{
		JobID: in.JobID, PaneID: in.PaneID, AgentName: in.AgentName, SessionID: in.SessionID, Cwd: in.Cwd,
		ToolName: in.ToolName, ToolInput: input, Suggestions: suggestions, Risk: string(verdict.Level), RiskReasons: string(reasons),
		Hooked: in.Hooked, CreatedAt: now, ExpiresAt: now.Add(s.opts.TTL),
	})
	if err != nil {
		return Approval{}, err
	}
	s.mu.Lock()
	s.settled[id] = make(chan struct{})
	s.lastPoll[id] = now
	s.mu.Unlock()
	approval, err := s.get(ctx, id)
	if err != nil {
		return Approval{}, err
	}
	s.announce(ctx, approval, "requested")
	return approval, nil
}

// Get returns one approval.
func (s *Service) Get(ctx context.Context, id string) (Approval, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return Approval{}, err
	}
	return s.get(ctx, numeric)
}

func (s *Service) get(ctx context.Context, id int64) (Approval, error) {
	record, err := s.opts.Store.GetApproval(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Approval{}, ErrNotFound
	}
	if err != nil {
		return Approval{}, err
	}
	return toApproval(record), nil
}

// List returns approvals, newest first; statuses filters (none: all).
func (s *Service) List(ctx context.Context, statuses []Status, limit int) ([]Approval, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var names []string
	for _, status := range statuses {
		names = append(names, string(status))
	}
	records, err := s.opts.Store.ListApprovals(ctx, names, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Approval, 0, len(records))
	for _, record := range records {
		out = append(out, toApproval(record))
	}
	return out, nil
}

// PendingForJob reports whether a job has an approval waiting.
func (s *Service) PendingForJob(ctx context.Context, jobID string) bool {
	if jobID == "" {
		return false
	}
	count, err := s.opts.Store.CountPendingApprovals(ctx, jobID)
	return err == nil && count > 0
}

// Wait long-polls one approval: it returns as soon as it is no longer pending, or after max with
// it still pending. Each call counts as the hook being alive.
func (s *Service) Wait(ctx context.Context, id string, max time.Duration) (Approval, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return Approval{}, err
	}
	approval, err := s.get(ctx, numeric)
	if err != nil || approval.Status != StatusPending {
		return approval, err
	}
	s.mu.Lock()
	ch, ok := s.settled[numeric]
	if !ok {
		// Pending but not ours: left by an earlier crowsnest process (RecoverAtStart settles
		// those), or a race with its settle. Make a channel so a settle still wakes us.
		ch = make(chan struct{})
		s.settled[numeric] = ch
	}
	s.lastPoll[numeric] = s.opts.Now()
	s.waiting[numeric]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.waiting[numeric]--
		if s.waiting[numeric] <= 0 {
			delete(s.waiting, numeric)
		}
		if _, live := s.lastPoll[numeric]; live {
			s.lastPoll[numeric] = s.opts.Now()
		}
		s.mu.Unlock()
	}()
	timer := time.NewTimer(max)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	case <-ctx.Done():
		return Approval{}, ctx.Err()
	}
	return s.get(ctx, numeric)
}

// Decision is a human's answer.
type Decision struct {
	Allow bool
	Note  string
	// Confirmed is the explicit second confirmation a dangerous allow needs.
	Confirmed bool
	Via       string
}

// Decide settles a pending approval with a human's answer.
func (s *Service) Decide(ctx context.Context, id string, decision Decision) (Approval, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return Approval{}, err
	}
	approval, err := s.get(ctx, numeric)
	if err != nil {
		return Approval{}, err
	}
	if approval.Status != StatusPending {
		return approval, ErrNotPending
	}
	if decision.Via == ViaNotification && approval.Risk != risk.Low {
		return approval, ErrVia
	}
	if decision.Allow && approval.Risk == risk.Dangerous && !decision.Confirmed {
		return approval, ErrConfirm
	}
	note := strings.TrimSpace(decision.Note)
	if len(note) > maxNote {
		return approval, fmt.Errorf("the note is too long (at most %d characters)", maxNote)
	}
	via := decision.Via
	if via == "" {
		via = ViaBrowser
	}
	status := StatusDenied
	if decision.Allow {
		status = StatusAllowed
	}
	return s.settle(ctx, numeric, status, via, note, "")
}

// settle moves a pending approval to status, wakes its waiter and announces it.
func (s *Service) settle(ctx context.Context, id int64, status Status, via, note, reason string) (Approval, error) {
	if err := s.opts.Store.SettleApproval(ctx, id, string(status), via, note, reason, s.opts.Now()); err != nil {
		if errors.Is(err, store.ErrNotPending) {
			approval, _ := s.get(ctx, id)
			return approval, ErrNotPending
		}
		if errors.Is(err, store.ErrNotFound) {
			return Approval{}, ErrNotFound
		}
		return Approval{}, err
	}
	s.mu.Lock()
	if ch, ok := s.settled[id]; ok {
		close(ch)
		delete(s.settled, id)
	}
	delete(s.lastPoll, id)
	delete(s.misses, id)
	s.mu.Unlock()
	approval, err := s.get(ctx, id)
	if err != nil {
		return Approval{}, err
	}
	s.announce(ctx, approval, string(status))
	return approval, nil
}

// SupersedePane supersedes every pending approval in a pane (its turn or session ended); it
// returns how many.
func (s *Service) SupersedePane(ctx context.Context, paneID, reason string) int {
	if paneID == "" {
		return 0
	}
	count := 0
	for _, approval := range s.pending(ctx) {
		if approval.PaneID == paneID && approval.hooked() {
			if _, err := s.settle(ctx, mustParse(approval.ID), StatusSuperseded, "", "", reason); err == nil {
				count++
			}
		}
	}
	return count
}

// SupersedeRan supersedes a pending approval for a tool call that has now run (PostToolUse):
// the human answered it in the terminal.
func (s *Service) SupersedeRan(ctx context.Context, paneID, tool string, input json.RawMessage) int {
	if paneID == "" {
		return 0
	}
	ran := compactJSON(input, "{}")
	count := 0
	for _, approval := range s.pending(ctx) {
		if approval.PaneID == paneID && approval.ToolName == tool && sameInput(string(approval.ToolInput), ran) && approval.hooked() {
			if _, err := s.settle(ctx, mustParse(approval.ID), StatusSuperseded, "", "", "Answered in the terminal: the tool ran."); err == nil {
				count++
			}
		}
	}
	return count
}

// sameInput compares two tool inputs as JSON values (key order and spacing aside). Claude can
// add fields after the permission step, so a PostToolUse input that holds every field of the
// request with the same value counts.
func sameInput(requested, ran string) bool {
	var a, b map[string]any
	if json.Unmarshal([]byte(requested), &a) != nil || json.Unmarshal([]byte(ran), &b) != nil {
		return requested == ran
	}
	for key, value := range a {
		left, _ := json.Marshal(value)
		right, _ := json.Marshal(b[key])
		if string(left) != string(right) {
			return false
		}
	}
	return true
}

// Sweep expires approvals past their time and supersedes hooked ones whose hook stopped polling.
func (s *Service) Sweep(ctx context.Context) {
	now := s.opts.Now()
	for _, approval := range s.pending(ctx) {
		id := mustParse(approval.ID)
		if !now.Before(approval.ExpiresAt) {
			_, _ = s.settle(ctx, id, StatusExpired, "", "", "Nobody answered in time (expiry is not consent). The terminal dialog is still there.")
			continue
		}
		if !approval.hooked() {
			continue
		}
		s.mu.Lock()
		last, known := s.lastPoll[id]
		active := s.waiting[id] > 0
		s.mu.Unlock()
		if !known {
			last = approval.CreatedAt
		}
		if !active && now.Sub(last) > s.opts.PollGrace {
			_, _ = s.settle(ctx, id, StatusSuperseded, "", "", "The agent stopped waiting for this answer (its hook ended).")
		}
	}
}

// Screen is what a look at a pane found: whether a dialog is on it, and whether the look worked.
type Screen struct {
	Dialog bool
	Known  bool
}

// ReconcileScreens supersedes a hooked approval whose pane has shown no dialog on two looks in a
// row (once it is old enough for the dialog to have been drawn): it was answered in the terminal,
// or the turn was interrupted.
func (s *Service) ReconcileScreens(ctx context.Context, look func(ctx context.Context, paneID string) Screen) {
	now := s.opts.Now()
	for _, approval := range s.pending(ctx) {
		if !approval.hooked() || approval.PaneID == "" || now.Sub(approval.CreatedAt) < screenGrace {
			continue
		}
		id := mustParse(approval.ID)
		screen := look(ctx, approval.PaneID)
		if !screen.Known {
			continue
		}
		s.mu.Lock()
		if screen.Dialog {
			delete(s.misses, id)
			s.mu.Unlock()
			continue
		}
		s.misses[id]++
		misses := s.misses[id]
		s.mu.Unlock()
		if misses >= screenMisses {
			_, _ = s.settle(ctx, id, StatusSuperseded, "", "", "Answered in the terminal (the dialog is gone).")
		}
	}
}

// RecoverAtStart supersedes the hooked approvals a previous crowsnest process left pending: their
// hooks' long-polls died with it (each hook then prints nothing, leaving the terminal dialog).
func (s *Service) RecoverAtStart(ctx context.Context) {
	for _, approval := range s.pending(ctx) {
		if approval.hooked() {
			_, _ = s.settle(ctx, mustParse(approval.ID), StatusSuperseded, "", "", "crowsnest restarted while this was waiting; answer it in the terminal.")
		}
	}
}

// Run sweeps every few seconds until ctx ends. look, when set, reconciles against the screens.
func (s *Service) Run(ctx context.Context, look func(ctx context.Context, paneID string) Screen) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep(ctx)
			if look != nil {
				s.ReconcileScreens(ctx, look)
			}
		}
	}
}

func (s *Service) pending(ctx context.Context) []Approval {
	approvals, err := s.List(ctx, []Status{StatusPending}, 500)
	if err != nil {
		s.opts.Logger.Warn("listing pending approvals", "err", err)
		return nil
	}
	return approvals
}

func (s *Service) announce(ctx context.Context, approval Approval, change string) {
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: "approval", Data: Event{ID: approval.ID, Status: approval.Status, Risk: approval.Risk,
			Tool: approval.ToolName, JobID: approval.JobID, PaneID: approval.PaneID, Change: change}})
	}
	if s.opts.OnChange != nil {
		s.opts.OnChange(ctx, approval, change)
	}
}

// hooked is carried on the record; the API shape leaves it out.
func (a Approval) hooked() bool { return a.hookedFlag }

// HookOutput is what `crowsnest hook permission-request` prints for a settled approval: the
// decision JSON for allowed or denied, and "" for anything else (expired, superseded), which
// leaves Claude's own permission dialog. An allow carries no updatedPermissions: nothing here can
// widen an agent's permanent permissions.
func HookOutput(approval Approval) string {
	var decision map[string]any
	switch approval.Status {
	case StatusAllowed:
		decision = map[string]any{"behavior": "allow"}
	case StatusDenied:
		message := "The human denied this tool call in crowsnest."
		if approval.Note != "" {
			message += " Their note: " + approval.Note
		} else {
			message += " Do not retry it as it was; carry on with the rest, or ask the human (the ask tool) if you are blocked."
		}
		decision = map[string]any{"behavior": "deny", "message": message}
	default:
		return ""
	}
	body, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": decision}})
	return string(body)
}

// Summary renders a tool call in one line.
func Summary(tool string, input json.RawMessage) string {
	var fields map[string]any
	_ = json.Unmarshal(input, &fields)
	text := termtext.Clean(risk.SummaryText(fields)) // format characters (bidi among them) go: R5-S7
	if text == "" {
		return tool
	}
	runes := []rune(text)
	if len(runes) <= risk.MaxSummaryRunes {
		return tool + ": " + text
	}
	// Say how much is not shown: a bare ellipsis hid that a payload could sit past the cut. Such
	// a call is never low (risk.shapeHits), so no notification offers to allow it unseen.
	more := fmt.Sprintf("… (%d more characters)", len(runes)-(risk.MaxSummaryRunes-1))
	return tool + ": " + string(runes[:risk.MaxSummaryRunes-1]) + more
}

func toApproval(r store.ApprovalRecord) Approval {
	var reasons []string
	_ = json.Unmarshal([]byte(r.RiskReasons), &reasons)
	if reasons == nil {
		reasons = []string{}
	}
	approval := Approval{
		ID: FormatID(r.ID), JobID: r.JobID, PaneID: r.PaneID, AgentName: r.AgentName, SessionID: r.SessionID, Cwd: r.Cwd,
		ToolName: r.ToolName, ToolInput: json.RawMessage(r.ToolInput), Suggestions: json.RawMessage(r.Suggestions),
		Risk: risk.Level(r.Risk), RiskReasons: reasons, Status: Status(r.Status), DecidedVia: r.DecidedVia, Note: r.Note,
		StatusReason: r.StatusReason, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, hookedFlag: r.Hooked,
	}
	if !r.SettledAt.IsZero() {
		settled := r.SettledAt
		approval.SettledAt = &settled
	}
	approval.Summary = Summary(r.ToolName, approval.ToolInput)
	return approval
}

func compactJSON(raw json.RawMessage, empty string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return empty
	}
	var out strings.Builder
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return empty
	}
	body, err := json.Marshal(value)
	if err != nil {
		return empty
	}
	out.Write(body)
	return out.String()
}

func mustParse(id string) int64 {
	value, _ := ParseID(id)
	return value
}
