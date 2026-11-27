// --demo jobs (DESIGN.md 6.8, 21.5): a board with a job in every state across the demo's five
// projects and the Unfiled work the orchestrator holds, written straight into the store (nothing
// is launched), plus a small loop that keeps it alive: the running job reports progress, the
// nudged job gets nudged again now and then, the job stuck on a limit counts down to its next
// retry and reschedules it, and a job whose pane shows a terminal dialog is needs_you with that
// dialog's command (and running again once it is answered). Every job with a pane is held by the
// fake fleet's agent of the same name (internal/demostory), in its project's workspace; the
// questions and approvals that wait on these jobs are seeded afterwards by internal/questions and
// internal/approvals, from the same panes.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jclement/crowsnest/internal/demostory"
	"github.com/jclement/crowsnest/internal/store"
)

// DemoPanes is what the demo jobs need from the demo fleet and the demo projects.
type DemoPanes struct {
	// PaneOf returns the pane of the demo agent named name ("" for none).
	PaneOf func(agentName string) string
	// DialogOf returns the command of the terminal dialog on a pane now ("" for none). A dialog
	// job's cause detail is read from it, so it always says what the screen says.
	DialogOf func(paneID string) string
	// ProjectOf files a demo job under a demo project by its directory (DESIGN.md 21.5): the
	// project's id and the real directory to record instead of the made-up one. Nil: Unfiled.
	ProjectOf func(dir string) (projectID int64, realDir string)
}

// demoStep is how often the demo loop moves things along; demoDialogCheck is how often it reads
// the dialog panes, short so an answered dialog leaves the board about when it leaves the screen.
const (
	demoStep        = 20 * time.Second
	demoDialogCheck = 2 * time.Second
)

type demoEvent struct {
	ago     time.Duration
	kind    string
	summary string
}

type demoJob struct {
	record store.JobRecord
	parent string // the agent name of the job that created this one ("" for none)
	events []demoEvent
}

// SeedDemo fills an empty board with the demo jobs and starts the loop. It does nothing when
// jobs already exist.
func SeedDemo(ctx context.Context, s *Service, panes DemoPanes) error {
	existing, err := s.opts.Store.ListJobs(ctx, nil, 1)
	if err != nil || len(existing) > 0 {
		return err
	}
	ids := map[string]string{} // agent name → job id: parents, and the jobs runDemo moves
	for position, item := range demoJobs(s.opts.Now(), panes) {
		if item.parent != "" {
			item.record.ParentID = mustParse(ids[item.parent])
		}
		if panes.ProjectOf != nil && item.record.Dir != "" {
			if projectID, dir := panes.ProjectOf(item.record.Dir); projectID != 0 {
				item.record.ProjectID, item.record.Dir = projectID, dir
			}
		}
		id, err := s.opts.Store.InsertJob(ctx, item.record)
		if err != nil {
			return fmt.Errorf("seeding a demo job: %w", err)
		}
		if want := int64(position + 1); id != want {
			// The fleet's agents are named job-<n> for these ids (internal/demostory).
			s.opts.Logger.Warn("demo job ids do not follow the seed order; job and agent names will disagree", "id", id, "want", want)
		}
		for _, event := range item.events {
			if _, err := s.opts.Store.InsertJobEvent(ctx, store.JobEventRecord{JobID: id, At: s.opts.Now().Add(-event.ago), Kind: event.kind, Summary: event.summary}); err != nil {
				return err
			}
		}
		if item.record.AgentName != "" && item.record.AgentName != OrchestratorName {
			ids[item.record.AgentName] = FormatID(id)
		}
	}
	go runDemo(ctx, s, ids, panes, s.opts.Logger)
	return nil
}

// demoBase builds a job record with the fields every demo job shares. A job held by an agent
// sits in that agent's pane.
func demoBase(now time.Time, panes DemoPanes, title, brief, role, dir, state, agent string, created time.Duration) store.JobRecord {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	pane := ""
	if agent != "" && panes.PaneOf != nil {
		pane = panes.PaneOf(agent)
	}
	return store.JobRecord{Title: title, BriefMD: brief, Role: role, Dir: dir, Engine: EngineClaude, State: state,
		PaneID: pane, AgentName: agent, BriefSent: pane != "", CreatedAt: ago(created), StartedAt: ago(created - time.Minute),
		LastActivityAt: ago(time.Minute), LastProgressAt: ago(time.Minute), VerifiedJSON: "[]", NotVerifiedJSON: "[]"}
}

