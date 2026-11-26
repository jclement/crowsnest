// What an agent may create (R2-H1). job_create on the agent socket used to accept any directory,
// any engine and isolate:false from any identified agent, with no limit: a prompt-injected builder
// could start Codex (approvals off) in $HOME, and a looping one could create jobs without end.
//
// The rules, for a job agent delegating from job P:
//
//   - The child runs inside P's working directory (or P's repository root), with P's engine,
//     P's model unless it names another, and P's isolation unless it asks for more (a worktree).
//   - Anything wider becomes a blocking question to the human instead of a launch: another
//     directory, no directory (that hands the work to the orchestrator), another engine, or
//     isolate:false while P is isolated. The job is recorded queued with cause approval and
//     waits; "allow" launches it, anything else (deny, expiry, cancel) cancels it, and the answer
//     is typed back to the asking agent like any other.
//   - Limits, each a plain refusal: at most MaxDepth levels of delegation, MaxChildren children
//     per parent (ever, so a loop that finishes its children quickly is still stopped), and
//     MaxLiveAgentJobs live agent-created jobs in all. The socket adds a per-caller rate limit.
//
// The orchestrator is the human's lead: it may create a job in any directory the human has given
// a job before (the remembered list) or with no directory (it holds that job itself) without
// asking, and must ask for any other directory. Jobs the human creates in the browser are not
// limited at all. Model, role and engine are validated strictly for everyone (newRecord).

package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
)

// Origins of a job (store.JobRecord.Origin).
const (
	OriginHuman        = ""
	OriginAgent        = "agent"
	OriginOrchestrator = "orchestrator"
)

// DelegationLimits bound what agents may create. Zero fields take the defaults.
type DelegationLimits struct {
	MaxDepth         int // levels of delegation below the human (default 3)
	MaxChildren      int // children one job may create, ever (default 8)
	MaxLiveAgentJobs int // agent-created jobs not yet finished, across the session (default 16)
}

// Default delegation limits.
const (
	DefaultMaxDepth         = 3
	DefaultMaxChildren      = 8
	DefaultMaxLiveAgentJobs = 16
)

// maxAncestors bounds the walk up the parent chain (a damaged chain cannot loop forever).
const maxAncestors = 32

// Approval question answers.
const (
	approvalAllow = "allow"
	approvalDeny  = "deny"
	// approvalContextKey marks an approval question; its value is the held job's id.
	approvalContextKey = "approval_for"
)

// AgentCaller is who called job_create, as the socket's token says.
type AgentCaller struct {
	PaneID string
	JobID  string // empty for the orchestrator, whose tokens name no job
}

// creator is the resolved caller: where its authority comes from.
type creator struct {
	origin string
	paneID string
	parent *store.JobRecord // the caller's job (the orchestrator's held job, if any)
}

// label names the creator in the human's question.
func (c creator) label() string {
	if c.origin == OriginOrchestrator {
		return "The orchestrator"
	}
	return "Job " + FormatID(c.parent.ID)
}

func (c creator) parentID() string {
	if c.parent == nil {
		return ""
	}
	return FormatID(c.parent.ID)
}

func (s *Service) limits() DelegationLimits {
	l := s.opts.Delegation
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	if l.MaxChildren <= 0 {
		l.MaxChildren = DefaultMaxChildren
	}
	if l.MaxLiveAgentJobs <= 0 {
		l.MaxLiveAgentJobs = DefaultMaxLiveAgentJobs
	}
	return l
}

// CreateForAgent is job_create: Create under the rules above. A job that needs the human's
// approval is returned queued with cause approval; it starts only if the human allows it.
func (s *Service) CreateForAgent(ctx context.Context, input CreateInput, caller AgentCaller) (Job, error) {
	who, err := s.creatorOf(ctx, caller)
	if err != nil {
		return Job{}, err
	}
	input = who.inherit(input)
	record, err := s.newRecord(input, who.parentID())
	if err != nil {
		return Job{}, err
	}
	record.Origin = who.origin
	wider := s.widerThanAllowed(ctx, who, record)
	if len(wider) > 0 {
		record.Cause = string(CauseApproval)
		record.CauseDetail = "Asked the human to allow it: " + strings.Join(wider, "; ")
	}
	s.mu.Lock()
	err = s.checkLimitsLocked(ctx, who)
	if err == nil {
		err = s.insertLocked(ctx, &record)
	}
	s.mu.Unlock()
	if err != nil {
		return Job{}, err
	}
	job := s.toJob(record, 0)
	s.publish(job, "created")
	if who.parent != nil {
		_ = s.Journal(ctx, who.parentID(), EventChild, "Delegated "+job.ID+": "+job.Title, map[string]any{"child": job.ID, "held": len(wider) > 0})
	}
	if len(wider) == 0 {
		s.goBackground(ctx, job.ID, func(ctx context.Context) { s.launch(ctx, job.ID) })
		return job, nil
	}
	return s.askApproval(ctx, job, who, wider)
}

