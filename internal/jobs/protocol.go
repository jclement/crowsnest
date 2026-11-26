// Creating jobs and the agent protocol (DESIGN.md 6.2): job_update, done, fail, job_create and
// job_get, as the socket, the CLI and the MCP tools all call them. Every refusal is a
// ValidationError worded for the agent: it says what to send instead.

package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
)

// CreateInput is a new job: from the browser's text box (POST /jobs) or an agent delegating
// (job_create).
type CreateInput struct {
	Brief    string `json:"brief"`
	Dir      string `json:"dir,omitempty"`
	Role     string `json:"role,omitempty"`
	DoneWhen string `json:"done_when,omitempty"`
	Engine   string `json:"engine,omitempty"`
	Model    string `json:"model,omitempty"`
	// Isolate asks for a git worktree; nil means the role's default (builders isolate).
	Isolate *bool `json:"isolate,omitempty"`
	// Attachments are uploads (a_…) given with the job from the browser: copied into the job's
	// inbox and listed in its brief. Agents cannot pass them (CreateForAgent refuses).
	Attachments []string `json:"attachments,omitempty"`
	// ProjectID gives the job to a project (DESIGN.md 21.1); 0 lets a directory inside a
	// project's choose it, else the job is Unfiled.
	ProjectID int64 `json:"project_id,omitempty"`
	// To is, for a job in a project, "lead" (no directory: the project's lead holds it) or "job"
	// (a job agent in the project's directory). Empty: "lead" without a directory, else "job".
	To string `json:"to,omitempty"`
}

// maxTitleRunes caps the title taken from the brief's first line.
const maxTitleRunes = 120

// kvDirs is where remembered directories are kept (most recent first).
const kvDirs = "jobs.dirs"

// maxRememberedDirs bounds that list.
const maxRememberedDirs = 20

// Create validates and records a job the human gave (the browser's text box), then launches it
// in the background. The returned job is queued; launch progress arrives as `job` events. An
// agent's job_create goes through CreateForAgent (delegate.go), which adds R2-H1's limits;
// parentID here is for tests and the demo.
func (s *Service) Create(ctx context.Context, input CreateInput, parentID string) (Job, error) {
	input, projectID, err := s.applyProject(ctx, input)
	if err != nil {
		return Job{}, err
	}
	record, err := s.newRecord(input, parentID)
	if err != nil {
		return Job{}, err
	}
	record.ProjectID = projectID
	if err := s.checkAttachments(ctx, input.Attachments); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	err = s.insertLocked(ctx, &record)
	s.mu.Unlock()
	if err != nil {
		return Job{}, err
	}
	if record.Dir != "" {
		s.rememberDir(ctx, record.Dir)
	}
	job := s.toJob(record, 0)
	s.attachBrief(ctx, job, input.Attachments)
	s.publish(job, "created")
	if parentID != "" {
		_ = s.Journal(ctx, parentID, EventChild, "Delegated "+job.ID+": "+job.Title, map[string]any{"child": job.ID})
	}
	if job.ProjectID != 0 && job.Dir == "" && s.opts.Projects != nil {
		s.opts.Projects.GivenJob(ctx, job.ProjectID) // the lead's start policy
	}
	s.goBackground(ctx, job.ID, func(ctx context.Context) { s.launch(ctx, job.ID) })
	return job, nil
}

// checkAttachments refuses a job whose attachments cannot be given to it, before it is recorded.
func (s *Service) checkAttachments(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if s.opts.Attachments == nil {
		return invalid("attachments are not available on this server")
	}
	if err := s.opts.Attachments.CheckBrief(ctx, ids); err != nil {
		return invalid("%v", err)
	}
	return nil
}

