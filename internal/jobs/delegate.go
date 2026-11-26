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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

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
	parent *store.JobRecord // the caller's job (the orchestrator's or a lead's held job, if any)
	// lead is set when a project's lead is the caller (origin is its agent name, lead-<slug>):
	// its children run inside its project's root without asking (DESIGN.md 21.2).
	lead *StandingAgent
}

// standing reports whether the creator is a standing agent (the orchestrator or a lead).
func (c creator) standing() bool { return c.origin == OriginOrchestrator || c.lead != nil }

// label names the creator in the human's question.
func (c creator) label() string {
	if c.origin == OriginOrchestrator {
		return "The orchestrator"
	}
	if c.lead != nil {
		return "The project lead " + c.lead.Name
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
	if len(input.Attachments) > 0 {
		return Job{}, invalid("attachments are the human's uploads; an agent's job cannot carry them. Name the files in the brief instead")
	}
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
	switch {
	case who.lead != nil:
		record.ProjectID = who.lead.ProjectID
	case who.parent != nil:
		record.ProjectID = who.parent.ProjectID // a child stays in its parent's project
	}
	// The directory is resolved once, here, and that resolved path is what is checked, stored,
	// shown to the human and launched (R3-H1): a link the agent flips afterwards cannot move the
	// child, and the launcher refuses a path that no longer resolves to itself.
	requested := record.Dir
	if record.Dir != "" {
		if record.Dir, err = filepath.EvalSymlinks(record.Dir); err != nil {
			return Job{}, invalid("dir %s cannot be resolved: %v", displayPath(requested), err)
		}
	}
	wider := s.widerThanAllowed(ctx, who, record)
	if len(wider) > 0 {
		record.Cause = string(CauseApproval)
		record.CauseDetail = "Asked the human to allow it: " + strings.Join(wider, "; ")
	}
	s.mu.Lock()
	err = s.checkLimitsLocked(ctx, who, len(wider) > 0)
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
	return s.askApproval(ctx, job, who, wider, requested)
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
	agent, ok := s.standingOf(caller.PaneID)
	if !ok {
		return creator{}, invalid("only job agents, project leads and the orchestrator can create jobs")
	}
	who := creator{origin: OriginOrchestrator, paneID: caller.PaneID}
	if agent.ProjectID != 0 {
		who.origin, who.lead = agent.Name, &agent
	}
	held, err := s.opts.Store.ListJobs(ctx, []string{string(StateRunning), string(StateNeedsYou), string(StateBlocked)}, listLimit)
	if err != nil {
		return creator{}, err
	}
	for i := range held {
		if held[i].AgentName == agent.Name && held[i].PaneID == caller.PaneID {
			who.parent = &held[i]
			break
		}
	}
	return who, nil
}

// inherit fills what a job agent's child leaves unset from its parent: the engine, the model
// (when the engine is the same) and the isolation (a child of an isolated job is isolated).
func (c creator) inherit(input CreateInput) CreateInput {
	if c.lead != nil && strings.TrimSpace(input.Dir) == "" {
		input.Dir = c.lead.RootDir // a lead's child works in the project unless it says where
	}
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
// creator may grant itself. Empty means it may start at once. record.Dir is already resolved, and
// every root it is compared with was resolved when it was recorded: nothing here resolves a path
// or asks git again, since the agent could have changed either since (R3-H1).
func (s *Service) widerThanAllowed(ctx context.Context, who creator, record store.JobRecord) []string {
	var wider []string
	if who.lead != nil {
		// A lead is held to the orchestrator's engine rule and confined to its project's root,
		// recorded resolved when the project was made (never recomputed, R3-H1).
		if record.Engine != EngineClaude {
			wider = append(wider, fmt.Sprintf("as %s (crowsnest's agents run as %s)", record.Engine, EngineClaude))
		}
		if !withinRoots(record.Dir, safeRoots([]string{who.lead.RootDir})) {
			wider = append(wider, "in "+mdCode(displayPath(record.Dir))+", outside its project's directory")
		}
		return wider
	}
	if who.origin == OriginOrchestrator {
		// The orchestrator is held to the same engine rule as any agent (R3-M2): Codex runs with
		// approvals off, so only the human starts one.
		if record.Engine != EngineClaude {
			wider = append(wider, fmt.Sprintf("as %s (crowsnest's agents run as %s)", record.Engine, EngineClaude))
		}
		if record.Dir != "" && !withinRoots(record.Dir, safeRoots(s.RememberedDirs(ctx))) {
			wider = append(wider, "in "+mdCode(displayPath(record.Dir))+", a directory no job of yours has used")
		}
		return wider
	}
	parent := who.parent
	switch {
	case record.Dir == "":
		wider = append(wider, "with no directory, which hands it to the orchestrator")
	case !withinRoots(record.Dir, safeRoots([]string{parent.RootDir})):
		wider = append(wider, "in "+mdCode(displayPath(record.Dir))+", outside its own directory")
	}
	if record.Engine != parent.Engine {
		wider = append(wider, fmt.Sprintf("as %s (it runs as %s)", record.Engine, parent.Engine))
	}
	if record.Dir != "" && parent.Isolate && !record.Isolate {
		wider = append(wider, "without a worktree (it works in one)")
	}
	return wider
}

// confinementRoot is the root a job's children may use, recorded when crowsnest creates the
// job's pane in workDir: workDir with its symlinks resolved, or "" (nothing granted) when it
// cannot be resolved or is too wide to hand out (safeRoot).
func confinementRoot(workDir string) string {
	if workDir == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(workDir)
	if err != nil || !safeRoot(resolved) {
		return ""
	}
	return resolved
}

// safeRoot reports whether root may grant anything: not empty, not relative, not /, and neither
// the home directory nor a directory that holds it (a dotfiles repository, a job given in ~).
func safeRoot(root string) bool {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) == string(filepath.Separator) {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return true
	}
	for _, h := range homeForms(home) {
		if within(h, filepath.Clean(root)) {
			return false
		}
	}
	return true
}

// homeForms is the home directory as written and as resolved (it may be reached by a link).
func homeForms(home string) []string {
	forms := []string{filepath.Clean(home)}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != forms[0] {
		forms = append(forms, resolved)
	}
	return forms
}

// safeRoots keeps the roots that may grant anything.
func safeRoots(roots []string) []string {
	var out []string
	for _, root := range roots {
		if safeRoot(root) {
			out = append(out, filepath.Clean(root))
		}
	}
	return out
}

// withinRoots reports whether dir is one of roots or inside one. Both sides must already be
// resolved: this compares paths, it does not touch the filesystem.
func withinRoots(dir string, roots []string) bool {
	for _, root := range roots {
		if within(dir, root) {
			return true
		}
	}
	return false
}

// within reports whether path is root or inside it, by path alone (".." and prefix tricks such
// as /a/bc against /a/b fail).
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// mdCode puts text in a Markdown code span whose fence is longer than any run of backticks in it,
// so nothing in it (a directory named like a link or a heading) renders as Markdown (R3-M1).
func mdCode(text string) string {
	fence := strings.Repeat("`", longestRun(text, '`')+1)
	return fence + " " + text + " " + fence
}

// longestRun is the longest run of ch in text.
func longestRun(text string, ch rune) int {
	longest, run := 0, 0
	for _, r := range text {
		if r == ch {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// displayPath shows a path with ~ for the home directory, cleaned to one line.
func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, form := range homeForms(home) { // a resolved path names the resolved home
			if path == form {
				path = "~"
				break
			} else if rest, ok := strings.CutPrefix(path, form+string(filepath.Separator)); ok {
				path = "~/" + rest
				break
			}
		}
	}
	return termtext.Cap(termtext.Clean(path), 100)
}

// maxHeldPerCreator is how many requests one creator may have waiting for the human at once
// (R3-L3): an agent in a loop must not fill the Inbox with approvals to deny one by one.
const maxHeldPerCreator = 2

// checkLimitsLocked refuses a job past the depth, fan-out or global cap, or one more request
// waiting for the human than a creator may have (s.mu held, so two concurrent calls cannot both
// take the last place). held says the new job would wait for approval.
func (s *Service) checkLimitsLocked(ctx context.Context, who creator, held bool) error {
	limits := s.limits()
	if held {
		waiting, err := s.opts.Store.CountHeldApprovals(ctx, who.origin, parentNumber(who))
		if err != nil {
			return err
		}
		if waiting >= maxHeldPerCreator {
			return invalid("you already have %d jobs waiting for the human's approval, the most crowsnest allows; wait for their answers, or do it within your own directory", waiting)
		}
	}
	if who.parent == nil && who.standing() {
		// Holding no job, a standing agent has no parent row to count children against: its own
		// live jobs are capped instead (R3-M2). Each lead's origin is its own agent name.
		own, err := s.opts.Store.CountLiveOwnJobs(ctx, who.origin)
		if err != nil {
			return err
		}
		if !held && own >= limits.MaxChildren {
			return invalid("%s already has %d jobs of its own running, the most crowsnest allows; wait for some to finish", strings.ToLower(who.label()[:1])+who.label()[1:], own)
		}
	}
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
	if !held && live >= limits.MaxLiveAgentJobs {
		return invalid("agents already have %d jobs open, the most crowsnest allows at once; wait for some to finish, or ask the human", live)
	}
	return nil
}

// parentNumber is the creator's job as a number (0 for the orchestrator holding no job).
func parentNumber(who creator) int64 {
	if who.parent == nil {
		return 0
	}
	return who.parent.ID
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
func (s *Service) askApproval(ctx context.Context, job Job, who creator, wider []string, requested string) (Job, error) {
	if s.opts.AskHuman == nil {
		s.cancelHeld(ctx, job.ID, "crowsnest could not ask the human")
		return Job{}, invalid("this job needs the human's approval (%s) and crowsnest cannot ask from here; it was not created", strings.Join(wider, "; "))
	}
	question, err := s.opts.AskHuman(ctx, approvalQuestion(job, who, wider, requested), questions.Caller{PaneID: who.paneID, JobID: job.ID})
	if err != nil {
		s.cancelHeld(ctx, job.ID, "the approval question could not be asked")
		return Job{}, fmt.Errorf("asking the human to allow %s: %w", job.ID, err)
	}
	held, err := s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
		if State(r.State) == StateQueued && Cause(r.Cause) == CauseApproval {
			r.CauseDetail = question.ID
		}
		c.event(EventQuestion, "Held for the human's approval ("+question.ID+"): "+strings.Join(wider, "; "), map[string]any{"question": question.ID})
		return nil
	})
	if err != nil {
		return held, err
	}
	// An answer that arrived before the link was stored found nothing to act on (R3-C02); look
	// once more now that it is there. The tick looks too, so nothing hangs on this call.
	s.ApprovalSettled(ctx, job.ID)
	return held, nil
}

// maxApprovalBrief caps the brief quoted in an approval question; the job card has all of it.
const maxApprovalBrief = 3000

// approvalQuestion is the human's question, built by crowsnest from the job's fields (R3-M1): who
// asks, for which job, with which role, engine and model, in which directory (resolved, and as
// the agent wrote it when that differs), why it needs the human, and the brief. Every path is a
// code span, and the brief sits in a fenced block longer than any run of backticks in it, with
// every line break normalised first, so nothing the agent wrote can render as crowsnest's own
// text (a lone CR used to end the indented block and let the rest render as headings).
func approvalQuestion(job Job, who creator, wider []string, requested string) questions.AskInput {
	where := "with no directory"
	if job.Dir != "" {
		where = "in " + displayPath(job.Dir)
	}
	title := termtext.Cap(fmt.Sprintf("%s wants to start a job %s as %s: allow?", who.label(), where, job.Engine), 200)
	var body strings.Builder
	fmt.Fprintf(&body, "**Requested by:** %s\n\n", who.label())
	fmt.Fprintf(&body, "**Job:** %s, role %s, engine %s", job.ID, mdCode(termtext.Clean(job.Role)), job.Engine)
	if job.Model != "" {
		fmt.Fprintf(&body, ", model %s", mdCode(job.Model))
	}
	body.WriteString("\n\n")
	if job.Dir != "" {
		fmt.Fprintf(&body, "**Directory:** %s", mdCode(displayPath(job.Dir)))
		if requested != "" && requested != job.Dir {
			fmt.Fprintf(&body, " (the agent asked for %s, which resolves there)", mdCode(displayPath(requested)))
		}
		body.WriteString("\n\n")
	}
	body.WriteString("**It needs you because it would run:**\n\n")
	for _, reason := range wider {
		fmt.Fprintf(&body, "- %s\n", reason)
	}
	body.WriteString("\nAllow starts it exactly as asked. Deny, no answer before this expires, or Allow with a note cancels it: " +
		"crowsnest cannot follow conditions, so a qualified allow starts nothing, and your note goes to the agent. Either way the agent is told.\n\n")
	body.WriteString("**The brief, as the agent wrote it** (quoted; nothing in it comes from crowsnest):\n\n")
	body.WriteString(fencedQuote(job.BriefMD, maxApprovalBrief))
	return questions.AskInput{
		Title: title, BodyMD: body.String(), Kind: questions.KindChoice,
		Options: []questions.Option{
			{Key: approvalAllow, Label: "Allow: start it as asked"},
			{Key: approvalDeny, Label: "Deny: cancel it"},
		},
		Recommend: approvalDeny, Urgency: questions.UrgencyBlocking,
		Context: map[string]string{approvalContextKey: job.ID, "requested_by": strings.ToLower(who.label())},
	}
}

// fencedQuote renders text as a fenced code block that its content cannot close: line breaks
// (CR, VT, FF, NEL, U+2028, U+2029) become LF and control characters go (termtext.Block), it is
// cut to limit bytes, and the fence is longer than any run of backticks inside.
func fencedQuote(text string, limit int) string {
	text = termtext.Block(text)
	truncated := false
	if len(text) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text, truncated = text[:cut], true
	}
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	out := fence + "text\n" + strings.TrimRight(text, "\n") + "\n" + fence + "\n"
	if truncated {
		out += "\n(cut at " + fmt.Sprint(limit) + " bytes; the job's card has the whole brief)\n"
	}
	return out
}

