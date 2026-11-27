// Wiring for structured approvals and the guard (DESIGN.md 8.7): the guard's rules (written into
// every agent's guard.json), the risk classifier (built-ins plus ~/.config/crowsnest/risk.yaml),
// and the approvals service, whose changes move the job's state and feed Web Push.

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/push"
	"github.com/jclement/crowsnest/internal/risk"
	"github.com/jclement/crowsnest/internal/store"
)

// guardRules is what every agent's guard hook enforces: its own session, the protected hosts
// (--protected-host plus the hosts file when it exists) and --protected-branch.
func guardRules(cfg config.Config, logger *slog.Logger) *guard.Rules {
	hosts := append([]string(nil), cfg.ProtectedHosts...)
	fromFile, err := guard.ReadHostsFile(cfg.ProtectedHostsFile)
	if err != nil {
		logger.Warn("reading the protected hosts file; using --protected-host alone", "file", cfg.ProtectedHostsFile, "err", err)
	}
	hosts = append(hosts, fromFile...)
	return &guard.Rules{Session: cfg.Session, ProtectedHosts: hosts, ProtectedBranches: cfg.ProtectedBranches}
}

// riskRulesPath is ~/.config/crowsnest/risk.yaml (next to the config file).
func riskRulesPath(cfg config.Config) string {
	if cfg.ConfigFile != "" {
		return filepath.Join(filepath.Dir(cfg.ConfigFile), "risk.yaml")
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "crowsnest", "risk.yaml")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "crowsnest", "risk.yaml")
	}
	return ""
}

// approvalWiring holds the late-bound collaborators the approvals' change callback needs.
type approvalWiring struct {
	jobs   atomic.Pointer[jobs.Service]
	pusher atomic.Pointer[push.Watcher]
}

// newApprovals builds the approvals service. An invalid risk.yaml stops the start: a typo must not
// silently loosen what needs a confirmation.
func newApprovals(cfg config.Config, db *store.Store, events *hub.Hub, rules *guard.Rules, wiring *approvalWiring, logger *slog.Logger) (*approvals.Service, error) {
	classifier, err := risk.Load(riskRulesPath(cfg))
	if err != nil {
		return nil, err
	}
	return approvals.New(approvals.Options{
		Store: db, Hub: events, Logger: logger, Classifier: classifier,
		RiskContext: func(ctx context.Context, jobID string) risk.Context {
			riskContext := risk.Context{ProtectedHosts: rules.ProtectedHosts}
			if service := wiring.jobs.Load(); service != nil && jobID != "" {
				if job, err := service.Get(ctx, jobID); err == nil && job.AgentName != jobs.OrchestratorName {
					riskContext.JobRoot = firstNonEmpty(job.WorkDir, job.Dir)
				}
			}
			return riskContext
		},
		OnChange: func(ctx context.Context, approval approvals.Approval, change string) {
			if service := wiring.jobs.Load(); service != nil && approval.JobID != "" {
				// The job service reads what is pending itself (Options.ApprovalPending), under its
				// lock, so callbacks that run out of order still leave the job right.
				if err := service.SetApprovalWaiting(context.WithoutCancel(ctx), approval.JobID, change == "requested", approval.Summary); err != nil {
					logger.Warn("recording a job's approval wait", "job", approval.JobID, "err", err)
				}
			}
			if pusher := wiring.pusher.Load(); pusher != nil && change == "requested" {
				pusher.ApprovalRequested(approval)
			}
		},
	}), nil
}

// runApprovals recovers what a previous process left pending, then sweeps (expiry, hooks that
// stopped polling) and reconciles against the screens (a dialog answered in the terminal). The
// --demo approvals are seeded later, once the jobs they name exist (startDemoInbox).
func runApprovals(ctx context.Context, service *approvals.Service, cache *fleet.Cache, logger *slog.Logger) {
	service.RecoverAtStart(ctx)
	go service.Run(ctx, func(ctx context.Context, paneID string) approvals.Screen {
		snapshot, err := cache.Fleet(ctx)
		if err != nil {
			return approvals.Screen{}
		}
		for _, agent := range snapshot.Agents {
			if agent.PaneID == paneID {
				return approvals.Screen{Known: true, Dialog: agent.NeedsYou}
			}
		}
		return approvals.Screen{Known: true} // the agent is gone
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