// attachBrief copies the job's files into its inbox before the launch writes the brief that
// lists them. A failure is on the job's timeline; the job still runs, with the files it got.
func (s *Service) attachBrief(ctx context.Context, job Job, ids []string) {
	if len(ids) == 0 || s.opts.Attachments == nil {
		return
	}
	if err := s.opts.Attachments.AttachBrief(ctx, job, ids); err != nil {
		s.opts.Logger.Warn("giving a job its attachments failed", "job", job.ID, "err", err)
		_ = s.Journal(ctx, job.ID, EventMessage, "Some attached files could not be given to the agent: "+oneLine(err.Error(), 200), nil)
		return
	}
	_ = s.Journal(ctx, job.ID, EventMessage, fmt.Sprintf("You attached %d file(s) to the brief", len(ids)), nil)
}

// insertLocked stores a new job and its created event (s.mu held), setting record.ID.
func (s *Service) insertLocked(ctx context.Context, record *store.JobRecord) error {
	id, err := s.opts.Store.InsertJob(ctx, *record)
	if err != nil {
		return err
	}
	record.ID = id
	var c change
	detail := map[string]any{"role": record.Role, "dir": record.Dir, "engine": record.Engine, "origin": record.Origin}
	if record.ParentID > 0 {
		detail["parent"] = FormatID(record.ParentID)
	}
	c.event(EventCreated, "Job created", detail)
	return s.save(ctx, *record, c)
}

// modelPattern is what a model name may look like (R2-I7): it becomes one argument of the agent's
// start command, which herdr types into a shell.
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]{1,64}$`)

func (s *Service) newRecord(input CreateInput, parentID string) (store.JobRecord, error) {
	brief := strings.TrimSpace(input.Brief)
	switch {
	case brief == "":
		return store.JobRecord{}, invalid("brief is required: say what needs doing")
	case len(brief) > MaxBriefBytes:
		return store.JobRecord{}, invalid("the brief is longer than %d bytes; put the detail in a file and point to it", MaxBriefBytes)
	}
	role := strings.TrimSpace(input.Role)
	if role == "" {
		role = defaultRole(input.Dir)
	}
	if !s.opts.Roles.Has(role) {
		return store.JobRecord{}, invalid("unknown role %q; known roles: %s", role, strings.Join(s.opts.Roles.Names(), ", "))
	}
	engine := strings.TrimSpace(input.Engine)
	if engine == "" && strings.TrimSpace(input.Dir) != "" {
		engine = s.defaultEngine()
	}
	if engine == "" {
		engine = EngineClaude
	}
	if engine != EngineClaude && engine != EngineCodex {
		return store.JobRecord{}, invalid("engine must be claude or codex")
	}
	dir, err := cleanDir(input.Dir)
	if err != nil {
		return store.JobRecord{}, err
	}
	if dir == "" && engine != EngineClaude {
		return store.JobRecord{}, invalid("a job without a directory goes to the orchestrator, which is Claude; pick a directory to use %s", engine)
	}
	model := strings.TrimSpace(input.Model)
	if model == "" && engine == EngineClaude && dir != "" {
		// A job with no directory goes to the orchestrator, which keeps its own model.
		model = s.defaultModel()
	}
	if model != "" && !modelPattern.MatchString(model) {
		return store.JobRecord{}, invalid("model %q is not a model name (letters, digits and . _ : [ ] -, at most 64)", termtext.Cap(termtext.Clean(model), 70))
	}
	isolate := role == "builder"
	if input.Isolate != nil {
		isolate = *input.Isolate
	}
	var parent int64
	if parentID != "" {
		if parent, err = ParseID(parentID); err != nil {
			return store.JobRecord{}, invalid("parent job: %v", err)
		}
	}
	return store.JobRecord{
		Title: titleOf(brief), BriefMD: brief, DoneWhen: strings.TrimSpace(input.DoneWhen), Role: role, Dir: dir,
		Isolate: isolate && dir != "", Engine: engine, Model: model, ParentID: parent, ProjectID: input.ProjectID,
		State: string(StateQueued), VerifiedJSON: "[]", NotVerifiedJSON: "[]", CreatedAt: s.opts.Now(),
	}, nil
}

// defaultRole: a job with a directory is building something; one without goes to the lead.
func defaultRole(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return "lead"
	}
	return "builder"
}

// cleanDir expands ~ and checks the directory exists. Empty stays empty (the orchestrator).
func cleanDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", invalid("cannot expand ~: %v", err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		return "", invalid("dir must be an absolute path (or start with ~/), got %q", dir)
	}
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", invalid("dir %s does not exist or is not a directory", dir)
	}
	return dir, nil
}

func titleOf(brief string) string {
	first, _, _ := strings.Cut(brief, "\n")
	first = strings.TrimLeft(strings.TrimSpace(first), "# ")
	return oneLine(first, maxTitleRunes)
}

// rememberDir puts dir first in the remembered list. It is stored resolved, at the moment the
// human gave or allowed it, and the orchestrator's shortcut compares against that stored form
// without resolving it again (R3-M2): a link swapped in later cannot widen it. A directory too
// wide to hand out (/, ~, or one that holds ~) is still listed for the job form, but grants the
// orchestrator nothing (safeRoot).
func (s *Service) rememberDir(ctx context.Context, dir string) {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	dirs := s.RememberedDirs(ctx)
	next := []string{dir}
	for _, existing := range dirs {
		if existing != dir && len(next) < maxRememberedDirs {
			next = append(next, existing)
		}
	}
	body, _ := json.Marshal(next)
	if err := s.opts.Store.SetKV(ctx, kvDirs, string(body)); err != nil {
		s.opts.Logger.Warn("remembering a job directory", "err", err)
	}
}

// RememberedDirs returns directories jobs were given, most recent first.
func (s *Service) RememberedDirs(ctx context.Context) []string {
	value, err := s.opts.Store.GetKV(ctx, kvDirs)
	if err != nil {
		return []string{}
	}
	dirs := []string{}
	_ = json.Unmarshal([]byte(value), &dirs)
	return dirs
}

// ---- The agent's calls ---------------------------------------------------------------------

// UpdateInput is job_update.
type UpdateInput struct {
	Progress string `json:"progress"`
	// State may be "blocked" (with Reason) or "running" (to come back from blocked).
	State  string `json:"state,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Waiting declares that nothing is left that the open questions do not block.
	Waiting *bool `json:"waiting,omitempty"`
}