// demoJobs is the board in id order: the first ten are the states every demo has had (their ids
// are what tests and screenshots name), then the rest of the story's jobs.
func demoJobs(now time.Time, panes DemoPanes) []demoJob {
	return append(demoStates(now, panes), demoStory(now, panes)...)
}

// demoStates is one job in each state the board renders (j_1 to j_10).
func demoStates(now time.Time, panes DemoPanes) []demoJob {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	list := func(items ...string) string { body, _ := json.Marshal(items); return string(body) }
	crowsnest, website, mobile, handbook := demostory.Dir(demostory.Crowsnest), demostory.Dir(demostory.Website), demostory.Dir(demostory.Mobile), demostory.Dir(demostory.Handbook)

	running := demoBase(now, panes, "Add retry back-off to the webhook sender", "Add retry back-off to the webhook sender\n\nExponential, capped at 5 minutes, with jitter. Tests for the schedule.",
		"builder", crowsnest, string(StateRunning), demostory.WebhookBuilder, 40*time.Minute)
	running.WorktreeBranch, running.WorkDir = "job/1-add-retry-back-off", "~/.herdr/worktrees/crowsnest/job-1"
	running.Progress, running.LastTool = demoProgress[0], "Bash: go test ./internal/webhook/ -run Backoff"

	nudged := demoBase(now, panes, "Tidy the install guide", "Tidy the install guide on the website: one path per platform.", "builder", website, string(StateRunning), demostory.InstallGuide, 25*time.Minute)
	nudged.Progress, nudged.Nudges, nudged.NudgeStreak, nudged.LastNudgeAt = "Rewriting the macOS steps", 2, 1, ago(3*time.Minute)

	limit := demoBase(now, panes, "Migrate the settings page to the new form kit", "Migrate the mobile app's settings page to the new form kit.", "builder", mobile, string(StateRunning), demostory.FormKit, 2*time.Hour)
	limit.Stuck, limit.Cause, limit.CauseDetail = true, string(CauseLimit), "Usage limit reached; resets at 14:00"
	limit.RetryAt, limit.RetryCount, limit.Progress, limit.LastActivityAt = now.Add(4*time.Minute), 1, "Converting the notification toggles", ago(16*time.Minute)

	needsYou := demoBase(now, panes, "Decide the release date for v0.2", "Draft the v0.2 release notes and pick a date.", "lead", "", string(StateNeedsYou), OrchestratorName, 90*time.Minute)
	needsYou.Cause, needsYou.CauseDetail, needsYou.Waiting, needsYou.Progress = string(CauseQuestions), "1 open question(s)", true, "Waiting on the release date"
	needsYou.LastActivityAt = ago(29 * time.Minute)

	dialog := demoBase(now, panes, "Rebuild and publish the handbook site", "Rebuild the handbook site from a clean tree and publish it to gh-pages.", "builder", handbook, string(StateNeedsYou), demostory.HandbookPublish, 12*time.Minute)
	dialog.Cause, dialog.CauseDetail = string(CauseDialog), demoDialogDetail(panes, dialog.PaneID)
	dialog.LastActivityAt = ago(2 * time.Minute)

	unresponsive := demoBase(now, panes, "Profile the fleet cache", "Profile the fleet cache under 200 panes.", "tester", crowsnest, string(StateRunning), demostory.CacheProfiler, 3*time.Hour)
	unresponsive.Stuck, unresponsive.Cause, unresponsive.Nudges, unresponsive.NudgeStreak = true, string(CauseUnresponsive), 3, 3
	unresponsive.CauseDetail, unresponsive.LastActivityAt = "Ended its turn 4 times in a row without declaring anything or doing any work.", ago(35*time.Minute)

	blocked := demoBase(now, panes, "Upgrade the Postgres driver", "Upgrade the Postgres driver to v6.", "builder", crowsnest, string(StateBlocked), "", 5*time.Hour)
	blocked.AgentName, blocked.BlockedReason, blocked.Progress = "job-7", "Waiting for the staging database to come back", "Blocked on staging"

	done := demoBase(now, panes, "Fix the flaky SSE reconnect test", "Fix the flaky SSE reconnect test.", "builder", crowsnest, string(StateDone), "", 4*time.Hour)
	done.AgentName, done.SettledAt, done.Progress = "job-8", ago(2*time.Hour), "Done"
	done.ResultMD = "Fixed the race in `sse.test.tsx`: the fake timer advanced before the stream opened.\n\n- Branch `job/8-fix-flaky-sse`, one commit\n- The test now waits for `open` before advancing"
	done.VerifiedJSON, done.NotVerifiedJSON = list("npm test -- sse: 50 runs, 0 failures", "mise run check: green"), list("Safari (no WebKit runner here)")

	failed := demoBase(now, panes, "Port the TUI to Bubble Tea v2", "Port the TUI to Bubble Tea v2.", "builder", crowsnest, string(StateFailed), "", 6*time.Hour)
	failed.AgentName, failed.SettledAt, failed.FailReason = "job-9", ago(5*time.Hour), "Bubble Tea v2 is not released yet; the beta API changed twice this week."

	queued := demoBase(now, panes, "Write the changelog for M7", "Write the changelog for M7.", "lead", "", string(StateQueued), "", 2*time.Minute)
	queued.BriefSent, queued.StartedAt = false, time.Time{}

	return []demoJob{
		{record: running, events: []demoEvent{{40 * time.Minute, EventCreated, "Job created"}, {39 * time.Minute, EventLaunch, "Opened pane in a worktree, branch job/1-add-retry-back-off"}, {38 * time.Minute, EventLaunch, "Brief sent to job-1"}, {38 * time.Minute, EventState, "queued → running"}, {20 * time.Minute, EventProgress, "Reading the sender"}, {time.Minute, EventProgress, running.Progress}}},
		{record: nudged, events: []demoEvent{{25 * time.Minute, EventCreated, "Job created"}, {24 * time.Minute, EventState, "queued → running"}, {10 * time.Minute, EventNudge, "Nudged (1 of 3): the turn ended with nothing declared"}, {9 * time.Minute, EventProgress, "Rewriting the macOS steps"}, {3 * time.Minute, EventNudge, "Nudged (1 of 3): the turn ended with nothing declared"}}},
		{record: limit, events: []demoEvent{{2 * time.Hour, EventCreated, "Job created"}, {16 * time.Minute, EventStuck, "API error: rate_limit"}, {11 * time.Minute, EventRetry, "Retry 1: typed \"continue\""}}},
		{record: needsYou, events: []demoEvent{{90 * time.Minute, EventCreated, "Job created"}, {89 * time.Minute, EventLaunch, "Assigned to the orchestrator"}, {29 * time.Minute, EventProgress, "Declared: nothing left that the open questions do not block"}}},
		{record: dialog, events: []demoEvent{{12 * time.Minute, EventCreated, "Job created"}, {2 * time.Minute, EventStuck, "Needs you: " + dialog.CauseDetail}}},
		{record: unresponsive, events: []demoEvent{{3 * time.Hour, EventCreated, "Job created"}, {38 * time.Minute, EventNudge, "Nudged (1 of 3)"}, {37 * time.Minute, EventNudge, "Nudged (2 of 3)"}, {36 * time.Minute, EventNudge, "Nudged (3 of 3)"}, {35 * time.Minute, EventStuck, "Stuck: unresponsive after three nudges; the stop was allowed"}}},
		{record: blocked, events: []demoEvent{{5 * time.Hour, EventCreated, "Job created"}, {time.Hour, EventProgress, "Blocked: Waiting for the staging database to come back"}}},
		{record: done, events: []demoEvent{{4 * time.Hour, EventCreated, "Job created"}, {2 * time.Hour, EventDone, "Done: Fixed the race in sse.test.tsx"}}},
		{record: failed, events: []demoEvent{{6 * time.Hour, EventCreated, "Job created"}, {5 * time.Hour, EventFail, "Failed: Bubble Tea v2 is not released yet"}}},
		{record: queued, events: []demoEvent{{2 * time.Minute, EventCreated, "Job created"}}},
	}
}

