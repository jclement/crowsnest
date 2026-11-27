// Wiring for structured approvals and the guard (DESIGN.md 8.7): the guard's rules (written into
// every agent's guard.json), the risk classifier (built-ins plus ~/.config/crowsnest/risk.yaml),
// and the approvals service, whose changes move the job's state and feed Web Push. The policy's
// scope (which project, whether the job is live, the working root) is read here, and every call a
// rule allowed is journalled here: on the job's timeline and in the global journal.

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/projects"
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
	leads  atomic.Pointer[projects.Leads]
	// journal and projects are known before the service is built; either may be nil in tests.
	journal  *journal.Journal
	projects *projects.Service
}

// newApprovals builds the approvals service. An invalid risk.yaml stops the start: a typo must not
// silently loosen what needs a confirmation.
func newApprovals(cfg config.Config, db *store.Store, events *hub.Hub, rules *guard.Rules, wiring *approvalWiring, logger *slog.Logger) (*approvals.Service, error) {
	classifier, err := risk.Load(riskRulesPath(cfg))
	if err != nil {
		return nil, err
	}
	var service *approvals.Service
	service = approvals.New(approvals.Options{
		Store: db, Hub: events, Logger: logger, Classifier: classifier, Scope: wiring.scope,
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
			if jobService := wiring.jobs.Load(); jobService != nil && approval.JobID != "" {
				// The job service reads what is pending itself (Options.ApprovalPending), under its
				// lock, so callbacks that run out of order still leave the job right. After a
				// settle the cause names a call still waiting, not the one just settled.
				summary := approval.Summary
				if change != "requested" {
					summary = service.WaitingSummary(ctx, approval.JobID)
				}
				if err := jobService.SetApprovalWaiting(context.WithoutCancel(ctx), approval.JobID, change == "requested", summary); err != nil {
					logger.Warn("recording a job's approval wait", "job", approval.JobID, "err", err)
				}
			}
			if pusher := wiring.pusher.Load(); pusher != nil && change == "requested" {
				pusher.ApprovalRequested(approval)
			}
			if change == string(approvals.StatusAllowed) && approval.DecidedVia == approvals.ViaPolicy {
				wiring.journalAutoAllow(context.WithoutCancel(ctx), service, approval, logger)
			}
		},
	})
	return service, nil
}

// scope is the approval policy's view of a call (DESIGN.md 8.7, Policy): a job's project, whether
// it is still live and its working root (its worktree when isolated); for a lead's own calls,
// which hold no job, the lead's project and its directory. Anything else (the orchestrator's own
// calls) has no project, so only a job's remembered rules could apply.
func (w *approvalWiring) scope(ctx context.Context, jobID, paneID string) approvals.Scope {
	if service := w.jobs.Load(); service != nil && jobID != "" {
		job, err := service.Get(ctx, jobID)
		if err != nil {
			return approvals.Scope{}
		}
		scope := approvals.Scope{ProjectID: job.ProjectID, JobLive: !job.State.Terminal()}
		if job.AgentName != jobs.OrchestratorName {
			scope.Root = firstNonEmpty(job.WorkDir, job.Dir)
		}
		if w.projects != nil && job.ProjectID != 0 {
			if project, err := w.projects.ByID(ctx, job.ProjectID); err == nil {
				scope.ProjectName = project.Name
			}
		}
		return scope
	}
	if leads := w.leads.Load(); leads != nil && paneID != "" {
		if lead := leads.ByPane(paneID); lead != nil {
			return approvals.Scope{ProjectID: lead.Project.ID, ProjectName: lead.Project.Name, Root: lead.Project.Dir}
		}
	}
	return approvals.Scope{}
}

// journalAutoAllow records a call a rule allowed on the job's timeline and in the global journal,
// with the rules' patterns, so "why was this allowed" has an answer in both places.
func (w *approvalWiring) journalAutoAllow(ctx context.Context, service *approvals.Service, approval approvals.Approval, logger *slog.Logger) {
	var patterns []string
	for _, id := range approval.RuleIDs {
		if rule, err := service.GetRule(ctx, id); err == nil {
			patterns = append(patterns, rule.Kind+": "+rule.Pattern)
		}
	}
	word := "rule"
	if len(approval.RuleIDs) > 1 {
		word = "rules"
	}
	summary := "Auto-allowed by " + word + " " + strings.Join(approval.RuleIDs, ", ") + ": " + approval.Summary
	detail := map[string]any{"approval": approval.ID, "rule_ids": approval.RuleIDs, "patterns": patterns, "reason": approval.StatusReason, "risk": approval.Risk}
	if service := w.jobs.Load(); service != nil && approval.JobID != "" {
		if err := service.Journal(ctx, approval.JobID, jobs.EventHook, summary, detail); err != nil {
			logger.Warn("journalling an auto-allow on the job", "job", approval.JobID, "err", err)
		}
	}
	if w.journal != nil {
		detail["job"] = approval.JobID
		w.journal.Add(ctx, journal.KindApproval, summary, detail)
	}
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
