// The review gate (DESIGN.md 6.7, as built): a builder's work in a worktree is looked at by a
// second agent before it counts as done.
//
// When a job in a worktree calls done and a reviewer applies (its own `reviewer`, else the
// server's --review default for builder jobs: the other engine when installed, else the same
// engine in the reviewer role; "off" turns it off), the job becomes in_review and crowsnest
// creates a reviewer job: an ordinary supervised job (its own pane, conversation and timeline,
// never a hidden subagent), role reviewer, in the same worktree, told to read only and to report
// its findings with a verdict line. When the reviewer settles:
//
//   - VERDICT: approve → the job is done, the review attached;
//   - VERDICT: changes, round 1 → the job goes back to running and the findings are typed to
//     the builder as a message; its next done starts round 2;
//   - changes after round 2, or a reviewer that failed or was cancelled → needs_you, cause
//     review: the human decides (land anyway, message the builder, or discard).
//
// The verdict is parsed from the first `VERDICT:` line of the reviewer's result; a result with
// none counts as changes, so a reviewer that forgot the line does not wave work through.

package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/store"
)

const (
	// maxReviewRounds is how many reviews a job gets before the human decides.
	maxReviewRounds = 2
	// ReviewerOff turns the gate off for a job.
	ReviewerOff = "off"
)

// Review verdicts.
const (
	VerdictApprove = "approve"
	VerdictChanges = "changes"
	VerdictError   = "error"
)

// Review defaults (config.Review*), mirrored here so the jobs package does not import config.
const (
	ReviewDefaultAuto = "auto"
	ReviewDefaultOff  = "off"
	ReviewDefaultSame = "same"
)

// LandingOptions configure the review gate and Land.
type LandingOptions struct {
	// ReviewDefault is --review: auto, off, same, claude or codex. Empty means off.
	ReviewDefault string
	// CodexInstalled says whether `codex` is on PATH (auto picks it for a Claude builder).
	CodexInstalled bool
	// MergeMode is --merge: merge or ff.
	MergeMode string
	// Protected are the guard's protected branches: Land never merges into or pushes to them.
	Protected []string
	// GH is the gh binary for pull requests ("" looks it up on PATH).
	GH string
}

var verdictLine = regexp.MustCompile(`(?im)^\W*verdict\W*:?\s*(approve[sd]?|lgtm|changes(?:\s+requested)?|request(?:ed)?\s+changes|object(?:s|ion)?|reject(?:ed)?)\b`)

// ParseVerdict reads a reviewer's result: approve or changes (changes when no verdict is given).
func ParseVerdict(result string) string {
	match := verdictLine.FindStringSubmatch(result)
	if match == nil {
		return VerdictChanges
	}
	word := strings.ToLower(match[1])
	if strings.HasPrefix(word, "approve") || word == "lgtm" {
		return VerdictApprove
	}
	return VerdictChanges
}

// reviewerFor is the engine that reviews r's work now, or "" for no review. Called under the
// lock from Done.
func (s *Service) reviewerFor(r store.JobRecord) string {
	if r.ReviewOf != 0 || r.WorktreeBranch == "" || r.ReviewRound >= maxReviewRounds || r.WorkDir == "" {
		return ""
	}
	return s.reviewEngine(r)
}

// reviewEngine is the engine r's reviews use, whatever the round ("" for none).
func (s *Service) reviewEngine(r store.JobRecord) string {
	switch r.Reviewer {
	case ReviewerOff:
		return ""
	case EngineClaude, EngineCodex:
		return r.Reviewer
	}
	if r.Role != "builder" {
		return ""
	}
	switch s.landing().ReviewDefault {
	case EngineClaude, EngineCodex:
		return s.landing().ReviewDefault
	case ReviewDefaultSame:
		return r.Engine
	case ReviewDefaultAuto:
		if r.Engine == EngineCodex {
			return EngineClaude
		}
		if s.landing().CodexInstalled {
			return EngineCodex
		}
		return r.Engine
	}
	return ""
}