// demoStory is the rest of the morning (j_11 to j_17): the Crowsnest lead's fleet cache job and
// the reviewer it started, the three jobs whose tool calls wait for approval, the Website job
// waiting on a blocking question, and Infra's one quiet job.
func demoStory(now time.Time, panes DemoPanes) []demoJob {
	crowsnest, website, mobile, infra := demostory.Dir(demostory.Crowsnest), demostory.Dir(demostory.Website), demostory.Dir(demostory.Mobile), demostory.Dir(demostory.Infra)

	cache := demoBase(now, panes, "Coalesce the fleet cache refresh", "Make the fleet cache coalesce events instead of resetting a debounce timer, so a busy pane cannot starve the refetch. Have it reviewed before asking to apply.",
		"builder", crowsnest, string(StateNeedsYou), demostory.CacheBuilder, 70*time.Minute)
	cache.Origin, cache.Cause, cache.CauseDetail, cache.Waiting = LeadPrefix+demostory.Crowsnest, string(CauseQuestions), "1 open question(s)", true
	cache.Progress, cache.LastActivityAt = "Waiting on whether to apply the refactor", demoAgo(now, 6*time.Minute)

	review := demoBase(now, panes, "Review the fleet cache refactor", "Review wip/fleet-cache. Findings to REVIEW.md, then ask which the builder should fix before merge.",
		"reviewer", crowsnest, string(StateRunning), demostory.CacheReviewer, 30*time.Minute)
	review.Origin, review.Progress = OriginAgent, "Writing findings to REVIEW.md"

	limiter := demoBase(now, panes, "Review the rate-limiter fix on fix/limiter", "Review fix/limiter, rebase it onto main and land it if it is sound.", "reviewer", crowsnest, string(StateNeedsYou), demostory.LimiterReviewer, 45*time.Minute)
	limiter.Cause, limiter.CauseDetail = string(CauseApprovalTool), "Bash: git push --force origin main"

	links := demoBase(now, panes, "Fix the broken links the link check found", "The link check found 3 broken links in the built site. Fix them and check again.", "builder", website, string(StateNeedsYou), demostory.LinkFixer, 20*time.Minute)
	links.Cause, links.CauseDetail = string(CauseApprovalTool), "Bash: npx linkinator ./dist --recurse"

	hero := demoBase(now, panes, "Rewrite the homepage hero", "Rewrite the homepage hero so it says what crowsnest does, not what it is built on.", "builder", website, string(StateNeedsYou), demostory.HeroWriter, 50*time.Minute)
	hero.Cause, hero.CauseDetail, hero.Waiting, hero.Progress = string(CauseQuestions), "1 open question(s)", true, "Waiting on the first sentence"

	refresh := demoBase(now, panes, "Add pull-to-refresh to the inbox screen", "Add pull-to-refresh to the mobile inbox screen, with a test.", "builder", mobile, string(StateNeedsYou), demostory.RefreshBuilder, 35*time.Minute)
	refresh.Cause, refresh.CauseDetail = string(CauseApprovalTool), "Edit: "+demoRefreshFile

	devbox := demoBase(now, panes, "Pin the devbox base image by digest", "Pin the devbox image's base to a digest, rebuild it and smoke-test it.", "builder", infra, string(StateRunning), demostory.DevboxBuilder, 15*time.Minute)
	devbox.Progress = "Rebuilding the devbox image"

	approvalWait := func(detail string) demoEvent {
		return demoEvent{time.Minute, EventStuck, "Needs you: a tool call waits for approval: " + detail}
	}
	return []demoJob{
		{record: cache, events: []demoEvent{{70 * time.Minute, EventCreated, "Job created by lead-crowsnest"}, {30 * time.Minute, EventChild, "Delegated j_12: Review the fleet cache refactor"}, {6 * time.Minute, EventProgress, "Declared: nothing left that the open questions do not block"}}},
		{record: review, parent: demostory.CacheBuilder, events: []demoEvent{{30 * time.Minute, EventCreated, "Job created by job-11"}, {5 * time.Minute, EventProgress, review.Progress}}},
		{record: limiter, events: []demoEvent{{45 * time.Minute, EventCreated, "Job created"}, {10 * time.Minute, EventProgress, "Rebased fix/limiter onto main; 1 conflict resolved"}, approvalWait(limiter.CauseDetail)}},
		{record: links, events: []demoEvent{{20 * time.Minute, EventCreated, "Job created by the link-check schedule"}, approvalWait(links.CauseDetail)}},
		{record: hero, events: []demoEvent{{50 * time.Minute, EventCreated, "Job created"}, {8 * time.Minute, EventProgress, "Declared: nothing left that the open questions do not block"}}},
		{record: refresh, events: []demoEvent{{35 * time.Minute, EventCreated, "Job created"}, approvalWait(refresh.CauseDetail)}},
		{record: devbox, events: []demoEvent{{15 * time.Minute, EventCreated, "Job created"}, {4 * time.Minute, EventProgress, devbox.Progress}}},
	}
}

