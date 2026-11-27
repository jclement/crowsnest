// job_adopt (DESIGN.md 7.10): a standing agent records work the human gave it in conversation
// (the Claude app through Remote Control, or Chat) as a crowsnest job it holds itself. No agent
// is started and nothing is typed: the job is born held, exactly as dispatchTo leaves a job it
// gave a standing agent (its pane and name, running, brief sent), so the supervisor, job_update,
// done and fail treat it like any other held job. The orchestrator's is Unfiled, a lead's is in
// its project. A job agent is refused: it already has its job.

package jobs

import (
	"context"
	"strings"

	"github.com/jclement/crowsnest/internal/store"
)

// AdoptInput is job_adopt's: a title and the brief, in the agent's words.
type AdoptInput struct {
	Title string `json:"title"`
	Brief string `json:"brief"`
}

// FromConversation marks an adopted job's created and launch events (detail "from"), so its
// timeline says where the work came from.
const FromConversation = "conversation"

// AdoptForStanding records input as a job held by the standing agent in caller's pane and
// returns it running. A standing agent holds one live job at a time (6.1), so one that already
// holds a job is told to finish it or hand this work out with job_create.
func (s *Service) AdoptForStanding(ctx context.Context, input AdoptInput, caller AgentCaller) (Job, error) {
	if caller.JobID != "" {
		return Job{}, invalid("job_adopt is for the orchestrator and project leads; you are a job agent and already hold %s: report on it with job_update, or delegate with job_create", caller.JobID)
	}
	agent, ok := s.standingOf(caller.PaneID)
	if !ok {
		return Job{}, invalid("only the orchestrator and project leads can record work as their own job")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return Job{}, invalid("title is required: one line naming the work")
	}
	record, err := s.newRecord(CreateInput{Brief: title + "\n\n" + strings.TrimSpace(input.Brief)}, "")
	if err != nil {
		return Job{}, err
	}
	now := s.opts.Now()
	record.ProjectID, record.PaneID, record.AgentName = agent.ProjectID, agent.PaneID, agent.Name
	// The brief is the agent's words, so the job is the agent's, not the human's (R6-S11).
	record.Origin = OriginOrchestrator
	if agent.ProjectID != 0 {
		record.Origin = agent.Name
	}
	record.State, record.BriefSent = string(StateRunning), true
	record.StartedAt, record.LastActivityAt = now, now
	record.Progress = "Recorded from conversation"
	if err := s.insertHeld(ctx, &record, agent); err != nil {
		return Job{}, err
	}
	job := s.toJob(record, 0)
	s.publish(job, "created")
	return job, nil
}

// insertHeld stores an adopted job, refusing when the agent already holds a live one; the check
// and the insert share s.mu so two adoptions cannot both pass it.
func (s *Service) insertHeld(ctx context.Context, record *store.JobRecord, agent StandingAgent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := []string{string(StateQueued), string(StateRunning), string(StateNeedsYou), string(StateBlocked)}
	held, err := s.opts.Store.CountJobsHeldBy(ctx, agent.Name, live)
	if err != nil {
		return err
	}
	if held > 0 {
		return invalid("you already hold a live job; finish it with done (or fail) first, or hand this work out with job_create")
	}
	if err := s.insertLocked(ctx, record); err != nil {
		return err
	}
	var c change
	c.event(EventLaunch, "Recorded from conversation; held by "+agent.Name, map[string]any{"pane": agent.PaneID, "from": FromConversation})
	return s.save(ctx, *record, c)
}