// startReview creates the reviewer job for job (in review). A reviewer that cannot be created
// puts the job in front of the human rather than leaving it in review with nobody reviewing.
func (s *Service) startReview(ctx context.Context, job Job, engine string) {
	off := false
	if head, err := landing.Head(ctx, job.WorkDir); err == nil {
		_, _ = s.mutate(ctx, job.ID, func(r *store.JobRecord, _ *change) error {
			if r.ReviewCommit == head {
				return errSkip
			}
			r.ReviewCommit = head
			return nil
		})
	}
	reviewer, err := s.Create(ctx, CreateInput{
		Brief: reviewBrief(job), Dir: job.WorkDir, Role: "reviewer", Engine: engine, Isolate: &off, Reviewer: ReviewerOff,
		reviewOf: mustParse(job.ID), reviewRound: job.ReviewRound,
	}, job.ID)
	if err != nil {
		s.opts.Logger.Warn("starting a reviewer", "job", job.ID, "err", err)
		s.reviewNeedsHuman(ctx, job.ID, "The reviewer could not be started: "+oneLine(err.Error(), 200), "")
		return
	}
	_ = s.Journal(ctx, job.ID, EventReview, "Reviewer "+reviewer.ID+" started ("+engine+")", map[string]any{"reviewer": reviewer.ID})
}

// reviewBrief is what the reviewer is told: the job, where its change is, what the builder said,
// and how to report. It is written by crowsnest, not an agent.
func reviewBrief(job Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review job %s (round %d of %d): %s\n\n", job.ID, job.ReviewRound, maxReviewRounds, job.Title)
	fmt.Fprintf(&b, "You are a reviewer. You are in the job's own worktree (%s, branch %s). ", job.WorkDir, job.WorktreeBranch)
	b.WriteString("READ ONLY: never edit, create or delete a file, never commit, push, merge, rebase, stash or switch branches. ")
	b.WriteString("Run read-only commands and the tests (through `crowsnest heavy` when it is a build or a suite).\n\n")
	if job.BaseBranch != "" {
		fmt.Fprintf(&b, "The change is the branch against %s: `git diff %s...HEAD` and `git log %s..HEAD`.\n\n", job.BaseBranch, job.BaseBranch, job.BaseBranch)
	}
	b.WriteString("Check: does it do what the brief asked, is it correct (edge cases, errors, concurrency), are there tests for what changed and do they pass, does it follow the repository's conventions, is anything risky left unsaid.\n\n")
	b.WriteString("Report by calling done. Its result_md must start with one line, exactly `VERDICT: approve` or `VERDICT: changes`, ")
	b.WriteString("then the findings, most serious first, each with file:line and why it matters. Approve only what you would merge as it is. ")
	b.WriteString("Your findings go back to the builder, who fixes them and asks for another round.\n\n")
	b.WriteString("## The original brief\n\n")
	b.WriteString(indent(job.BriefMD))
	b.WriteString("\n\n## What the builder reported\n\n")
	b.WriteString(indent(job.ResultMD))
	if job.WalkthroughMD != "" {
		b.WriteString("\n\n## The builder's walkthrough\n\n")
		b.WriteString(indent(job.WalkthroughMD))
	}
	if job.ReviewMD != "" && job.ReviewRound > 1 {
		b.WriteString("\n\n## The previous round's findings (check they were addressed)\n\n")
		b.WriteString(indent(job.ReviewMD))
	}
	return b.String()
}

// indent quotes agent-written text as an indented block, so nothing in it reads as part of the
// brief's own instructions.
func indent(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}

// afterSettled runs when a job reaches done or failed (and from Cancel): a reviewer's outcome
// moves the job it reviews on.
func (s *Service) afterSettled(ctx context.Context, job Job) {
	s.wakeWaiters(ctx, job) // jobs blocked until this one settled (waits.go)
	if job.State == StateCancelled {
		s.cancelQuestions(ctx, job.ID, "the job was cancelled")
	}
	if job.FixOf != "" {
		s.fixSettled(ctx, job)
	}
	if job.ReviewOf == "" {
		return
	}
	s.reviewSettled(ctx, job)
}

// cancelQuestions cancels a cancelled job's open questions (DESIGN.md 8): nobody will act on
// their answers. A failed job's questions are left to the human, who may still want to answer.
func (s *Service) cancelQuestions(ctx context.Context, jobID, reason string) {
	if s.opts.CancelQuestions != nil {
		s.opts.CancelQuestions(ctx, jobID, reason)
	}
}