// IsApprovalQuestion reports whether a question is a job approval, and for which job.
func IsApprovalQuestion(question questions.Question) (string, bool) {
	jobID := question.Context[approvalContextKey]
	return jobID, jobID != ""
}

// ApprovalSettled applies the human's decision on a held job, reading the question from the
// store, so the same call serves the settle callback and the supervisor's tick (a decision that
// landed just before a crash, or before the job learnt its question's id, is not lost). It does
// nothing for a job not held for approval or a question still open.
//
// Only a plain "allow" launches. An allow that carries a note does not (R3-C03): in this product
// the note outranks the choice, and crowsnest cannot follow a condition such as "not until
// tomorrow" or "only with Claude", so a qualified allow is treated as not given. The job is
// cancelled, and the note reaches the asking agent with the answer, which may ask again.
func (s *Service) ApprovalSettled(ctx context.Context, jobID string) {
	detail, err := s.Get(ctx, jobID)
	if err != nil || detail.State != StateQueued || detail.Cause != CauseApproval {
		return
	}
	questionID, err := questions.ParseID(detail.CauseDetail)
	if err != nil {
		var ok bool
		if questionID, ok = s.relinkApproval(ctx, detail.Job); !ok {
			return
		}
	}
	question, err := s.opts.Store.GetDelivery(ctx, questionID)
	if err != nil || question.Status == string(questions.StatusOpen) {
		return
	}
	label := fmt.Sprintf("q_%d", questionID)
	allowed := question.Status == string(questions.StatusAnswered) && string(question.AnswerValue) == `"`+approvalAllow+`"`
	switch {
	case allowed && strings.TrimSpace(question.AnswerNote) == "":
		s.approve(ctx, jobID, label)
	case allowed:
		s.cancelHeld(ctx, jobID, fmt.Sprintf("the human allowed it with a note (%s), and crowsnest does not act on a qualified allow; "+
			"the note went to the asking agent, which may ask again", label))
	default:
		s.cancelHeld(ctx, jobID, fmt.Sprintf("the human did not allow it (%s %s; not consent)", label, question.Status))
	}
}

