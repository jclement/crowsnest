// Landing a finished job (DESIGN.md 6.11): the Review tab's data, and the three ways a job's
// branch leaves the board: merged into its base locally, pushed with a pull request, or
// discarded. Then the teardown, in one step and in this order: stop the job's agent (and any
// reviewer still running), remove the worktree through herdr (worktree.remove, which refuses a
// dirty tree: the refusal is shown, never forced), close its workspace, delete the branch when it
// was merged or discarded, revoke the job's tokens and remove its state directory. Every step is
// on the job's timeline.
//
// Only the human lands. An agent (a project's lead, the orchestrator) may only *request* a land
// action (RequestLand): that becomes a question for the human, and only their Allow runs it.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// Land actions.
const (
	LandMerge   = "merge"
	LandPR      = "pr"
	LandDiscard = "discard"
)

// Land states recorded on a job.
const (
	LandedMerged    = "merged"
	LandedPR        = "pr"
	LandedDiscarded = "discarded"
)

// ValidLandAction reports whether action is merge, pr or discard.
func ValidLandAction(action string) bool {
	return action == LandMerge || action == LandPR || action == LandDiscard
}

// ReviewRound is one reviewer job as the Review tab lists it.
type ReviewRound struct {
	JobID    string `json:"job_id"`
	Round    int    `json:"round"`
	Engine   string `json:"engine"`
	State    State  `json:"state"`
	Verdict  string `json:"verdict"` // "" while it runs
	Findings string `json:"findings"`
}

// ReviewView is the Review tab: what the job produced, in reading order, with the agent's own
// account beside it, the review rounds, and which land actions are open and why not.
type ReviewView struct {
	JobID       string            `json:"job_id"`
	Produced    *landing.Produced `json:"produced"`
	InspectErr  string            `json:"inspect_error,omitempty"`
	ResultMD    string            `json:"result_md"`
	Walkthrough string            `json:"walkthrough_md"`
	Verified    []string          `json:"verified"`
	NotVerified []string          `json:"not_verified"`
	Reviews     []ReviewRound     `json:"reviews"`
	Land        LandOptions       `json:"land"`
}

// LandOptions says what the human may do now, with a sentence for each action that is closed.
type LandOptions struct {
	State     string            `json:"state"` // land_state
	Detail    string            `json:"detail"`
	TornDown  bool              `json:"torn_down"`
	Base      string            `json:"base"`
	Branch    string            `json:"branch"`
	MergeMode string            `json:"merge_mode"`
	Protected bool              `json:"base_protected"`
	Allowed   map[string]bool   `json:"allowed"`
	Why       map[string]string `json:"why,omitempty"` // action → why it is not allowed
	Confirm   map[string]string `json:"confirm"`       // action → exactly what it will do
	Request   string            `json:"request,omitempty"`
}

// ReviewOf builds the Review tab for a job.
func (s *Service) ReviewOf(ctx context.Context, id string) (ReviewView, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return ReviewView{}, err
	}
	job := detail.Job
	view := ReviewView{JobID: job.ID, ResultMD: job.ResultMD, Walkthrough: job.WalkthroughMD, Verified: job.Verified,
		NotVerified: job.NotVerified, Reviews: []ReviewRound{}}
	if job.WorktreeBranch != "" && job.TornDownAt == nil && job.WorkDir != "" {
		record, _ := s.opts.Store.GetJob(ctx, mustParse(job.ID))
		produced, err := landing.Inspect(ctx, job.WorkDir, job.BaseBranch, record.BaseCommit)
		if err != nil {
			view.InspectErr = oneLine(err.Error(), 300)
		} else {
			produced.Files = landing.Order(produced.Files, job.WalkthroughMD)
			view.Produced = &produced
		}
	}
	reviewers, err := s.opts.Store.ListReviewJobs(ctx, mustParse(job.ID))
	if err != nil {
		return ReviewView{}, err
	}
	for i, record := range reviewers {
		round := ReviewRound{JobID: FormatID(record.ID), Round: i + 1, Engine: record.Engine, State: State(record.State), Findings: record.ResultMD}
		switch State(record.State) {
		case StateDone:
			round.Verdict = ParseVerdict(record.ResultMD)
		case StateFailed, StateCancelled:
			round.Verdict, round.Findings = VerdictError, firstNonEmptyDir(record.FailReason, "cancelled")
		}
		view.Reviews = append(view.Reviews, round)
	}
	if len(view.Reviews) == 0 && job.ReviewMD != "" {
		// The verdict is on the job even when its reviewer rows are not (seeded, or imported).
		view.Reviews = append(view.Reviews, ReviewRound{Round: max(job.ReviewRound, 1), Verdict: job.ReviewVerdict, Findings: job.ReviewMD, State: StateDone})
	}
	view.Land = s.landOptions(job, view.Produced)
	return view, nil
}