// reviewSettled applies a reviewer's outcome to the job it reviews.
func (s *Service) reviewSettled(ctx context.Context, reviewer Job) {
	parent, err := s.Get(ctx, reviewer.ReviewOf)
	if err != nil || parent.State != StateInReview {
		return
	}
	if reviewer.ReviewRound != 0 && reviewer.ReviewRound != parent.ReviewRound {
		return // a reviewer of an earlier round, settling late: its verdict is not this round's
	}
	findings, verdict := reviewer.ResultMD, ParseVerdict(reviewer.ResultMD)
	switch reviewer.State {
	case StateFailed:
		findings, verdict = "The reviewer failed: "+reviewer.FailReason, VerdictError
	case StateCancelled:
		findings, verdict = "The reviewer was cancelled.", VerdictError
	}
	path := s.writeFindings(parent.ID, parent.ReviewRound, findings)
	switch {
	case verdict == VerdictApprove:
		recheck := false
		approved, err := s.mutate(ctx, parent.ID, func(r *store.JobRecord, c *change) error {
			if State(r.State) != StateInReview {
				return errSkip
			}
			r.ReviewVerdict, r.ReviewMD = verdict, findings
			c.event(EventReview, fmt.Sprintf("Review approved by %s (round %d)", reviewer.ID, r.ReviewRound), map[string]any{"reviewer": reviewer.ID, "findings": path})
			// The approval is for the revision the reviewer saw; evidence for another revision
			// (a commit since the check) is checked again before the job is done (6.15).
			liftHoldLocked(r, c, HoldReviewing, LiftCrowsnest, nil)
			if recheck = s.evidenceMovedLocked(ctx, r); recheck {
				r.AcceptState, r.AcceptDetail = AcceptChecking, "The branch moved after its check; crowsnest is running it again"
				s.placeHoldLocked(r, c, HoldChecking, r.AcceptDetail, "")
				c.event(EventAccept, r.AcceptDetail, nil)
				return nil
			}
			r.State = string(StateDone)
			return nil
		})
		if err == nil && recheck {
			s.startCheck(ctx, parent.ID)
		} else if err == nil && approved.State == StateDone {
			s.afterSettled(ctx, approved)
		}
	case verdict == VerdictError:
		s.reviewNeedsHuman(ctx, parent.ID, findings, findings)
	case parent.ReviewRound >= maxReviewRounds:
		s.reviewNeedsHuman(ctx, parent.ID, fmt.Sprintf("The reviewer still objects after %d rounds; decide: land it anyway, message the builder, or discard it", maxReviewRounds), findings)
	case s.heldWhilePaused(ctx, "review:"+parent.ID, "the review findings for "+parent.ID):
		// Sending the findings starts a builder turn: not while paused (R5-C08). The job stays in
		// review, which is durable; reconcileReview delivers them at the first tick after resume.
	default:
		s.sendBackForChanges(ctx, parent, reviewer, findings, path)
	}
}

// reviewAskGrace is how long a job may sit in review with no reviewer for its round before the
// reconciler starts one: Done starts it a moment after recording the round.
const reviewAskGrace = time.Minute

// reconcileReview gives every intermediate state of the review gate an owner (R5-C05): a job in
// review whose round has no reviewer (crowsnest stopped between Done and startReview) gets one;
// a job whose reviewer has already settled (crowsnest stopped before applying the verdict, or the
// pause held the findings) has the verdict applied. A reviewer records the round it reviews when
// it is created, so the round's reviewer is found by that, never by counting. A live reviewer is supervised like any job: if it
// dies, its own settle lands here.
func (s *Service) reconcileReview(ctx context.Context, r store.JobRecord) {
	if State(r.State) != StateInReview || r.AcceptState == AcceptChecking || r.ReviewRound == 0 {
		return // a running check is accept.go's to finish
	}
	reviewers, err := s.opts.Store.ListReviewJobs(ctx, r.ID)
	if err != nil {
		return
	}
	id := FormatID(r.ID)
	var current *store.JobRecord
	for i := range reviewers {
		if reviewers[i].ReviewRound == r.ReviewRound {
			current = &reviewers[i] // the newest of this round wins (oldest first)
		}
	}
	switch {
	case current == nil:
		if s.opts.Now().Sub(r.LastProgressAt) < reviewAskGrace || s.inflight(id) {
			return
		}
		engine := s.reviewEngine(r)
		if engine == "" {
			s.reviewNeedsHuman(ctx, id, "The reviewer could not be started: no reviewer is configured for this job any more", "")
			return
		}
		_ = s.Journal(ctx, id, EventReview, fmt.Sprintf("Round %d had no reviewer (crowsnest stopped before starting it); starting one", r.ReviewRound), nil)
		s.startReview(ctx, s.toJob(r, 0), engine)
	case State(current.State).Terminal():
		s.reviewSettled(ctx, s.toJob(*current, 0))
	}
}

