// Wiring for Web Push (DESIGN.md 8.5): the push service (VAPID keys in the database, the
// subscriptions, the settings) and the watcher that turns hub events and newly asked questions
// into notices. Under --demo the service also accepts http:// endpoints on loopback, so the
// hermetic browser suite can stand up a fake push service; a demo instance has no shell to guard.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/jclement/crowsnest/internal/actions"
	"github.com/jclement/crowsnest/internal/attention"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/push"
	"github.com/jclement/crowsnest/internal/store"
)

// startPush builds the push service and starts its watcher, which runs until ctx ends.
func startPush(ctx context.Context, cfg config.Config, db *store.Store, events *hub.Hub, ctl *controls, describe push.Describer, logger *slog.Logger) (*push.Service, *push.Watcher, error) {
	service, err := push.New(ctx, push.Options{Store: db, Logger: logger, AllowLoopbackHTTP: cfg.Demo, Name: cfg.Name})
	if err != nil {
		return nil, nil, fmt.Errorf("starting Web Push: %w", err)
	}
	options := push.WatcherOptions{Sender: service, Hub: events, Logger: logger, Tokens: &actions.Tokens{Store: db}, Describe: describe}
	if ctl != nil {
		options.Route = ctl.route // presence and do not disturb (DESIGN.md 6.14)
	}
	watcher := push.NewWatcher(options)
	go watcher.Run(ctx)
	return service, watcher, nil
}

// pushDescriber tells the push watcher where an item belongs (DESIGN.md 8.5, decision-grade
// notices): the job (given, or the one in the asking pane) and its project, or the project whose
// lead is in that pane. Lookups that fail leave the item unfiled; a notice is never held for this.
func pushDescriber(jobService *jobs.Service, wiring *projectWiring) push.Describer {
	return func(ctx context.Context, jobID, paneID string) push.Where {
		var where push.Where
		if jobID == "" && paneID != "" {
			jobID = jobService.JobInPane(ctx, paneID)
		}
		if jobID != "" {
			if job, err := jobService.Get(ctx, jobID); err == nil {
				where.JobID, where.JobTitle = job.ID, job.Title
				where.JobStopped = attention.JobStoppedOnQuestions(&job.Job) // the queue's rule (R9-C12)
				if job.ProjectID != 0 {
					if project, err := wiring.service.Get(ctx, strconv.FormatInt(job.ProjectID, 10)); err == nil {
						where.Project = project.Name
					}
				}
				return where
			}
		}
		if project, ok := wiring.leadProjectInPane(paneID); ok {
			where.Project = project.Name
		}
		return where
	}
}