// landOptions decides which actions are open for job.
func (s *Service) landOptions(job Job, produced *landing.Produced) LandOptions {
	opts := LandOptions{State: job.LandState, Detail: job.LandDetail, TornDown: job.TornDownAt != nil, Base: job.BaseBranch,
		Branch: job.WorktreeBranch, MergeMode: firstNonEmptyDir(s.landing().MergeMode, landing.ModeMerge),
		Protected: slices.Contains(s.landing().Protected, job.BaseBranch), Request: job.LandRequest,
		Allowed: map[string]bool{}, Why: map[string]string{}, Confirm: map[string]string{}}
	for _, action := range []string{LandMerge, LandPR, LandDiscard} {
		if why := s.landBlocked(job, action, produced); why != "" {
			opts.Why[action] = why
		} else {
			opts.Allowed[action] = true
		}
		opts.Confirm[action] = confirmText(job, action, opts.MergeMode)
	}
	return opts
}

// landBlocked is why action cannot run on job now ("" when it can).
func (s *Service) landBlocked(job Job, action string, produced *landing.Produced) string {
	switch {
	case job.WorktreeBranch == "":
		return "the job did not run in a worktree of its own, so there is nothing to land"
	case job.TornDownAt != nil:
		return "the job's worktree is already gone"
	case job.LandState != "":
		return "the job was already " + landWord(job.LandState) + "; its teardown can be retried from the sweep"
	case job.ReviewOf != "":
		return "this is a reviewer job; land the job it reviews"
	}
	landable := job.State == StateDone || (job.State == StateNeedsYou && job.Cause == CauseReview)
	if action == LandDiscard {
		if !landable && !job.State.Terminal() {
			return "the job is still " + string(job.State) + "; cancel it first, or wait for it to finish"
		}
		return ""
	}
	if !landable {
		return "only a finished job is landed (it is " + string(job.State) + ")"
	}
	if action == LandMerge {
		if job.BaseBranch == "" {
			return "crowsnest does not know the branch this job started from; open a pull request instead"
		}
		if slices.Contains(s.landing().Protected, job.BaseBranch) {
			return job.BaseBranch + " is a protected branch: Land never merges into it. Open a pull request instead"
		}
		if produced != nil && produced.Ahead == 0 {
			return "the branch has no commits ahead of " + job.BaseBranch + ": nothing to merge (discard it)"
		}
	}
	if action == LandPR && slices.Contains(s.landing().Protected, job.WorktreeBranch) {
		return job.WorktreeBranch + " is a protected branch: Land never pushes to it"
	}
	return ""
}

