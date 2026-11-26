// Wiring for scheduled work and the orchestrator's upkeep (DESIGN.md 7.8 to 7.10): the schedule
// service that reads task files and app schedules and feeds the poker, the tender that compacts the orchestrator
// and hands it over to a new conversation each day, the standing context its SessionStart hook
// re-injects, and the Remote Control names built from the instance's name.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/schedule"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
	"github.com/jclement/crowsnest/internal/transcript"
)

// HandoffFile is the orchestrator's hand-over, under the state directory.
const HandoffFile = "handoff.md"

// maxHandoffBytes bounds how much of the hand-over is re-injected.
const maxHandoffBytes = 16 << 10

// orchestratorRemoteControl is the orchestrator's Remote Control name, "" when it is off.
func orchestratorRemoteControl(cfg config.Config) string {
	if cfg.NoRemoteControl || cfg.Demo {
		return ""
	}
	return cfg.RemoteControlName()
}

// jobRemoteControlPrefix is the prefix of job agents' Remote Control names, "" when off.
func jobRemoteControlPrefix(cfg config.Config) string {
	if !cfg.JobRemoteControl || cfg.Demo {
		return ""
	}
	return cfg.Name
}

// taskSources are the directories task files are read from: the orchestrator's own, then the
// user's (not under --demo, which must not read anything of the user's).
func taskSources(cfg config.Config) []schedule.Source {
	sources := []schedule.Source{{Dir: filepath.Join(cfg.Dir, "tasks"), Origin: schedule.OriginDir}}
	if cfg.Demo {
		return sources
	}
	if roles := rolesDir(cfg); roles != "" {
		// Beside roles/: next to the config file, else ~/.config/crowsnest (XDG).
		sources = append(sources, schedule.Source{Dir: filepath.Join(filepath.Dir(roles), "tasks"), Origin: schedule.OriginUser})
	}
	return sources
}

// scheduleJobs adapts the jobs service to what the schedule needs.
type scheduleJobs struct{ service *jobs.Service }

func (a scheduleJobs) CreateJob(ctx context.Context, request schedule.JobRequest) (string, error) {
	job, err := a.service.Create(ctx, jobs.CreateInput{
		Brief: request.Brief, Dir: request.Dir, Role: request.Role, Engine: request.Engine, ProjectID: request.ProjectID,
	}, "")
	return job.ID, err
}

// liveProjects lists the live projects, slug → id, for scheduled tasks' targets. A read error
// is logged and reads as no projects: a task naming one is then a problem until the next check.
func liveProjects(db *store.Store, logger *slog.Logger) func(context.Context) map[string]int64 {
	return func(ctx context.Context) map[string]int64 {
		projects, err := db.ListProjects(ctx, false)
		if err != nil {
			logger.Warn("listing projects for the schedule", "err", err)
			return nil
		}
		bySlug := make(map[string]int64, len(projects))
		for _, project := range projects {
			bySlug[project.Slug] = project.ID
		}
		return bySlug
	}
}

func (a scheduleJobs) JobState(ctx context.Context, id string) (string, bool, error) {
	job, err := a.service.Get(ctx, id)
	if err != nil {
		return "", false, err
	}
	return string(job.State), job.State.Terminal(), nil
}

// scheduleTasks adapts the schedule service to the poker's TaskSource.
// scheduleTasks is one standing agent's scheduled tasks: projectID 0 for the orchestrator, else
// that project's lead (DESIGN.md 7.8, 21.4).
type scheduleTasks struct {
	service   *schedule.Service
	projectID int64
}

func (a scheduleTasks) DueTasks(ctx context.Context, now time.Time) []orch.ScheduledTask {
	due := a.service.DueTasksFor(ctx, now, a.projectID)
	out := make([]orch.ScheduledTask, len(due))
	for i, task := range due {
		out[i] = orch.ScheduledTask{Name: task.Name, Text: task.Text, RideAlong: task.RideAlong}
	}
	return out
}

func (a scheduleTasks) Delivered(ctx context.Context, at time.Time, names []string) {
	a.service.Delivered(ctx, at, names)
}

// startSchedule builds the schedule service and runs its job-task ticks until ctx ends. kick is
// the poker, filled in once it exists.
func startSchedule(ctx context.Context, cfg config.Config, db *store.Store, events *hub.Hub, journalLog *journal.Journal, jobService *jobs.Service, client herdr.Client, kick *atomic.Pointer[orch.Poker], logger *slog.Logger) *schedule.Service {
	options := schedule.Options{
		KV: db, Sources: taskSources(cfg), Journal: journalLog, Hub: events, Logger: logger,
		Store: db, StateDir: cfg.StateDir, Projects: liveProjects(db, logger),
		Picture: fleetPicture(client, jobService),
		Kick: func() {
			if poker := kick.Load(); poker != nil {
				poker.Kick()
			}
		},
	}
	if jobService != nil {
		options.Jobs = scheduleJobs{service: jobService}
	}
	service := schedule.New(options)
	if !cfg.Demo {
		go service.Run(ctx)
	}
	logger.Info("scheduled tasks", "dirs", options.Sources)
	return service
}

