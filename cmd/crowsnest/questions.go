// Wiring for the questions inbox and the agent socket (M6): the question service with its expiry
// sweeper, the deliverer that types answers back into askers' panes, --notify-url, the unix
// socket server agents reach through `crowsnest ask|notify|say|mcp`, and under --demo a seeded
// inbox with a question asked every so often.

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// startQuestions builds the question service, starts its background work and the agent socket,
// and returns the service and the socket path. Everything stops when ctx ends.
func startQuestions(ctx context.Context, cfg config.Config, client herdr.Client, db *store.Store, events *hub.Hub, jobService *jobs.Service, socket string, logger *slog.Logger) (*questions.Service, error) {
	var deliverer *questions.Deliverer
	var notifyAsked func(questions.Question)
	if cfg.NotifyURL != "" {
		notifyAsked = questions.NewURLNotifier(cfg.NotifyURL, nil, logger)
	}
	options := questions.Options{
		Store: db, Hub: events, Logger: logger,
		OnSettled: func(question questions.Question) {
			deliverer.Kick()
			jobQuestionChanged(ctx, jobService, question, string(question.Status))
		},
		OnAsked: func(question questions.Question) {
			if notifyAsked != nil {
				notifyAsked(question)
			}
			jobQuestionChanged(ctx, jobService, question, "asked")
		},
	}
	service := questions.New(options)
	deliverer = questions.NewDeliverer(questions.DelivererOptions{
		Store: db, Prompt: questions.HerdrPrompter(client), Logger: logger,
		OnDelivered: func(id int64) {
			if question, err := service.Get(ctx, questions.FormatID(id)); err == nil {
				events.Publish(hub.Event{Type: "question", Data: questions.Event{
					ID: question.ID, Status: question.Status, Title: question.Title, Kind: question.Kind, Urgency: question.Urgency, Change: "delivered",
				}})
			}
		},
	})
	go deliverer.Run(ctx)
	go service.RunSweeper(ctx, 0)

	listener, err := agentapi.Listen(ctx, socket)
	if err != nil {
		return nil, err
	}
	server := agentapi.NewServer(agentapi.ServerOptions{Questions: service, Jobs: jobService, Hub: events, Logger: logger, Version: version})
	go func() {
		if err := server.Serve(ctx, listener); err != nil {
			logger.Error("agent socket stopped", "err", err)
		}
	}()
	logger.Info("agent API listening", "socket", socket)

	if demo, ok := client.(*fake.Demo); ok {
		if err := startDemoQuestions(ctx, service, demo, logger); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// jobQuestionChanged tells the job a question was asked or settled under it. It runs in the
// background: the questions service calls its hooks inline and they must not block.
func jobQuestionChanged(ctx context.Context, jobService *jobs.Service, question questions.Question, change string) {
	if jobService == nil || question.JobID == "" {
		return
	}
	go jobService.QuestionChanged(context.WithoutCancel(ctx), question.JobID, question.ID, question.Title, change, question.Wait)
}

// startDemoQuestions seeds the demo inbox and keeps asking.
func startDemoQuestions(ctx context.Context, service *questions.Service, demo *fake.Demo, logger *slog.Logger) error {
	panes := questions.DemoPanes{
		Orchestrator: demo.PaneOf(fake.DemoOrchestrator), Builder: demo.PaneOf(fake.DemoBuilder),
		Reviewer: demo.PaneOf(fake.DemoReviewer), Docs: demo.PaneOf(fake.DemoDocs),
	}
	if err := questions.SeedDemo(ctx, service, panes); err != nil {
		return fmt.Errorf("seeding the demo questions: %w", err)
	}
	go questions.RunDemo(ctx, service, panes, 0, logger)
	return nil
}