// creatorOf resolves the caller: a job agent (its job is the parent) or the orchestrator.
func (s *Service) creatorOf(ctx context.Context, caller AgentCaller) (creator, error) {
	if caller.PaneID == "" {
		return creator{}, invalid("only an agent crowsnest started can create jobs")
	}
	if caller.JobID != "" {
		id, err := ParseID(caller.JobID)
		if err != nil {
			return creator{}, invalid("your token names no valid job")
		}
		parent, err := s.opts.Store.GetJob(ctx, id)
		if err != nil {
			return creator{}, invalid("your job %s cannot be read: %v", caller.JobID, err)
		}
		if State(parent.State).Terminal() {
			return creator{}, invalid("your job %s is already %s; a finished job cannot delegate", caller.JobID, parent.State)
		}
		return creator{origin: OriginAgent, paneID: caller.PaneID, parent: &parent}, nil
	}
	if s.opts.Orchestrator == nil || s.opts.Orchestrator().PaneID != caller.PaneID {
		return creator{}, invalid("only job agents and the orchestrator can create jobs")
	}
	who := creator{origin: OriginOrchestrator, paneID: caller.PaneID}
	held, err := s.opts.Store.ListJobs(ctx, []string{string(StateRunning), string(StateNeedsYou), string(StateBlocked)}, listLimit)
	if err != nil {
		return creator{}, err
	}
	for i := range held {
		if held[i].AgentName == OrchestratorName && held[i].PaneID == caller.PaneID {
			who.parent = &held[i]
			break
		}
	}
	return who, nil
}

// inherit fills what a job agent's child leaves unset from its parent: the engine, the model
// (when the engine is the same) and the isolation (a child of an isolated job is isolated).
func (c creator) inherit(input CreateInput) CreateInput {
	if c.origin != OriginAgent {
		return input
	}
	if strings.TrimSpace(input.Engine) == "" {
		input.Engine = c.parent.Engine
	}
	if strings.TrimSpace(input.Model) == "" && strings.TrimSpace(input.Engine) == c.parent.Engine {
		input.Model = c.parent.Model
	}
	if input.Isolate == nil && c.parent.Isolate {
		isolate := true
		input.Isolate = &isolate
	}
	return input
}

// widerThanAllowed lists, in words for the human, what the new job asks for beyond what its
// creator may grant itself. Empty means it may start at once.
func (s *Service) widerThanAllowed(ctx context.Context, who creator, record store.JobRecord) []string {
	if who.origin == OriginOrchestrator {
		if record.Dir == "" || withinAny(record.Dir, s.RememberedDirs(ctx)) {
			return nil
		}
		return []string{"in " + displayPath(record.Dir) + ", a directory no job of yours has used"}
	}
	parent := who.parent
	var wider []string
	switch {
	case record.Dir == "":
		wider = append(wider, "with no directory, which hands it to the orchestrator")
	case !withinAny(record.Dir, jobRoots(ctx, *parent)):
		wider = append(wider, "in "+displayPath(record.Dir)+", outside its own directory")
	}
	if record.Engine != parent.Engine {
		wider = append(wider, fmt.Sprintf("as %s (it runs as %s)", record.Engine, parent.Engine))
	}
	if record.Dir != "" && parent.Isolate && !record.Isolate {
		wider = append(wider, "without a worktree (it works in one)")
	}
	return wider
}