// confirmText says exactly what an action will do, for the confirmation.
func confirmText(job Job, action, mode string) string {
	teardown := "Then stop " + firstNonEmptyDir(job.AgentName, "the agent") + ", remove the worktree " + firstNonEmptyDir(job.WorkDir, "") +
		" and its workspace"
	switch action {
	case LandMerge:
		how := "with a merge commit"
		if mode == landing.ModeFF {
			how = "as a fast-forward only"
		}
		return fmt.Sprintf("Merge %s into %s %s, in the main checkout %s. Refused, with nothing changed, if that checkout has uncommitted changes or the merge conflicts. %s, and delete the branch %s.",
			job.WorktreeBranch, firstNonEmptyDir(job.BaseBranch, "its base"), how, job.Dir, teardown, job.WorktreeBranch)
	case LandPR:
		return fmt.Sprintf("Push %s to its remote and open a pull request into %s (GitHub with gh; elsewhere it is pushed only). No reviewers are assigned. %s; the branch is kept for the pull request.",
			job.WorktreeBranch, firstNonEmptyDir(job.BaseBranch, "the default branch"), teardown)
	default:
		return fmt.Sprintf("Throw the work away: %s, and delete the branch %s with its commits. Refused if the worktree has uncommitted changes. This cannot be undone.",
			strings.TrimPrefix(teardown, "Then s"), job.WorktreeBranch)
	}
}

func landWord(state string) string {
	switch state {
	case LandedMerged:
		return "merged"
	case LandedPR:
		return "pushed with a pull request"
	case LandedDiscarded:
		return "discarded"
	}
	return state
}

// FileDiffView is one file's diff for the Review tab, fetched only when its row is opened.
type FileDiffView struct {
	Path   string `json:"path"`
	Diff   string `json:"diff"`
	Binary bool   `json:"binary"`
	Cut    bool   `json:"cut"`
}

// FileDiff returns one changed file's diff. Only files the branch changed are served.
func (s *Service) FileDiff(ctx context.Context, id, path string) (FileDiffView, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return FileDiffView{}, err
	}
	job := detail.Job
	if job.WorktreeBranch == "" || job.TornDownAt != nil || job.WorkDir == "" {
		return FileDiffView{}, invalid("job %s has no worktree to read a diff from", job.ID)
	}
	record, _ := s.opts.Store.GetJob(ctx, mustParse(job.ID))
	produced, err := landing.Inspect(ctx, job.WorkDir, job.BaseBranch, record.BaseCommit)
	if err != nil {
		return FileDiffView{}, invalid("the worktree cannot be read: %v", err)
	}
	for _, file := range produced.Files {
		if file.Path == path {
			diff, binary, cut, err := landing.FileDiff(ctx, job.WorkDir, produced.DiffBase, file.Path, file.OldPath)
			if err != nil {
				return FileDiffView{}, err
			}
			return FileDiffView{Path: path, Diff: diff, Binary: binary || file.Binary, Cut: cut}, nil
		}
	}
	return FileDiffView{}, invalid("%s is not one of the files this job changed", path)
}

// ---- Land ----------------------------------------------------------------------------------

// LandResult is what a land action did.
type LandResult struct {
	Job      Job    `json:"job"`
	Action   string `json:"action"`
	Detail   string `json:"detail"`
	TornDown bool   `json:"torn_down"`
	// TeardownError says what of the teardown did not happen (a dirty worktree herdr would not
	// remove), when the land itself succeeded. The sweep lists the job until it is retried.
	TeardownError string `json:"teardown_error,omitempty"`
}

// Land runs a land action for the human (by names them on the timeline: "you", or "you, as
// lead-x asked"). A refusal changes nothing and comes back as a ValidationError.
func (s *Service) Land(ctx context.Context, id, action, by string) (LandResult, error) {
	if !ValidLandAction(action) {
		return LandResult{}, invalid("unknown land action %q: give merge, pr or discard", action)
	}
	detail, err := s.Get(ctx, id)
	if err != nil {
		return LandResult{}, err
	}
	job := detail.Job
	if why := s.landBlocked(job, action, nil); why != "" {
		return LandResult{}, invalid("%s", why)
	}
	if action == LandDiscard {
		return s.discard(ctx, job, by)
	}
	detailText, err := s.landRemote(ctx, job, action)
	if err != nil {
		if landing.IsRefusal(err) {
			_ = s.Journal(ctx, job.ID, EventLand, "Land ("+action+") refused: "+oneLine(err.Error(), 240), nil)
			return LandResult{}, invalid("%s", err.Error())
		}
		return LandResult{}, err
	}
	state := LandedMerged
	if action == LandPR {
		state = LandedPR
	}
	landed, err := s.recordLanded(ctx, job.ID, state, detailText, by)
	if err != nil {
		return LandResult{}, err
	}
	result := LandResult{Job: landed, Action: action, Detail: detailText}
	result.TornDown, result.TeardownError = s.teardown(ctx, landed, action == LandMerge, false)
	result.Job = s.jobNow(ctx, job.ID, landed)
	return result, nil
}