// sendBackForChanges puts the job back to running and types the findings to its builder.
func (s *Service) sendBackForChanges(ctx context.Context, parent Detail, reviewer Job, findings, path string) {
	job, err := s.mutate(ctx, parent.ID, func(r *store.JobRecord, c *change) error {
		if State(r.State) != StateInReview {
			return errSkip
		}
		liftHoldLocked(r, c, HoldReviewing, LiftCrowsnest, nil)
		r.State, r.ReviewVerdict, r.ReviewMD = string(StateRunning), VerdictChanges, findings
		s.markProgressLocked(r) // a fresh start for the nudge budget: the builder has new work
		c.event(EventReview, fmt.Sprintf("Changes requested by %s (round %d of %d); the findings were sent to the agent", reviewer.ID, r.ReviewRound, maxReviewRounds),
			map[string]any{"reviewer": reviewer.ID, "findings": path})
		return nil
	})
	if err != nil || job.PaneID == "" {
		return
	}
	text := fmt.Sprintf("[crowsnest review %s] The reviewer asked for changes (round %d of %d). Its findings are in %s (job_get shows them too). "+
		"Address each one, commit, and call done again with what you changed.", reviewer.ID, job.ReviewRound, maxReviewRounds, path)
	if path == "" {
		text = fmt.Sprintf("[crowsnest review %s] The reviewer asked for changes (round %d of %d); job_get shows its findings. Address each one, commit, and call done again.",
			reviewer.ID, job.ReviewRound, maxReviewRounds)
	}
	if err := s.sendText(ctx, job.PaneID, job.AgentName, text); err != nil {
		_ = s.Journal(ctx, job.ID, EventReview, "The findings could not be typed to the agent: "+oneLine(err.Error(), 160)+"; send them with a message", nil)
	}
}

// reviewNeedsHuman ends the review in front of the human: needs_you, cause review.
func (s *Service) reviewNeedsHuman(ctx context.Context, id, why, findings string) {
	_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return errSkip
		}
		verdict := VerdictChanges
		if strings.HasPrefix(why, "The reviewer could not") || strings.HasPrefix(why, "The reviewer failed") || strings.HasPrefix(why, "The reviewer was cancelled") {
			verdict = VerdictError
		}
		liftHoldLocked(r, c, HoldReviewing, LiftCrowsnest, nil)
		if State(r.State) == StateInReview {
			r.State = string(StateRunning) // the review hold below keeps it in front of the human
		}
		r.ReviewVerdict = verdict
		s.placeHoldLocked(r, c, HoldReview, why, "")
		if findings != "" {
			r.ReviewMD = findings
		}
		c.event(EventReview, "Review needs you: "+oneLine(why, 200), nil)
		c.notify = fmt.Sprintf("crowsnest: job %s needs you after its review: %s", FormatID(r.ID), oneLine(r.Title, 80))
		return nil
	})
}

// writeFindings keeps a round's findings beside the job's files and returns the path ("" when it
// could not be written: the findings are still on the job).
func (s *Service) writeFindings(jobID string, round int, findings string) string {
	dir, err := s.jobDir(jobID)
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("review-round-%d.md", round))
	_ = os.Remove(path)
	if err := os.WriteFile(path, []byte(findings+"\n"), 0o600); err != nil {
		return ""
	}
	return path
}

// evidenceMovedLocked reports whether a job accepted by a check has moved past the commit it
// passed at (s.mu held): then an approval needs a fresh check before the job is done.
func (s *Service) evidenceMovedLocked(ctx context.Context, r *store.JobRecord) bool {
	if r.AcceptState != AcceptAccepted {
		return false
	}
	check, err := s.opts.Store.LatestCheck(ctx, r.ID)
	if err != nil || check.Status != CheckPassed {
		return false
	}
	head, err := landing.Head(ctx, workDirOf(*r))
	return err == nil && head != check.Commit
}