// Update is job_update: a heartbeat with a "doing now" line, and optionally a declared state.
// It counts as progress (resetting the nudge budget) only when it changes something: a new
// progress line, a different state or blocked reason, or a different waiting value. Repeating
// what already holds is not progress (R2-C01): an agent answering every nudge with the same
// job_update must still run out of nudges.
func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (Job, error) {
	progress := oneLine(input.Progress, maxProgressRunes)
	if progress == "" && input.State == "" && input.Waiting == nil {
		return Job{}, invalid("progress is required: one line saying what you are doing now")
	}
	return s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return invalid("job %s is already %s; there is nothing to update", FormatID(r.ID), r.State)
		}
		if input.Waiting != nil && *input.Waiting && c.questions.Open == 0 {
			// Found by the stop test: an agent told to wait answered every nudge with
			// waiting=true, which reset the budget forever. Waiting needs a question.
			return invalid("you have no open questions, so there is nothing to wait on. If you need the human, call ask (it does not block) and then declare waiting; if you are finished, call done; if something else blocks you, set state blocked with the reason")
		}
		before := *r
		if progress != "" && progress != r.Progress {
			r.Progress = progress
			c.event(EventProgress, progress, nil)
		}
		if input.Waiting != nil && r.Waiting != *input.Waiting {
			r.Waiting = *input.Waiting
			if r.Waiting {
				c.event(EventProgress, "Declared: nothing left that the open questions do not block", nil)
			}
		}
		if err := applyDeclaredState(r, c, input); err != nil {
			return err
		}
		if r.Progress != before.Progress || r.Waiting != before.Waiting || r.State != before.State || r.BlockedReason != before.BlockedReason {
			s.markProgressLocked(r)
		} else {
			r.LastActivityAt = s.opts.Now() // a heartbeat, not progress
		}
		return nil
	})
}

