// Wiring for pokes and the backstop (M8, DESIGN.md 7.4 and 6.3): the poker that tells the
// orchestrator what changed, fed by the hub's `job` and `question` events and by the standing
// list of open work at each timer tick; the herdr-side backstop that nudges job agents without
// hooks (it runs in the poker's loop, through the jobs service). Under --demo pokes go out only
// for "Poke now" and the backstop is off (the demo's jobs are scripted).

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/voice"
)

// pokeServices are what the poker reads.
type pokeServices struct {
	orchestrator *orch.Orchestrator
	jobs         *jobs.Service
	questions    *questions.Service
	talk         *voice.Service
	journal      *journal.Journal
	alert        func(text string)
}

// startPoker builds the poker, feeds it from the hub, and runs it until ctx ends.
func startPoker(ctx context.Context, cfg config.Config, db *store.Store, events *hub.Hub, services pokeServices, client herdr.Client, logger *slog.Logger) *orch.Poker {
	options := orch.PokerOptions{
		Client: client, KV: db, Journal: services.journal, Hub: events, Logger: logger,
		Status:   services.orchestrator.Status,
		Interval: cfg.PokeInterval, Standing: standingWork(services), Alert: services.alert,
		ManualOnly: cfg.Demo,
	}
	if services.talk != nil {
		options.Talk = services.talk.TalkOn
	}
	if !cfg.Demo && services.jobs != nil {
		options.Backstop = services.jobs
	}
	poker := orch.NewPoker(options)
	go feedPoker(ctx, events, poker, services, logger)
	go poker.Run(ctx)
	logger.Info("pokes on", "interval", cfg.PokeInterval, "backstop", options.Backstop != nil, "manual_only", cfg.Demo)
	return poker
}

// feedPoker turns hub events into poke reasons: a job that finished or got stuck, a question that
// was settled. A subscription the hub closes (it fell behind) is renewed.
func feedPoker(ctx context.Context, events *hub.Hub, poker *orch.Poker, services pokeServices, logger *slog.Logger) {
	for ctx.Err() == nil {
		sub := events.Subscribe(0)
		for event := range sub.Events() {
			handlePokeEvent(ctx, event, poker, services, logger)
		}
		sub.Close()
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond): // a closed hub must not spin this loop
		}
	}
}

func handlePokeEvent(ctx context.Context, event hub.Event, poker *orch.Poker, services pokeServices, logger *slog.Logger) {
	switch data := event.Data.(type) {
	case jobs.HubEvent:
		if !data.State.Terminal() && !data.Stuck {
			return
		}
		detail, err := services.jobs.Get(ctx, data.ID)
		if err != nil {
			logger.Debug("poker could not read a job", "job", data.ID, "err", err)
			return
		}
		poker.JobChanged(ctx, orch.JobInfo{
			ID: data.ID, Title: data.Title, State: string(data.State), Stuck: data.Stuck,
			Cause: detail.CauseText, PaneID: detail.PaneID,
		})
	case questions.Event:
		if data.Change != "answered" && data.Change != "expired" && data.Change != "cancelled" {
			return
		}
		question, err := services.questions.Get(ctx, data.ID)
		if err != nil {
			return
		}
		poker.QuestionSettled(ctx, orch.QuestionInfo{
			ID: question.ID, Title: question.Title, Status: string(question.Status), Kind: string(question.Kind), SourcePane: question.SourcePane,
		})
	}
}

// standingWork lists what is open right now, one line each, for the timer: stuck jobs, jobs that
// need the human, and the open question count. The timer pokes only when this list changed.
func standingWork(services pokeServices) func(context.Context) []string {
	return func(ctx context.Context) []string {
		var lines []string
		if services.jobs != nil {
			list, err := services.jobs.List(ctx, []jobs.State{jobs.StateRunning, jobs.StateNeedsYou, jobs.StateBlocked})
			if err == nil {
				for _, job := range list {
					switch {
					case job.Stuck:
						lines = append(lines, fmt.Sprintf("job %s is stuck (%s): %s", job.ID, job.CauseText, job.Title))
					case job.State == jobs.StateNeedsYou:
						lines = append(lines, fmt.Sprintf("job %s needs the human: %s", job.ID, job.Title))
					case job.State == jobs.StateBlocked:
						lines = append(lines, fmt.Sprintf("job %s is blocked: %s", job.ID, job.Title))
					}
				}
			}
		}
		if services.questions != nil {
			if summary, err := services.questions.Summary(ctx); err == nil && summary.Open > 0 {
				lines = append(lines, fmt.Sprintf("%d open question(s) for the human, %d blocking", summary.Open, summary.Blocking))
			}
		}
		return lines
	}
}