// landRemote merges or pushes; it returns the sentence recorded as land_detail.
func (s *Service) landRemote(ctx context.Context, job Job, action string) (string, error) {
	mainDir, err := landing.MainCheckout(ctx, job.Dir)
	if err != nil {
		return "", invalid("the repository's main checkout cannot be found: %v", err)
	}
	if action == LandMerge {
		result, err := landing.Merge(ctx, landing.MergeInput{MainDir: mainDir, WorkDir: job.WorkDir, Branch: job.WorktreeBranch, Base: job.BaseBranch,
			Mode: s.landing().MergeMode, Message: fmt.Sprintf("Merge job %s: %s", job.ID, job.Title), Protected: s.landing().Protected})
		if err != nil {
			return "", err
		}
		how := "merge commit"
		if result.FastForward {
			how = "fast-forward"
		}
		return fmt.Sprintf("Merged into %s (%s, %s)", job.BaseBranch, how, shortHash(result.Commit)), nil
	}
	push, err := landing.Push(ctx, landing.PushInput{WorkDir: job.WorkDir, Branch: job.WorktreeBranch, Base: job.BaseBranch,
		Title: job.Title, Body: prBody(job), Protected: s.landing().Protected, GH: s.landing().GH})
	if err != nil {
		return "", err
	}
	if push.PRURL != "" {
		return push.PRURL, nil
	}
	return "Pushed " + job.WorktreeBranch + " to " + push.Remote + ": " + push.Note, nil
}

func shortHash(hash string) string {
	if len(hash) > 10 {
		return hash[:10]
	}
	return hash
}

// prBody is the pull request's description: the result, the walkthrough and the review.
func prBody(job Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", job.ResultMD)
	if len(job.Verified) > 0 || len(job.NotVerified) > 0 {
		b.WriteString("## Verified\n\n")
		for _, item := range job.Verified {
			b.WriteString("- " + item + "\n")
		}
		b.WriteString("\n## Not verified\n\n")
		for _, item := range job.NotVerified {
			b.WriteString("- " + item + "\n")
		}
		b.WriteString("\n")
	}
	if job.WalkthroughMD != "" {
		b.WriteString("## Walkthrough\n\n" + job.WalkthroughMD + "\n\n")
	}
	if job.ReviewMD != "" {
		fmt.Fprintf(&b, "## Review (round %d, %s)\n\n%s\n\n", job.ReviewRound, firstNonEmptyDir(job.ReviewVerdict, "pending"), job.ReviewMD)
	}
	fmt.Fprintf(&b, "_Crowsnest job %s._\n", job.ID)
	return b.String()
}

// discard tears the job down first (a dirty worktree refuses, and nothing is recorded), then
// records it and deletes the branch.
func (s *Service) discard(ctx context.Context, job Job, by string) (LandResult, error) {
	if job.WorkDir != "" {
		if produced, err := landing.Inspect(ctx, job.WorkDir, job.BaseBranch, ""); err == nil && !produced.Clean {
			return LandResult{}, invalid("the job's worktree has uncommitted changes (%s); herdr will not remove a dirty worktree and crowsnest never forces it. Clean it in the terminal first",
				strings.Join(produced.Dirty[:min(3, len(produced.Dirty))], "; "))
		}
	}
	landed, err := s.recordLanded(ctx, job.ID, LandedDiscarded, "Discarded", by)
	if err != nil {
		return LandResult{}, err
	}
	result := LandResult{Job: landed, Action: LandDiscard, Detail: "Discarded"}
	result.TornDown, result.TeardownError = s.teardown(ctx, landed, false, true)
	result.Job = s.jobNow(ctx, job.ID, landed)
	return result, nil
}

