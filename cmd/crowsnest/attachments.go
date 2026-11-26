// Wiring for attachments (DESIGN.md 8.6): the store under <data-dir>/attachments, the inbox
// layout under the state directory, the adapters the jobs service, the inbox and the agent socket
// use, the directories an agent may attach files from, and the watcher that releases a job's or
// a question's files when it settles.

package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// megabyte converts the config's MB settings.
const megabyte = 1 << 20

// AttachmentEvent is the data of an `attachment` hub event: an owner's files changed
// ("job:j_3", "question:q_7", "orchestrator:", "pending:"), so the web app refetches its lists.
type AttachmentEvent struct {
	Owner string `json:"owner"`
}

// attachWiring holds the attachment service and what it needs from the services built after it.
type attachWiring struct {
	service *attach.Service
	layout  attach.Layout
	cfg     config.Config
	refs    *jobRefs
	jobs    *jobs.Service // set once the jobs service exists
	logger  *slog.Logger
}

// newAttachments builds the service. refs gives the orchestrator's pane once it exists.
func newAttachments(cfg config.Config, db *store.Store, events *hub.Hub, refs *jobRefs, logger *slog.Logger) (*attachWiring, error) {
	service, err := attach.New(attach.Options{
		Store: db, Dir: filepath.Join(cfg.DataDir, "attachments"), Logger: logger,
		Limits: attach.Limits{MaxFileBytes: int64(cfg.AttachmentMaxMB) * megabyte, QuotaBytes: int64(cfg.AttachmentQuotaMB) * megabyte},
		Changed: func(owner string) {
			events.Publish(hub.Event{Type: "attachment", Data: AttachmentEvent{Owner: owner}})
		},
	})
	if err != nil {
		return nil, err
	}
	return &attachWiring{service: service, layout: attach.Layout{StateDir: cfg.StateDir}, cfg: cfg, refs: refs, logger: logger}, nil
}

// ---- jobs.BriefAttacher -----------------------------------------------------------------

// CheckBrief verifies a new job's uploads can be given to it.
func (a *attachWiring) CheckBrief(ctx context.Context, ids []string) error {
	return a.service.CheckPending(ctx, ids)
}

// AttachBrief copies a new job's uploads into its inbox (the orchestrator's, for a job with no
// directory, which the orchestrator holds).
func (a *attachWiring) AttachBrief(ctx context.Context, job jobs.Job, ids []string) error {
	_, err := a.service.Deliver(ctx, ids, a.layout.Job(job.ID, job.Dir == ""), attach.RoleBrief, "")
	return err
}

// BriefLines are the "Attached: …" lines the brief file lists.
func (a *attachWiring) BriefLines(ctx context.Context, jobID string) []string {
	list, err := a.service.List(ctx, attach.OwnerJob, jobID, attach.RoleBrief)
	if err != nil {
		a.logger.Warn("listing a brief's attachments", "job", jobID, "err", err)
	}
	return attach.Lines(list)
}

// answerFiles are the files the human sent with an answer, with the asker's copies.
func (a *attachWiring) answerFiles(ctx context.Context, questionID string) []attach.Attachment {
	list, err := a.service.ListByRef(ctx, attach.RoleAnswer, questionID)
	if err != nil {
		a.logger.Warn("listing an answer's attachments", "question", questionID, "err", err)
	}
	return list
}

// roots are the directories a caller may attach files from: the orchestrator's working
// directory and state, a job's working directory (its worktree), its requested directory, and
// its job directory under the state directory. A caller crowsnest cannot place gets none.
func (a *attachWiring) roots(ctx context.Context, caller questions.Caller) []string {
	var roots []string
	add := func(dirs ...string) {
		for _, dir := range dirs {
			if dir != "" {
				roots = append(roots, dir)
			}
		}
	}
	if orchestrator := a.refs.orchestrator; orchestrator != nil && caller.PaneID != "" && orchestrator.Status().PaneID == caller.PaneID {
		add(a.cfg.Dir, filepath.Join(a.cfg.StateDir, "orchestrator"))
	}
	if caller.JobID != "" && a.jobs != nil {
		if job, err := a.jobs.Get(ctx, caller.JobID); err == nil {
			add(job.WorkDir, job.Dir, filepath.Join(a.cfg.StateDir, "jobs", job.ID))
		}
	}
	return roots
}

// ---- Upkeep ------------------------------------------------------------------------------

// run sweeps (abandoned uploads, old conversation files, the quota) and releases the files of
// jobs and questions as they settle, until ctx ends.
func (a *attachWiring) run(ctx context.Context, events *hub.Hub) {
	go a.service.Run(ctx)
	for ctx.Err() == nil {
		subscription := events.Subscribe(hub.DefaultBuffer)
		a.drain(ctx, subscription)
		subscription.Close()
	}
}

func (a *attachWiring) drain(ctx context.Context, subscription *hub.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-subscription.Events():
			if !open {
				return
			}
			a.release(ctx, event)
		}
	}
}

// release reacts to a job reaching done, failed or cancelled, and a question settling.
func (a *attachWiring) release(ctx context.Context, event hub.Event) {
	var kind, id string
	switch data := event.Data.(type) {
	case jobs.HubEvent:
		if !data.State.Terminal() {
			return
		}
		kind, id = attach.OwnerJob, data.ID
	case questions.Event:
		if data.Status == questions.StatusOpen {
			return
		}
		kind, id = attach.OwnerQuestion, data.ID
	default:
		return
	}
	if err := a.service.Release(ctx, kind, id); err != nil {
		a.logger.Warn("releasing attachments", "owner", kind+":"+id, "err", err)
	}
}