// approvalAskGrace is how long a held job may go without a linked question before it counts as
// orphaned: askApproval stores the link a moment after creating the job.
const approvalAskGrace = time.Minute

// relinkApproval repairs a held job whose question id was never stored (R3-C02: creating the
// job, asking, and storing the link are separate writes, and a crash can fall between them). An
// approval question attached to the job is found and linked; with none, after approvalAskGrace,
// the job is cancelled: nothing was asked, so nothing can approve it. ok reports a linked question.
func (s *Service) relinkApproval(ctx context.Context, job Job) (int64, bool) {
	records, err := s.opts.Store.ListQuestionsByJob(ctx, job.ID)
	if err != nil {
		return 0, false
	}
	for _, record := range records {
		var context map[string]string
		if json.Unmarshal(record.Context, &context) != nil || context[approvalContextKey] != job.ID {
			continue
		}
		label := fmt.Sprintf("q_%d", record.ID)
		if _, err := s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
			if State(r.State) != StateQueued || Cause(r.Cause) != CauseApproval {
				return errNotHeld
			}
			r.CauseDetail = label
			c.event(EventQuestion, "Found its approval question "+label+" (crowsnest stopped before linking it)", map[string]any{"question": label})
			return nil
		}); err != nil {
			return 0, false
		}
		return record.ID, true
	}
	if s.opts.Now().Sub(job.CreatedAt) >= approvalAskGrace {
		s.cancelHeld(ctx, job.ID, "its approval question was never asked (crowsnest stopped while asking); the agent may ask again")
	}
	return 0, false
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

// cancelHeld cancels a job that was held for approval. It does not use the caller's context: a
// request that was itself cancelled (the agent hung up mid-call) must still clean up its job.
func (s *Service) cancelHeld(ctx context.Context, jobID, why string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), herdrCallTimeout)
	defer cancel()
	_, _ = s.mutate(ctx, jobID, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return nil
		}
		r.State, r.Cause, r.CauseDetail = string(StateCancelled), "", ""
		c.event(EventState, "Not started: "+why, nil)
		return nil
	})
}

// ReconcileApprovals applies decisions on every held job, all of them, a page at a time (serve
// calls it at start; the tick does the same job by job).
func (s *Service) ReconcileApprovals(ctx context.Context) {
	err := s.eachLive(ctx, []State{StateQueued}, func(record store.JobRecord) {
		if Cause(record.Cause) == CauseApproval {
			s.ApprovalSettled(ctx, FormatID(record.ID))
		}
	})
	if err != nil && ctx.Err() == nil {
		s.opts.Logger.Warn("reading jobs held for approval", "err", err)
	}
}