// recordLanded marks the job landed (and done, when it was waiting on the human after its
// review, or cancelled when a live job is discarded).
func (s *Service) recordLanded(ctx context.Context, id, state, detail, by string) (Job, error) {
	return s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if r.LandState != "" {
			return invalid("job %s was already %s", FormatID(r.ID), landWord(r.LandState))
		}
		if !State(r.State).Terminal() {
			waitingAfterReview := State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseReview
			if state == LandedDiscarded && !waitingAfterReview {
				r.State = string(StateCancelled)
			} else {
				r.State = string(StateDone)
				r.Cause, r.CauseDetail = "", ""
			}
		}
		r.LandState, r.LandDetail, r.LandedAt, r.LandRequest = state, oneLine(detail, 500), s.opts.Now(), ""
		c.event(EventLand, landWord(state)+" by "+firstNonEmptyDir(by, "you")+": "+oneLine(detail, 200), map[string]any{"state": state})
		return nil
	})
}

// teardown stops the job's agent and any live reviewer, removes the worktree and workspace,
// deletes the branch (merged: only if git agrees it is merged; discarded: forced), revokes the
// job's tokens and removes its state directory. It reports whether the worktree is gone, and what
// went wrong otherwise; the steps that could run, ran, and each is on the timeline.
func (s *Service) teardown(ctx context.Context, job Job, deleteMerged, deleteUnmerged bool) (bool, string) {
	if s.opts.Herdr == nil {
		return false, "herdr is not connected"
	}
	s.stopReviewers(ctx, job.ID)
	if job.PaneID != "" {
		if err := s.stopAgent(ctx, job.PaneID, job.AgentName); err != nil {
			_ = s.Journal(ctx, job.ID, EventLand, "Teardown: the agent was not stopped: "+oneLine(err.Error(), 200), nil)
		} else {
			_ = s.Journal(ctx, job.ID, EventLand, "Teardown: stopped "+job.AgentName, nil)
		}
	}
	workspace := s.workspaceOf(ctx, job)
	if workspace == "" {
		return false, s.teardownFailed(ctx, job.ID, "the job's herdr workspace cannot be found, so the worktree was not removed; remove it by hand")
	}
	removeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	_, err := s.opts.Herdr.WorktreeRemove(removeCtx, herdr.WorktreeRemoveParams{WorkspaceID: workspace})
	cancel()
	if err != nil {
		return false, s.teardownFailed(ctx, job.ID, "herdr did not remove the worktree: "+oneLine(err.Error(), 240)+
			" (a dirty worktree is never forced: commit or clean it in its terminal, then use Tear down again)")
	}
	_ = s.Journal(ctx, job.ID, EventLand, "Teardown: removed the worktree "+job.WorkDir, nil)
	// worktree.remove closes the workspace; close it anyway in case herdr left it open.
	_ = s.opts.Herdr.WorkspaceClose(ctx, workspace)
	s.deleteBranch(ctx, job, deleteMerged, deleteUnmerged)
	s.releaseFiles(ctx, job)
	_, _ = s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
		r.TornDownAt = s.opts.Now()
		c.event(EventLand, "Teardown finished: agent, worktree and workspace are gone", nil)
		return nil
	})
	return true, ""
}

func (s *Service) teardownFailed(ctx context.Context, id, why string) string {
	_ = s.Journal(ctx, id, EventLand, "Teardown stopped: "+why, nil)
	return why
}

// workspaceOf is the job's recorded workspace, else the one its pane is in.
func (s *Service) workspaceOf(ctx context.Context, job Job) string {
	record, err := s.opts.Store.GetJob(ctx, mustParse(job.ID))
	if err == nil && record.WorkspaceID != "" {
		return record.WorkspaceID
	}
	if job.PaneID == "" {
		return ""
	}
	pane, err := s.opts.Herdr.PaneGet(ctx, job.PaneID)
	if err != nil {
		return ""
	}
	return pane.WorkspaceID
}