// jobRoots are the directories a job's children may use: its working directory and the root of
// the git repository (or worktree) that holds it.
func jobRoots(ctx context.Context, job store.JobRecord) []string {
	dir := job.WorkDir
	if dir == "" {
		return nil // not started yet: it has no directory of its own to share
	}
	roots := []string{dir}
	gitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(gitCtx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output() //nolint:gosec // a directory crowsnest recorded, one argument, no shell
	if top := strings.TrimSpace(string(out)); err == nil && top != "" {
		roots = append(roots, top)
	}
	return roots
}

// withinAny reports whether dir is one of roots or inside one, after resolving symlinks on both
// sides (a link inside the worktree that points at $HOME does not count as inside).
func withinAny(dir string, roots []string) bool {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	for _, root := range roots {
		rootResolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootResolved, resolved)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

// displayPath shows a path with ~ for the home directory, cleaned to one line.
func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			path = "~"
		} else if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			path = "~/" + rest
		}
	}
	return termtext.Cap(termtext.Clean(path), 100)
}

// checkLimitsLocked refuses a job past the depth, fan-out or global cap (s.mu held, so two
// concurrent calls cannot both take the last place).
func (s *Service) checkLimitsLocked(ctx context.Context, who creator) error {
	limits := s.limits()
	if who.parent != nil {
		depth, err := s.depthOf(ctx, *who.parent)
		if err != nil {
			return err
		}
		if depth+1 > limits.MaxDepth {
			return invalid("job %s is already %d levels of delegation deep and crowsnest allows %d; do this part yourself, or ask the human", who.parentID(), depth, limits.MaxDepth)
		}
		children, err := s.opts.Store.CountChildJobs(ctx, who.parent.ID)
		if err != nil {
			return err
		}
		if children >= limits.MaxChildren {
			return invalid("job %s has already created %d jobs, the most one job may; do the rest yourself, or ask the human", who.parentID(), children)
		}
	}
	live, err := s.opts.Store.CountLiveAgentJobs(ctx)
	if err != nil {
		return err
	}
	if live >= limits.MaxLiveAgentJobs {
		return invalid("agents already have %d jobs open, the most crowsnest allows at once; wait for some to finish, or ask the human", live)
	}
	return nil
}

// depthOf is how many levels of delegation a job sits below the human: 0 for a job the human
// gave, 1 for one the orchestrator or a human's job created, and so on.
func (s *Service) depthOf(ctx context.Context, job store.JobRecord) (int, error) {
	depth := 0
	for range maxAncestors {
		if job.ParentID == 0 {
			if job.Origin != OriginHuman {
				depth++
			}
			return depth, nil
		}
		depth++
		parent, err := s.opts.Store.GetJob(ctx, job.ParentID)
		if errors.Is(err, store.ErrNotFound) {
			return depth, nil
		}
		if err != nil {
			return 0, err
		}
		job = parent
	}
	return depth, nil
}

// ---- Approval ------------------------------------------------------------------------------

// askApproval puts the held job in front of the human as a blocking question. The asker is the
// creating agent's pane, so the answer is typed back to it; the question belongs to the held job,
// so its card shows it. If the question cannot be asked, the job is cancelled and the agent told.
func (s *Service) askApproval(ctx context.Context, job Job, who creator, wider []string) (Job, error) {
	if s.opts.AskHuman == nil {
		s.cancelHeld(ctx, job.ID, "crowsnest could not ask the human")
		return Job{}, invalid("this job needs the human's approval (%s) and crowsnest cannot ask from here; it was not created", strings.Join(wider, "; "))
	}
	question, err := s.opts.AskHuman(ctx, approvalQuestion(job, who, wider), questions.Caller{PaneID: who.paneID, JobID: job.ID})
	if err != nil {
		s.cancelHeld(ctx, job.ID, "the approval question could not be asked")
		return Job{}, fmt.Errorf("asking the human to allow %s: %w", job.ID, err)
	}
	return s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
		if State(r.State) == StateQueued && Cause(r.Cause) == CauseApproval {
			r.CauseDetail = question.ID
		}
		c.event(EventQuestion, "Held for the human's approval ("+question.ID+"): "+strings.Join(wider, "; "), map[string]any{"question": question.ID})
		return nil
	})
}

