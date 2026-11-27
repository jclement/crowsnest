// --demo jobs: a board with a job in every state, written straight into the store (nothing is
// launched), plus a small loop that keeps it alive: the running job reports progress, the
// nudged job gets nudged again now and then, and the job stuck on a limit counts down to its
// next retry and reschedules it. Used for UI work, screenshots and hermetic E2E.

package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// DemoPanes names the demo fleet's panes the seeded jobs sit in.
type DemoPanes struct {
	Orchestrator, Builder, Reviewer, Docs, Linter string
	// ProjectOf files a demo job under a demo project by its directory (DESIGN.md 21.5): the
	// project's id and the real directory to record instead of the made-up one. Nil: Unfiled.
	ProjectOf func(dir string) (projectID int64, realDir string)
}

// demoStep is how often the demo loop moves things along.
const demoStep = 20 * time.Second

type demoEvent struct {
	ago     time.Duration
	kind    string
	summary string
}

type demoJob struct {
	record store.JobRecord
	events []demoEvent
}

// SeedDemo fills an empty board with the demo jobs and starts the loop. It does nothing when
// jobs already exist.
func SeedDemo(ctx context.Context, s *Service, panes DemoPanes) error {
	existing, err := s.opts.Store.ListJobs(ctx, nil, 1)
	if err != nil || len(existing) > 0 {
		return err
	}
	ids := map[string]string{}
	for _, item := range demoJobs(s.opts.Now(), panes) {
		key := item.record.AgentName // runDemo finds the scripted jobs by these names
		if item.record.PaneID != "" && s.opts.Herdr != nil {
			// The job names the agent its pane really holds, as a real launch records it, so the
			// supervisor's ExpectAgent check (a nudge, a message) accepts the demo's panes.
			if agent, err := s.opts.Herdr.AgentGet(ctx, item.record.PaneID); err == nil && agent.Name != "" {
				item.record.AgentName = agent.Name
			}
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
		for _, event := range item.events {
			if _, err := s.opts.Store.InsertJobEvent(ctx, store.JobEventRecord{JobID: id, At: s.opts.Now().Add(-event.ago), Kind: event.kind, Summary: event.summary}); err != nil {
				return err
			}
		}
		ids[key] = FormatID(id)
		if item.record.State == string(StateNeedsYou) && Cause(item.record.Cause) == CauseQuestions {
			if err := seedDemoQuestion(ctx, s, FormatID(id), item.record.PaneID); err != nil {
				return err
			}
		}
	}
	go runDemo(ctx, s, ids, s.opts.Logger)
	return nil
}

func demoJobs(now time.Time, panes DemoPanes) []demoJob {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	list := func(items ...string) string { body, _ := json.Marshal(items); return string(body) }
	base := func(title, brief, role, dir, state, pane, agent string, created time.Duration) store.JobRecord {
		return store.JobRecord{Title: title, BriefMD: brief, Role: role, Dir: dir, Engine: EngineClaude, State: state,
			PaneID: pane, AgentName: agent, BriefSent: pane != "", CreatedAt: ago(created), StartedAt: ago(created - time.Minute),
			LastActivityAt: ago(time.Minute), LastProgressAt: ago(time.Minute), VerifiedJSON: "[]", NotVerifiedJSON: "[]"}
	}
	running := base("Add retry back-off to the webhook sender", "Add retry back-off to the webhook sender\n\nExponential, capped at 5 minutes, with jitter. Tests for the schedule.",
		"builder", "~/src/crowsnest", string(StateRunning), panes.Builder, "job-1", 40*time.Minute)
	running.WorktreeBranch, running.WorkDir = "job/1-add-retry-back-off", "~/.herdr/worktrees/crowsnest/job-1"
	running.Progress, running.LastTool = "Writing table tests for the back-off schedule", "Bash: go test ./internal/webhook/ -run Backoff"

	nudged := base("Tidy the README install section", "Tidy the README install section: one path per platform.", "builder", "~/src/website", string(StateRunning), panes.Builder, "job-2", 25*time.Minute)
	nudged.Progress, nudged.Nudges, nudged.NudgeStreak, nudged.LastNudgeAt = "Rewriting the macOS steps", 2, 1, ago(3*time.Minute)
	nudged.BudgetUSD, nudged.BudgetState, nudged.BudgetDetail = 1, BudgetWarned, "Used 84%: $0.84 of its $1.00 budget"
	nudged.LastMessage, nudged.LastMessageAt = "The macOS steps are rewritten. Want me to do the Linux ones the same way?", ago(3*time.Minute)

	limit := base("Migrate the settings page to the new form kit", "Migrate the settings page to the new form kit.", "builder", "~/src/crowsnest", string(StateRunning), panes.Linter, "job-3", 2*time.Hour)
	limit.Stuck, limit.Cause, limit.CauseDetail = true, string(CauseLimit), "Usage limit reached; resets at 14:00"
	limit.RetryAt, limit.RetryCount, limit.Progress, limit.LastActivityAt = now.Add(4*time.Minute), 1, "Converting the notification toggles", ago(16*time.Minute)

	needsYou := base("Decide the release date for v0.2", "Draft the v0.2 release notes and pick a date.", "lead", "", string(StateNeedsYou), panes.Orchestrator, OrchestratorName, 90*time.Minute)
	needsYou.Cause, needsYou.CauseDetail, needsYou.Waiting, needsYou.Progress = string(CauseQuestions), "1 open question", true, "Waiting on the release date"
	needsYou.LastActivityAt = ago(29 * time.Minute)

	dialog := base("Review the auth change", "Review the auth change on branch fix/limiter.", "reviewer", "~/src/crowsnest", string(StateNeedsYou), panes.Docs, "job-5", 12*time.Minute)
	dialog.Cause, dialog.CauseDetail = string(CauseDialog), "Claude wants to run: rm -rf node_modules"
	dialog.LastActivityAt = ago(2 * time.Minute)

	unresponsive := base("Profile the fleet cache", "Profile the fleet cache under 200 panes.", "tester", "~/src/crowsnest", string(StateRunning), panes.Reviewer, "job-6", 3*time.Hour)
	unresponsive.Stuck, unresponsive.Cause, unresponsive.Nudges, unresponsive.NudgeStreak = true, string(CauseUnresponsive), 3, 3
	unresponsive.CauseDetail, unresponsive.LastActivityAt = "Ended its turn 4 times in a row without declaring anything or doing any work.", ago(35*time.Minute)
	unresponsive.LastMessage, unresponsive.LastMessageAt = "Profiling at 200 panes shows the cache rebuilds on every event. I'm not sure how to proceed without changing herdr's event shape.", ago(35*time.Minute)

	blocked := base("Upgrade the Postgres driver", "Upgrade the Postgres driver to v6.", "builder", "~/src/crowsnest", string(StateBlocked), "", "job-7", 5*time.Hour)
	blocked.BlockedReason, blocked.Progress = "Waiting for the staging database to come back", "Blocked on staging"
	blocked.WakeKind, blocked.WakeAt, blocked.Rechecks = WakeTime, now.Add(25*time.Minute), 1
	blocked.LastMessage, blocked.LastMessageAt = "Staging's Postgres answers 503. Blocked; I'll look again at the recheck.", ago(time.Hour)

	done := base("Fix the flaky SSE reconnect test", "Fix the flaky SSE reconnect test.", "builder", "~/src/crowsnest", string(StateDone), "", "job-8", 4*time.Hour)
	done.SettledAt, done.Progress = ago(2*time.Hour), "Done"
	done.ResultMD = "Fixed the race in `sse.test.tsx`: the fake timer advanced before the stream opened.\n\n- Branch `job/8-fix-flaky-sse`, one commit\n- The test now waits for `open` before advancing"
	done.VerifiedJSON, done.NotVerifiedJSON = list("npm test -- sse: 50 runs, 0 failures", "mise run check: green"), list("Safari (no WebKit runner here)")
	done.AcceptState, done.AcceptDetail = AcceptUnverified, "Finished, unverified: no check command (no mise check task, and none set on a project)"

	failed := base("Port the TUI to Bubble Tea v2", "Port the TUI to Bubble Tea v2.", "builder", "~/src/crowsnest", string(StateFailed), "", "job-9", 6*time.Hour)
	failed.SettledAt, failed.FailReason = ago(5*time.Hour), "Bubble Tea v2 is not released yet; the beta API changed twice this week."

	queued := base("Write the changelog for M7", "Write the changelog for M7.", "lead", "", string(StateQueued), "", "", 2*time.Minute)
	queued.BriefSent, queued.StartedAt = false, time.Time{}

	return []demoJob{
		{record: running, events: []demoEvent{{40 * time.Minute, EventCreated, "Job created"}, {39 * time.Minute, EventLaunch, "Opened pane in a worktree, branch job/1-add-retry-back-off"}, {38 * time.Minute, EventLaunch, "Brief sent to job-1"}, {38 * time.Minute, EventState, "queued → running"}, {20 * time.Minute, EventProgress, "Reading the sender"}, {time.Minute, EventProgress, running.Progress}}},
		{record: nudged, events: []demoEvent{{25 * time.Minute, EventCreated, "Job created"}, {24 * time.Minute, EventState, "queued → running"}, {10 * time.Minute, EventNudge, "Nudged (1 of 3): the turn ended with nothing declared"}, {9 * time.Minute, EventProgress, "Rewriting the macOS steps"}, {3 * time.Minute, EventNudge, "Nudged (1 of 3): the turn ended with nothing declared"}}},
		{record: limit, events: []demoEvent{{2 * time.Hour, EventCreated, "Job created"}, {16 * time.Minute, EventStuck, "API error: rate_limit"}, {11 * time.Minute, EventRetry, "Retry 1: typed \"continue\""}}},
		{record: needsYou, events: []demoEvent{{90 * time.Minute, EventCreated, "Job created"}, {89 * time.Minute, EventLaunch, "Assigned to the orchestrator"}, {30 * time.Minute, EventQuestion, "Asked q_1: Ship v0.2 on Friday?"}, {29 * time.Minute, EventProgress, "Declared: nothing left that the open questions do not block"}}},
		{record: dialog, events: []demoEvent{{12 * time.Minute, EventCreated, "Job created"}, {2 * time.Minute, EventStuck, "Needs you: Claude wants to run: rm -rf node_modules"}}},
		{record: unresponsive, events: []demoEvent{{3 * time.Hour, EventCreated, "Job created"}, {38 * time.Minute, EventNudge, "Nudged (1 of 3)"}, {37 * time.Minute, EventNudge, "Nudged (2 of 3)"}, {36 * time.Minute, EventNudge, "Nudged (3 of 3)"}, {35 * time.Minute, EventStuck, "Stuck: unresponsive after three nudges; the stop was allowed"}}},
		{record: blocked, events: []demoEvent{{5 * time.Hour, EventCreated, "Job created"}, {time.Hour, EventProgress, "Blocked: Waiting for the staging database to come back"}}},
		{record: done, events: []demoEvent{{4 * time.Hour, EventCreated, "Job created"}, {2 * time.Hour, EventDone, "Done: Fixed the race in sse.test.tsx"}}},
		{record: failed, events: []demoEvent{{6 * time.Hour, EventCreated, "Job created"}, {5 * time.Hour, EventFail, "Failed: Bubble Tea v2 is not released yet"}}},
		{record: queued, events: []demoEvent{{2 * time.Minute, EventCreated, "Job created"}}},
	}
}

// seedDemoQuestion gives the demo's needs-you job the open question it is waiting on.
func seedDemoQuestion(ctx context.Context, s *Service, jobID, paneID string) error {
	now := s.opts.Now()
	options, _ := json.Marshal([]map[string]string{{"key": "fri", "label": "Friday"}, {"key": "mon", "label": "Next Monday"}})
	_, err := s.opts.Store.InsertQuestion(ctx, store.QuestionRecord{
		Title: "Ship v0.2 on Friday?", BodyMD: "The release notes are drafted. Friday gives a quiet weekend to watch it; Monday gives the docs another pass.",
		Kind: "choice", Options: options, Recommend: "fri", Urgency: "normal", SourcePane: paneID, JobID: jobID,
		CreatedAt: now.Add(-30 * time.Minute), ExpiresAt: now.Add(24 * time.Hour),
	})
	return err
}

var demoProgress = []string{"Writing table tests for the back-off schedule", "Running the webhook tests", "Fixing the jitter bound", "Committing on job/1-add-retry-back-off"}

// runDemo keeps the demo board moving until ctx ends.
func runDemo(ctx context.Context, s *Service, ids map[string]string, logger *slog.Logger) {
	ticker := time.NewTicker(demoStep)
	defer ticker.Stop()
	for step := 0; ; step++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if id := ids["job-1"]; id != "" {
			_, _ = s.Update(ctx, id, UpdateInput{Progress: demoProgress[step%len(demoProgress)]})
		}
		if id := ids["job-2"]; id != "" && step%6 == 5 {
			_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
				r.Nudges++
				r.LastNudgeAt = s.opts.Now()
				c.event(EventNudge, "Nudged (1 of 3): the turn ended with nothing declared", nil)
				return nil
			})
		}
		if id := ids["job-3"]; id != "" {
			_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
				if r.Stuck && !r.RetryAt.IsZero() && s.opts.Now().After(r.RetryAt) {
					r.RetryCount++
					r.RetryAt = s.opts.Now().Add(retryDelay(limitRetries, limitEvery, r.RetryCount))
					c.event(EventRetry, fmt.Sprintf("Retry %d: typed \"continue\" (still limited)", r.RetryCount), nil)
				}
				return nil
			})
		}
		logger.Debug("demo jobs stepped", "step", step)
	}
}