func (s *Service) deleteBranch(ctx context.Context, job Job, merged, unmerged bool) {
	if !merged && !unmerged {
		return
	}
	mainDir, err := landing.MainCheckout(ctx, job.Dir)
	if err == nil {
		err = landing.DeleteBranch(ctx, mainDir, job.WorktreeBranch, unmerged)
	}
	if err != nil {
		_ = s.Journal(ctx, job.ID, EventLand, "Teardown: the branch was kept: "+oneLine(err.Error(), 200), nil)
		return
	}
	_ = s.Journal(ctx, job.ID, EventLand, "Teardown: deleted the branch "+job.WorktreeBranch, nil)
}

// releaseFiles revokes the job's tokens and removes its state directory (brief, prompt,
// settings, review findings).
func (s *Service) releaseFiles(ctx context.Context, job Job) {
	if record, err := s.opts.Store.GetJob(ctx, mustParse(job.ID)); err == nil {
		s.mu.Lock()
		s.revokeTokensLocked(ctx, record)
		s.mu.Unlock()
	}
	if s.opts.Launch.StateDir == "" {
		return
	}
	dir, err := s.jobDir(job.ID)
	if err == nil {
		_ = os.RemoveAll(dir)
	}
}

// stopReviewers cancels the job's reviewers still running (a landed job needs no review).
func (s *Service) stopReviewers(ctx context.Context, id string) {
	reviewers, err := s.opts.Store.ListReviewJobs(ctx, mustParse(id))
	if err != nil {
		return
	}
	for _, record := range reviewers {
		if State(record.State).Terminal() {
			continue
		}
		reviewer := FormatID(record.ID)
		if _, err := s.Cancel(ctx, reviewer); err == nil && record.PaneID != "" {
			_ = s.stopAgent(ctx, record.PaneID, record.AgentName)
		}
	}
}

// Teardown retries the teardown of a landed job whose worktree is still around (the sweep), or
// tears down a finished job whose branch should simply go: discarded by default.
func (s *Service) Teardown(ctx context.Context, id string) (LandResult, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return LandResult{}, err
	}
	job := detail.Job
	if job.WorktreeBranch == "" || job.TornDownAt != nil {
		return LandResult{}, invalid("job %s has no worktree left to tear down", job.ID)
	}
	if job.LandState == "" {
		return s.Land(ctx, id, LandDiscard, "you")
	}
	result := LandResult{Job: job, Action: "teardown", Detail: job.LandDetail}
	result.TornDown, result.TeardownError = s.teardown(ctx, job, job.LandState == LandedMerged, job.LandState == LandedDiscarded)
	result.Job = s.jobNow(ctx, id, job)
	return result, nil
}

// ---- The sweep -----------------------------------------------------------------------------

// SweepItem is a finished job whose worktree is still around.
type SweepItem struct {
	Job   Job           `json:"job"`
	Age   time.Duration `json:"-"`
	AgeS  int64         `json:"age_seconds"` // since it settled
	Dirty bool          `json:"dirty"`
}