// demoRefreshFile is the file the Mobile job's Edit approval changes, relative to the project
// (internal/approvals/demo.go asks for it under the job's real directory).
const demoRefreshFile = "src/screens/InboxScreen.tsx"

func demoAgo(now time.Time, d time.Duration) time.Time { return now.Add(-d) }

// demoDialogDetail is a dialog job's cause detail: the command its pane's dialog shows.
func demoDialogDetail(panes DemoPanes, paneID string) string {
	if panes.DialogOf != nil {
		if command := panes.DialogOf(paneID); command != "" {
			return "Claude wants to run: " + command
		}
	}
	return "A terminal dialog is waiting"
}

var demoProgress = []string{"Writing table tests for the back-off schedule", "Running the webhook tests", "Fixing the jitter bound", "Committing on job/1-add-retry-back-off"}

// runDemo keeps the demo board moving until ctx ends.
func runDemo(ctx context.Context, s *Service, ids map[string]string, panes DemoPanes, logger *slog.Logger) {
	ticker := time.NewTicker(demoStep)
	defer ticker.Stop()
	dialogs := time.NewTicker(demoDialogCheck)
	defer dialogs.Stop()
	for step := 0; ; {
		select {
		case <-ctx.Done():
			return
		case <-dialogs.C:
			for _, id := range ids {
				reconcileDemoDialog(ctx, s, id, panes)
			}
			continue
		case <-ticker.C:
		}
		stepDemo(ctx, s, ids, step)
		logger.Debug("demo jobs stepped", "step", step)
		step++
	}
}