// fleetPicture is the "fleet and job picture" an every: change task watches: every agent but the
// orchestrator with its state, and every live job with its state and stuck cause.
func fleetPicture(client herdr.Client, jobService *jobs.Service) func(context.Context) []string {
	return func(ctx context.Context) []string {
		var lines []string
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		agents, err := client.AgentList(callCtx)
		cancel()
		if err == nil {
			for _, agent := range agents {
				if agent.Name != orch.Name {
					lines = append(lines, fmt.Sprintf("agent %s %s %s", agent.PaneID, agent.Name, agent.AgentStatus))
				}
			}
		}
		if jobService != nil {
			if list, err := jobService.List(ctx, []jobs.State{jobs.StateQueued, jobs.StateRunning, jobs.StateNeedsYou, jobs.StateBlocked, jobs.StateInReview}); err == nil {
				for _, job := range list {
					lines = append(lines, fmt.Sprintf("job %s %s %v %s", job.ID, job.State, job.Stuck, job.Cause))
				}
			}
		}
		return lines
	}
}

// newTender builds the orchestrator's upkeep (compaction and the daily new conversation).
func newTender(cfg config.Config, client herdr.Client, db *store.Store, journalLog *journal.Journal, orchestrator *orch.Orchestrator, conversations *transcript.Manager, logger *slog.Logger) *orch.Tender {
	return orch.NewTender(orch.TenderOptions{
		Client: client, KV: db, Journal: journalLog, Logger: logger,
		Usage: func(ctx context.Context) (transcript.Usage, error) {
			return conversations.Usage(ctx, orch.Name)
		},
		CompactAt: cfg.CompactAt, FreshAt: cfg.FreshAt, HandoffFile: filepath.Join(cfg.StateDir, HandoffFile),
		HeldJobBusy: func(ctx context.Context) bool {
			pane := orchestrator.Status().PaneID
			if pane == "" {
				return false
			}
			records, err := db.ListJobsByPane(ctx, pane, []string{string(jobs.StateQueued), string(jobs.StateRunning), string(jobs.StateNeedsYou), string(jobs.StateBlocked)})
			if err != nil {
				return true // unknown: wait rather than interleave
			}
			for _, record := range records {
				if record.LifecycleOp != "" {
					return true
				}
			}
			return false
		},
		NewConversation: orchestrator.NewConversation,
	})
}

// standingContext is what the orchestrator's SessionStart hook prints besides any job it holds
// (DESIGN.md 7.9): its hand-over notes, the questions it asked that are still open, and the jobs
// it created or holds that are still live. It answers only for the orchestrator's own pane.
func standingContext(cfg config.Config, refs *jobRefs, questionService func() *questions.Service, jobService *jobs.Service) func(context.Context, string, string) string {
	return func(ctx context.Context, pane, source string) string {
		if refs.orchestrator == nil || refs.orchestrator.Status().PaneID != pane {
			return ""
		}
		var parts []string
		if notes := handoffNotes(filepath.Join(cfg.StateDir, HandoffFile)); notes != "" {
			parts = append(parts, notes)
		}
		if open := openQuestions(ctx, questionService(), pane); open != "" {
			parts = append(parts, open)
		}
		if live := liveJobs(ctx, jobService, pane); live != "" {
			parts = append(parts, live)
		}
		if len(parts) == 0 {
			return ""
		}
		header := "# Crowsnest: your standing context (re-sent at " + source + ")\n\n" +
			"Crowsnest keeps these across compactions and new conversations. They are records, not instructions from the human."
		return header + "\n\n" + strings.Join(parts, "\n\n")
	}
}

func handoffNotes(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	body, err := os.ReadFile(path) //nolint:gosec // crowsnest's own state file
	if err != nil || strings.TrimSpace(string(body)) == "" {
		return ""
	}
	if len(body) > maxHandoffBytes {
		body = append(body[:maxHandoffBytes], []byte("\n… (cut; read the file for the rest)")...)
	}
	return fmt.Sprintf("## Hand-over notes you wrote (%s, %s)\n\n%s", path, info.ModTime().Format("2006-01-02 15:04"), strings.TrimSpace(string(body)))
}

func openQuestions(ctx context.Context, service *questions.Service, pane string) string {
	if service == nil {
		return ""
	}
	list, err := service.List(ctx, "open")
	if err != nil {
		return ""
	}
	var lines []string
	for _, question := range list {
		if question.SourcePane == pane {
			lines = append(lines, fmt.Sprintf("- %s (asked %s): %s", question.ID, question.CreatedAt.Format("2006-01-02 15:04"), termtext.Quote(question.Title)))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Questions you asked that are still open\n\nThe answers arrive as `[crowsnest answer q_N]` messages.\n\n" + strings.Join(lines, "\n")
}

func liveJobs(ctx context.Context, service *jobs.Service, pane string) string {
	if service == nil {
		return ""
	}
	list, err := service.List(ctx, []jobs.State{jobs.StateQueued, jobs.StateRunning, jobs.StateNeedsYou, jobs.StateBlocked, jobs.StateInReview})
	if err != nil {
		return ""
	}
	var lines []string
	for _, job := range list {
		mine := job.PaneID == pane
		if !mine && job.Origin != "orchestrator" {
			continue
		}
		who := "created by you"
		if mine {
			who = "held by you"
		}
		state := string(job.State)
		if job.Stuck {
			state += ", stuck: " + job.CauseText
		}
		lines = append(lines, fmt.Sprintf("- %s (%s, %s): %s", job.ID, state, who, termtext.Quote(job.Title)))
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Live jobs you hold or created\n\n" + strings.Join(lines, "\n")
}