// Sweep lists finished jobs that still have a worktree, oldest first.
func (s *Service) Sweep(ctx context.Context) ([]SweepItem, error) {
	records, err := s.opts.Store.ListUnlandedWorktrees(ctx, listLimit)
	if err != nil {
		return nil, err
	}
	now := s.opts.Now()
	out := make([]SweepItem, 0, len(records))
	for _, record := range records {
		job := s.toJob(record, 0)
		settled := record.SettledAt
		if settled.IsZero() {
			settled = record.CreatedAt
		}
		item := SweepItem{Job: job, Age: now.Sub(settled), AgeS: int64(now.Sub(settled).Seconds())}
		if job.WorkDir != "" {
			if produced, err := landing.Inspect(ctx, job.WorkDir, job.BaseBranch, record.BaseCommit); err == nil {
				item.Dirty = !produced.Clean
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// ---- A lead's request ----------------------------------------------------------------------

// ErrLandRequested is returned to an agent: its request is with the human.
var ErrLandRequested = errors.New("requested")

// RequestLand turns an agent's land request into a question for the human. Only their Allow runs
// it (LandRequestSettled); the agent gets the outcome typed back like any answer.
func (s *Service) RequestLand(ctx context.Context, id, action string, caller questions.Caller, reason string) (questions.Question, error) {
	if !ValidLandAction(action) {
		return questions.Question{}, invalid("unknown land action %q: give merge, pr or discard", action)
	}
	if s.opts.AskHuman == nil {
		return questions.Question{}, invalid("land requests are not available on this server")
	}
	detail, err := s.Get(ctx, id)
	if err != nil {
		return questions.Question{}, err
	}
	job := detail.Job
	if why := s.landBlocked(job, action, nil); why != "" {
		return questions.Question{}, invalid("%s", why)
	}
	if job.LandRequest != "" {
		return questions.Question{}, invalid("a land request for job %s is already waiting for the human", job.ID)
	}
	body := fmt.Sprintf("An agent asks to **%s** job %s, %q.\n\n%s\n\n", landVerb(action), job.ID, job.Title,
		confirmText(job, action, firstNonEmptyDir(s.landing().MergeMode, landing.ModeMerge)))
	if reason = strings.TrimSpace(reason); reason != "" {
		body += "Its reason (written by the agent, not crowsnest):\n\n" + indent(oneLine(reason, 600)) + "\n\n"
	}
	body += "Open the job's Review tab to see the change before you allow it."
	question, err := s.opts.AskHuman(ctx, questions.AskInput{
		Title: fmt.Sprintf("Land job %s: %s?", job.ID, landVerb(action)), BodyMD: body, Kind: "confirm", Urgency: "normal",
		Options: []questions.Option{{Key: "allow", Label: "Allow: " + landVerb(action)}, {Key: "deny", Label: "Deny"}},
		Context: map[string]string{"land_for": job.ID, "land_action": action},
	}, caller)
	if err != nil {
		return questions.Question{}, err
	}
	qid, _ := strings.CutPrefix(question.ID, "q_")
	numeric, _ := parseInt64(qid)
	_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		r.LandRequest, r.LandQuestionID = action, numeric
		c.event(EventLand, "An agent asked to "+landVerb(action)+" it; waiting for you ("+question.ID+")", map[string]any{"question": question.ID})
		return nil
	})
	return question, nil
}

func landVerb(action string) string {
	switch action {
	case LandMerge:
		return "merge it"
	case LandPR:
		return "push it and open a pull request"
	default:
		return "discard it"
	}
}

// LandRequestSettled applies the human's answer to a land request: Allow (with no note: a note
// is a condition crowsnest cannot follow, as for delegation) runs it; anything else drops it.
func (s *Service) LandRequestSettled(ctx context.Context, jobID string, question questions.Question) {
	detail, err := s.Get(ctx, jobID)
	if err != nil || detail.LandRequest == "" {
		return
	}
	action := detail.LandRequest
	allowed := question.Status == "answered" && answerKey(question.AnswerValue) == "allow" && strings.TrimSpace(question.AnswerNote) == ""
	_, _ = s.mutate(ctx, jobID, func(r *store.JobRecord, c *change) error {
		r.LandRequest, r.LandQuestionID = "", 0
		if !allowed {
			c.event(EventLand, "You did not allow the request to "+landVerb(action), nil)
		}
		return nil
	})
	if !allowed {
		return
	}
	if _, err := s.Land(ctx, jobID, action, "you, as an agent asked"); err != nil {
		_ = s.Journal(ctx, jobID, EventLand, "The land you allowed was refused: "+oneLine(err.Error(), 240), nil)
	}
}

func answerKey(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		if len(v) == 1 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

func parseInt64(text string) (int64, error) {
	var n int64
	_, err := fmt.Sscan(text, &n)
	return n, err
}