// stepDemo is one round of the loop: progress, a nudge now and then, and the limit's retry.
func stepDemo(ctx context.Context, s *Service, ids map[string]string, step int) {
	if id := ids[demostory.WebhookBuilder]; id != "" {
		_, _ = s.Update(ctx, id, UpdateInput{Progress: demoProgress[(step+1)%len(demoProgress)]})
	}
	if id := ids[demostory.InstallGuide]; id != "" && step%6 == 5 {
		_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
			r.Nudges++
			r.LastNudgeAt = s.opts.Now()
			c.event(EventNudge, "Nudged (1 of 3): the turn ended with nothing declared", nil)
			return nil
		})
	}
	if id := ids[demostory.FormKit]; id != "" {
		_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
			if r.Stuck && !r.RetryAt.IsZero() && s.opts.Now().After(r.RetryAt) {
				r.RetryCount++
				r.RetryAt = s.opts.Now().Add(retryDelay(limitRetries, limitEvery, r.RetryCount))
				c.event(EventRetry, fmt.Sprintf("Retry %d: typed \"continue\" (still limited)", r.RetryCount), nil)
			}
			return nil
		})
	}
}

// reconcileDemoDialog keeps a demo job in step with a terminal dialog on its pane, as the real
// supervisor's dialog check does: a dialog makes a running job needs_you with the dialog's
// command; an answered one sends it back to running. A job waiting on an approval is left to
// the approvals (its permission dialog is the approval's, not a terminal dialog).
func reconcileDemoDialog(ctx context.Context, s *Service, id string, panes DemoPanes) {
	if panes.DialogOf == nil {
		return
	}
	_, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if r.PaneID == "" || State(r.State).Terminal() {
			return errSkip
		}
		command := panes.DialogOf(r.PaneID)
		atDialog := State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseDialog
		switch {
		case command != "" && (State(r.State) == StateRunning || (atDialog && r.CauseDetail != "Claude wants to run: "+command)):
			r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseDialog), "Claude wants to run: "+command
			r.LastActivityAt = s.opts.Now()
			c.event(EventStuck, "Needs you: "+r.CauseDetail, nil)
		case command == "" && atDialog:
			r.State, r.Cause, r.CauseDetail = string(StateRunning), "", ""
			r.LastActivityAt = s.opts.Now()
			c.event(EventHook, "The dialog was answered; back to running", nil)
		default:
			return errSkip
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSkip) && ctx.Err() == nil {
		s.opts.Logger.Debug("demo dialog check", "job", id, "err", err)
	}
}