// approvalQuestion is the human's question: who wants what, why it needs them, and the brief.
func approvalQuestion(job Job, who creator, wider []string) questions.AskInput {
	where := "with no directory"
	if job.Dir != "" {
		where = "in " + displayPath(job.Dir)
	}
	title := termtext.Cap(fmt.Sprintf("%s wants to start a job %s as %s: allow?", who.label(), where, job.Engine), 200)
	var body strings.Builder
	fmt.Fprintf(&body, "%s asked crowsnest to start **%s** (role %s, engine %s", who.label(), job.ID, job.Role, job.Engine)
	if job.Model != "" {
		fmt.Fprintf(&body, ", model %s", job.Model)
	}
	body.WriteString(").\n\nIt needs you because it would run:\n\n")
	for _, reason := range wider {
		fmt.Fprintf(&body, "- %s\n", reason)
	}
	body.WriteString("\nAllow starts it. Deny, or no answer before this expires, cancels it; either way the asking agent is told.\n\n")
	body.WriteString("The brief, as the agent wrote it:\n\n")
	for _, line := range strings.Split(job.BriefMD, "\n") {
		body.WriteString("    " + line + "\n") // an indented code block: nothing in it renders as Markdown
	}
	return questions.AskInput{
		Title: title, BodyMD: body.String(), Kind: questions.KindChoice,
		Options: []questions.Option{
			{Key: approvalAllow, Label: "Allow: start it"},
			{Key: approvalDeny, Label: "Deny: cancel it"},
		},
		Recommend: approvalDeny, Urgency: questions.UrgencyBlocking,
		Context: map[string]string{approvalContextKey: job.ID, "requested_by": strings.ToLower(who.label())},
	}
}

// IsApprovalQuestion reports whether a question is a job approval, and for which job.
func IsApprovalQuestion(question questions.Question) (string, bool) {
	jobID := question.Context[approvalContextKey]
	return jobID, jobID != ""
}

// ApprovalSettled applies the human's decision on a held job, reading the question from the
// store, so the same call serves the settle callback and the start-up reconcile (a decision
// that landed just before a crash is not lost). It does nothing for a job not held for approval
// or a question still open.
func (s *Service) ApprovalSettled(ctx context.Context, jobID string) {
	detail, err := s.Get(ctx, jobID)
	if err != nil || detail.State != StateQueued || detail.Cause != CauseApproval {
		return
	}
	questionID, err := questions.ParseID(detail.CauseDetail)
	if err != nil {
		return // not asked yet (or the ask failed and cancelled it)
	}
	question, err := s.opts.Store.GetDelivery(ctx, questionID)
	if err != nil || question.Status == string(questions.StatusOpen) {
		return
	}
	if question.Status == string(questions.StatusAnswered) && string(question.AnswerValue) == `"`+approvalAllow+`"` {
		s.approve(ctx, jobID, detail.CauseDetail)
		return
	}
	s.cancelHeld(ctx, jobID, fmt.Sprintf("the human did not allow it (%s %s; not consent)", detail.CauseDetail, question.Status))
}

// approve releases a held job and launches it. Its directory joins the remembered list: the
// human has now allowed it.
func (s *Service) approve(ctx context.Context, jobID, questionID string) {
	job, err := s.mutate(ctx, jobID, func(r *store.JobRecord, c *change) error {
		if State(r.State) != StateQueued || Cause(r.Cause) != CauseApproval {
			return errNotHeld
		}
		r.Cause, r.CauseDetail = "", ""
		c.event(EventState, "Allowed by the human ("+questionID+")", nil)
		return nil
	})
	if err != nil {
		return
	}
	if job.Dir != "" {
		s.rememberDir(ctx, job.Dir)
	}
	s.goBackground(ctx, jobID, func(ctx context.Context) { s.launch(ctx, jobID) })
}

// errNotHeld stops an approval that raced another change to the job.
var errNotHeld = errors.New("the job is no longer held for approval")

// cancelHeld cancels a job that was held for approval.
func (s *Service) cancelHeld(ctx context.Context, jobID, why string) {
	_, _ = s.mutate(ctx, jobID, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return nil
		}
		r.State, r.Cause, r.CauseDetail = string(StateCancelled), "", ""
		c.event(EventState, "Not started: "+why, nil)
		return nil
	})
}

// ReconcileApprovals applies decisions on every held job (serve calls it at start).
func (s *Service) ReconcileApprovals(ctx context.Context) {
	held, err := s.opts.Store.ListJobs(ctx, []string{string(StateQueued)}, listLimit)
	if err != nil {
		s.opts.Logger.Warn("reading jobs held for approval", "err", err)
		return
	}
	for _, record := range held {
		if Cause(record.Cause) == CauseApproval {
			s.ApprovalSettled(ctx, FormatID(record.ID))
		}
	}
}

// held reports a job waiting for the human's approval: the launcher and the orchestrator
// dispatch leave it alone.
func held(record store.JobRecord) bool {
	return State(record.State) == StateQueued && Cause(record.Cause) == CauseApproval
}