func applyDeclaredState(r *store.JobRecord, c *change, input UpdateInput) error {
	switch strings.TrimSpace(input.State) {
	case "":
		return nil
	case string(StateBlocked):
		reason := oneLine(input.Reason, 500)
		if reason == "" {
			return invalid("state blocked needs a reason: what are you waiting on?")
		}
		r.State, r.BlockedReason = string(StateBlocked), reason
		c.event(EventProgress, "Blocked: "+reason, nil)
	case string(StateRunning):
		if State(r.State) == StateBlocked || (State(r.State) == StateNeedsYou && Cause(r.Cause) != CauseQuestions) {
			r.State, r.BlockedReason = string(StateRunning), ""
			r.Cause, r.CauseDetail = "", ""
		}
	default:
		return invalid("state may be blocked or running; to finish call done, to give up call fail")
	}
	return nil
}

// markProgressLocked records that the agent did something: it resets the nudge streak and
// clears the stuck flags that progress disproves.
func (s *Service) markProgressLocked(r *store.JobRecord) {
	now := s.opts.Now()
	r.LastProgressAt, r.LastActivityAt = now, now
	r.NudgeStreak = 0
	if State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseDialog {
		// Work after a dialog means someone answered it.
		r.State, r.Cause, r.CauseDetail = string(StateRunning), "", ""
	}
	if r.Stuck {
		switch Cause(r.Cause) {
		case CauseUnresponsive, CauseLimit, CauseServerError, CauseSilent, CauseIdle, CauseExited:
			r.Stuck, r.Cause, r.CauseDetail = false, "", ""
			r.RetryAt, r.RetryCount = zeroTime, 0
		}
	}
}

// DoneInput is done.
type DoneInput struct {
	ResultMD    string   `json:"result_md"`
	Verified    []string `json:"verified"`
	NotVerified []string `json:"not_verified"`
	// Attachments are files the agent attaches to its result (absolute paths inside its working
	// or job directory). The agent socket imports them; Done itself never reads a path.
	Attachments []string `json:"attachments,omitempty"`
}

// Done declares the job finished with its evidence.
func (s *Service) Done(ctx context.Context, id string, input DoneInput) (Job, error) {
	result := strings.TrimSpace(input.ResultMD)
	switch {
	case result == "":
		return Job{}, invalid("result_md is required: what was done, in Markdown")
	case len(result) > maxResultBytes:
		return Job{}, invalid("result_md is longer than %d bytes; summarise and point to files", maxResultBytes)
	case input.Verified == nil || input.NotVerified == nil:
		return Job{}, invalid("verified and not_verified are both required (use an empty list for none): say what you checked and what you did not")
	}
	return s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return invalid("job %s is already %s", FormatID(r.ID), r.State)
		}
		s.markProgressLocked(r)
		r.State, r.ResultMD = string(StateDone), result
		r.VerifiedJSON, r.NotVerifiedJSON = encodeList(input.Verified), encodeList(input.NotVerified)
		r.Stuck, r.Cause, r.CauseDetail, r.Waiting = false, "", "", false
		c.event(EventDone, "Done: "+oneLine(result, 160), map[string]any{"verified": len(decodeList(r.VerifiedJSON)), "not_verified": len(decodeList(r.NotVerifiedJSON))})
		return nil
	})
}

// Fail declares the job cannot be finished.
func (s *Service) Fail(ctx context.Context, id, reason string) (Job, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Job{}, invalid("reason is required: why can this not be finished?")
	}
	if utf8.RuneCountInString(reason) > 4000 {
		reason = string([]rune(reason)[:4000])
	}
	return s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return invalid("job %s is already %s", FormatID(r.ID), r.State)
		}
		r.State, r.FailReason = string(StateFailed), reason
		r.Stuck, r.Cause, r.CauseDetail = false, "", ""
		c.event(EventFail, "Failed: "+oneLine(reason, 160), nil)
		c.notify = fmt.Sprintf("crowsnest: job %s failed: %s", FormatID(r.ID), oneLine(r.Title, 80))
		return nil
	})
}

