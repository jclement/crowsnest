// Wiring for the questions inbox and the agent socket (M6): the question service with its expiry
// sweeper, the deliverer that types answers back into askers' panes, --notify-url, the unix
// socket server agents reach through `crowsnest ask|notify|say|mcp`, and under --demo a seeded
// inbox with a question asked every so often.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/instructions"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/push"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// startQuestions builds the question service, starts its background work and the agent socket,
// and returns the service and a stop function. Everything stops when ctx ends or stop is called;
// stop returns once the deliverer and the sweeper have finished, and serve calls it before the
// database closes, so an attempt cut short by shutdown can still record its outcome (R1b-C08).
func startQuestions(ctx context.Context, cfg config.Config, client herdr.Client, db *store.Store, identity *agentid.Registry, events *hub.Hub, jobService *jobs.Service, speaker agentapi.Speaker, pusher *push.Watcher, socket string, sessionContext func(context.Context, string, string) string, files *attachWiring, approvalService *approvals.Service, layers *instructions.Service, subjectOf func(context.Context, string) string, projectsFor agentapi.ProjectReader, logger *slog.Logger) (*questions.Service, func(), error) {
	var deliverer *questions.Deliverer
	var notifyAsked func(questions.Question)
	if cfg.NotifyURL != "" {
		notifyAsked = questions.NewURLNotifier(cfg.NotifyURL, nil, logger)
	}
	options := questions.Options{
		Store: db, Hub: events, Logger: logger, Attachments: files.service,
		OnSettled: func(question questions.Question) {
			deliverer.Kick()
			jobQuestionChanged(ctx, jobService, question, string(question.Status))
			if jobID, ok := jobs.IsApprovalQuestion(question); ok && jobService != nil {
				go jobService.ApprovalSettled(context.WithoutCancel(ctx), jobID) // R2-H1
			}
		},
		OnAsked: func(question questions.Question) {
			if notifyAsked != nil {
				notifyAsked(question)
			}
			if pusher != nil {
				pusher.QuestionAsked(question) // Web Push, the second sink (DESIGN.md 8.5)
			}
			jobQuestionChanged(ctx, jobService, question, "asked")
		},
	}
	service := questions.New(options)
	deliverer = questions.NewDeliverer(questions.DelivererOptions{
		Store: db, Prompt: questions.HerdrPrompter(client), Logger: logger, AnswerFiles: files.answerFiles,
		OnDelivered: func(id int64) {
			if question, err := service.Get(ctx, questions.FormatID(id)); err == nil {
				events.Publish(hub.Event{Type: "question", Data: questions.Event{
					ID: question.ID, Status: question.Status, Title: question.Title, Kind: question.Kind, Urgency: question.Urgency, Change: "delivered",
				}})
			}
		},
	})
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	stop := func() { cancel(); workers.Wait() }
	workers.Go(func() { deliverer.Run(ctx) })
	workers.Go(func() { service.RunSweeper(ctx, 0) })

	listener, err := agentapi.Listen(ctx, socket)
	if err != nil {
		return nil, stop, err
	}
	server := agentapi.NewServer(agentapi.ServerOptions{Questions: service, Identity: identity, Jobs: jobService, Hub: events, Speaker: speaker, Logger: logger, Version: version, SessionContext: sessionContext,
		Attachments: files.service, AttachRoots: files.roots, Approvals: approvalService, Instructions: layers, SubjectOf: subjectOf, Projects: projectsFor})
	go func() {
		if err := server.Serve(ctx, listener); err != nil {
			logger.Error("agent socket stopped", "err", err)
		}
	}()
	logger.Info("agent API listening", "socket", socket)

	if demo, ok := client.(*fake.Demo); ok {
		if err := startDemoQuestions(ctx, service, demo, logger); err != nil {
			return nil, stop, err
		}
		if err := seedDemoAttachments(ctx, files, service, cfg.StateDir); err != nil {
			logger.Warn("seeding the demo attachments", "err", err)
		}
	}
	return service, stop, nil
}

// agentSocketPath is where the agent socket goes: under $XDG_RUNTIME_DIR, except under --demo,
// whose socket stays in its throwaway state directory so a demo never collides with, or refuses
// to start beside, a real instance of the same --session (R1 L2, R1b N11).
func agentSocketPath(cfg config.Config) string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if cfg.Demo {
		runtimeDir = ""
	}
	return agentapi.SocketPath(runtimeDir, cfg.StateDir, cfg.Session)
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