// RecordToolUse is the PostToolUse hook: progress (the nudge budget resets) and the "doing now"
// fallback. crowsnest's own bookkeeping tools (job_update, ask, job_get… through MCP or the CLI)
// are only a heartbeat: they decide for themselves whether they changed anything, and counting
// the tool call as well would let a no-op job_update reset the budget after all (R2-C01).
func (s *Service) RecordToolUse(ctx context.Context, id, toolName string, toolInput json.RawMessage) (Job, error) {
	bookkeeping := isBookkeepingTool(toolName, toolInput)
	summary := toolSummary(toolName, toolInput)
	return s.mutate(ctx, id, func(r *store.JobRecord, _ *change) error {
		if State(r.State).Terminal() {
			return nil
		}
		if bookkeeping {
			r.LastActivityAt = s.opts.Now()
			return nil
		}
		s.markProgressLocked(r)
		r.Denials = 0
		if summary != "" {
			r.LastTool = oneLine(summary, maxProgressRunes)
		}
		return nil
	})
}

// isBookkeepingTool reports whether a tool call is one of crowsnest's own: an MCP tool of the
// crowsnest server, or a shell command that runs the crowsnest CLI.
func isBookkeepingTool(name string, input json.RawMessage) bool {
	if strings.HasPrefix(name, "mcp__crowsnest__") {
		return true
	}
	if name != "Bash" {
		return false
	}
	var fields struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &fields) != nil {
		return false
	}
	command := strings.TrimSpace(fields.Command)
	if strings.ContainsAny(command, ";&|\n`$(") {
		return false // more than one command: something else ran too
	}
	first, _, _ := strings.Cut(command, " ")
	return filepath.Base(first) == "crowsnest"
}

// GetForAgent is job_get: the job, its questions and their answers, as text an agent reads after
// a compaction or restart.
func (s *Service) GetForAgent(ctx context.Context, id string) (string, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return ContextBlock(detail) + SpendBlock(detail.Spend), nil
}

// SpendBlock is the job's spend as job_get shows it: so an agent can see what it has used.
// Empty when the server measures nothing.
func SpendBlock(summary *spend.Summary) string {
	if summary == nil {
		return ""
	}
	return "\n## Spend so far\n\n" + spend.Describe(*summary) + "\n"
}

// ContextBlock renders a job for an agent: the SessionStart re-injection and job_get.
func ContextBlock(d Detail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[crowsnest job %s] You hold this job. State: %s.\n\n", d.ID, d.State)
	// briefText frames a brief another agent wrote and disarms its bracketed lines (R2-M1).
	brief := strings.TrimPrefix(briefText(d.Job), "# Job "+d.ID+": brief\n\n")
	fmt.Fprintf(&b, "## Brief\n\n%s\n", brief)
	if d.Progress != "" {
		fmt.Fprintf(&b, "Your last progress line: %s\n", d.Progress)
	}
	if d.BlockedReason != "" && d.State == StateBlocked {
		fmt.Fprintf(&b, "You declared it blocked: %s\n", d.BlockedReason)
	}
	if len(d.Questions) > 0 {
		b.WriteString("\n## Your questions\n\n")
		for _, q := range d.Questions {
			fmt.Fprintf(&b, "- %s (%s) %s", q.ID, q.Status, q.Title)
			if q.Status == "answered" {
				fmt.Fprintf(&b, " → %v", q.AnswerValue)
				if q.AnswerNote != "" {
					fmt.Fprintf(&b, " (note: %s)", q.AnswerNote)
				}
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nKeep working on it. When finished call done (result_md, verified, not_verified); " +
		"if you need the human call ask and keep going; if stuck on something else call job_update with state blocked.\n")
	return b.String()
}
