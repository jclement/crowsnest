// The day-thirty wiring (DESIGN.md 4.17 to 4.21): the httpd.Ops implementation (backup download,
// update notice, storage and clean-up, the summary other machines read), the retention service,
// the scheduled backup, and the reconciliation a start runs after a restore before anything is
// dispatched.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/backup"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/retention"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/sysstat"
)

// releaseURLBase is where a version's release notes are.
const releaseURLBase = "https://github.com/jclement/crowsnest/releases/tag/"

// opsWiring implements httpd.Ops over the running services.
type opsWiring struct {
	cfg       func() config.Config
	db        *store.Store
	retention *retention.Service
	jobs      *jobs.Service
	questions func() *questions.Service
	approvals *approvals.Service
	orch      *orch.Orchestrator
	stats     *sysstat.Sampler
	hold      restoreHold
}

func (o *opsWiring) RestoreHold(ctx context.Context) *httpd.RestoreHold { return o.hold.state(ctx) }

func (o *opsWiring) ResumeAfterRestore(ctx context.Context) error { return o.hold.resume(ctx) }

func (o *opsWiring) BackupName() string {
	return fmt.Sprintf("crowsnest-%s-%s%s", o.cfg().Session, backup.Stamp(time.Now()), backupSuffix)
}

func (o *opsWiring) WriteBackup(ctx context.Context, w io.Writer) error {
	_, err := backup.Write(ctx, backupSource(o.cfg(), o.db, "download"), w)
	return err
}

func (o *opsWiring) Updated(ctx context.Context) *httpd.UpdateNotice {
	raw, err := o.db.GetKV(ctx, backup.UpdatedFromKey)
	if err != nil {
		return nil
	}
	var notice httpd.UpdateNotice
	if json.Unmarshal([]byte(raw), &notice) != nil || notice.To == "" {
		return nil
	}
	if strings.HasPrefix(notice.To, "v") && !strings.ContainsAny(notice.To, "-+") {
		notice.ReleaseURL = releaseURLBase + notice.To
	}
	return &notice
}

func (o *opsWiring) DismissUpdated(ctx context.Context) error {
	err := o.db.DeleteKV(ctx, backup.UpdatedFromKey)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

func (o *opsWiring) Storage(ctx context.Context) (retention.Storage, error) {
	return o.retention.Storage(ctx)
}

func (o *opsWiring) Cleanup(ctx context.Context, dryRun bool, confirm []string) (retention.Report, error) {
	if dryRun {
		return o.retention.Plan(ctx)
	}
	return o.retention.Run(ctx, confirm)
}

func (o *opsWiring) ArchivedJobs(ctx context.Context) (map[int64]bool, error) {
	return o.db.ArchivedJobIDs(ctx)
}

func (o *opsWiring) SearchJobs(ctx context.Context, query string, archivedOnly bool) ([]int64, error) {
	return o.db.SearchJobIDs(ctx, query, archivedOnly, 100)
}

// Summary gathers the headline figures; a part that cannot be read is left at zero rather than
// failing the whole answer (another machine is only showing a card).
func (o *opsWiring) Summary(ctx context.Context) (httpd.Summary, error) {
	cfg := o.cfg()
	summary := httpd.Summary{Name: cfg.Name, Version: version, Jobs: map[string]int{}, At: time.Now().UTC().Format(time.RFC3339)}
	if service := o.questions(); service != nil {
		if inbox, err := service.Summary(ctx); err == nil {
			summary.Questions, summary.Blocking = inbox.Open, inbox.Blocking
		}
	}
	if o.approvals != nil {
		if pending, err := o.approvals.List(ctx, []approvals.Status{approvals.StatusPending}, 200); err == nil {
			summary.Approvals = len(pending)
		}
	}
	if o.jobs != nil {
		if list, err := o.jobs.List(ctx, nil); err == nil {
			for _, job := range list {
				switch job.State {
				case jobs.StateDone, jobs.StateFailed, jobs.StateCancelled:
				default:
					summary.Jobs[string(job.State)]++
				}
			}
		}
	}
	summary.NeedsYou, summary.Running = summary.Jobs[string(jobs.StateNeedsYou)], summary.Jobs[string(jobs.StateRunning)]
	summary.Attention = summary.Questions + summary.Approvals + summary.NeedsYou
	if o.orch != nil {
		summary.Orchestrator = string(o.orch.Status().State)
	}
	summary.Paused = pausedNow(ctx, o.db)
	if o.stats != nil {
		if latest := o.stats.Stats(ctx).Latest; latest != nil {
			summary.Stats = &httpd.SummaryStats{CPU: latest.CPU, MemUsed: latest.MemUsed, MemTotal: latest.MemTotal, Load1: latest.Load1}
		}
	}
	return summary, nil
}

// pausedNow reads the pause switch's stored state (kv pause.state).
func pausedNow(ctx context.Context, db *store.Store) bool {
	raw, err := db.GetKV(ctx, backup.PauseKey)
	if err != nil {
		return false
	}
	var state struct {
		Paused bool `json:"paused"`
	}
	return json.Unmarshal([]byte(raw), &state) == nil && state.Paused
}

// newRetention builds the retention service over the live settings.
func newRetention(cfg func() config.Config, db *store.Store, journalLog *journal.Journal, logger *slog.Logger) *retention.Service {
	base := cfg()
	return retention.New(retention.Options{
		Store: db, AttachmentsDir: base.DataDir + "/attachments", StateDir: base.StateDir, DataDir: base.DataDir,
		DatabasePath: base.DatabasePath(), BackupDir: base.BackupDir(), Journal: journalLog, Logger: logger,
		Policy: func() retention.Policy {
			current := cfg()
			return retention.Policy{ArchiveAfterDays: current.ArchiveAfterDays, DeleteAfterDays: current.DeleteAfterDays}
		},
	})
}

// liveConfig is the config with the Settings sheet's current values: the reloader stores a new
// one when a setting changes.
type liveConfig struct{ value atomic.Pointer[config.Config] }

func (l *liveConfig) get() config.Config { return *l.value.Load() }

func (l *liveConfig) set(cfg config.Config) { l.value.Store(&cfg) }

// ---- After a restore ------------------------------------------------------------

// reconcileWait bounds how long a start after a restore waits for herdr before taking stock
// anyway (and saying it could not see herdr).
const reconcileWait = 90 * time.Second

// reconcileRestore runs once after a restore, before the job supervisor starts: it compares the
// restored live jobs with the panes herdr really has, journals what it found, and clears the
// pending mark. The automation stays paused until the human resumes it.
func reconcileRestore(ctx context.Context, db *store.Store, fleetCache *fleet.Cache, journalLog *journal.Journal, logger *slog.Logger) {
	raw, err := db.GetKV(ctx, backup.PendingKey)
	if err != nil {
		return
	}
	var pending backup.Pending
	_ = json.Unmarshal([]byte(raw), &pending)
	panes, herdrSeen := waitForPanes(ctx, fleetCache)
	live, err := db.ListJobs(ctx, []string{"queued", "running", "needs_you", "blocked", "in_review"}, 500)
	if err != nil {
		logger.Warn("after a restore: reading the restored jobs", "err", err)
		return
	}
	var present, gone []string
	for _, job := range live {
		ref := fmt.Sprintf("j_%d", job.ID)
		switch {
		case job.PaneID == "":
		case panes[job.PaneID]:
			present = append(present, ref+" ("+job.PaneID+")")
		default:
			gone = append(gone, ref+" ("+job.PaneID+")")
		}
	}
	summary := fmt.Sprintf("Restored from a backup made %s: %d live jobs; %d still have their pane in herdr, %d do not. Automation is paused until you resume it.",
		pending.CreatedAt.Local().Format("2006-01-02 15:04"), len(live), len(present), len(gone))
	if !herdrSeen {
		summary = fmt.Sprintf("Restored from a backup made %s: %d live jobs, but herdr could not be read, so none were checked. Automation is paused until you resume it.",
			pending.CreatedAt.Local().Format("2006-01-02 15:04"), len(live))
	}
	journalLog.Add(ctx, journal.KindStorage, summary, map[string]any{"present": present, "gone": gone, "herdr": herdrSeen, "backup": pending})
	journalLog.Add(ctx, journal.KindAlert, summary, nil)
	logger.Warn(summary)
	if err := db.DeleteKV(ctx, backup.PendingKey); err != nil {
		logger.Warn("after a restore: clearing the pending mark", "err", err)
	}
}

// waitForPanes returns the set of pane ids herdr has, waiting up to reconcileWait for it to
// answer; false when it never did.
func waitForPanes(ctx context.Context, fleetCache *fleet.Cache) (map[string]bool, bool) {
	deadline := time.Now().Add(reconcileWait)
	for {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		snapshot, err := fleetCache.Fleet(attempt)
		cancel()
		if err == nil && snapshot.Error == "" {
			panes := map[string]bool{}
			for _, workspace := range snapshot.Workspaces {
				for _, tab := range workspace.Tabs {
					for _, pane := range tab.Panes {
						panes[pane.ID] = true
					}
				}
			}
			return panes, true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, false
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(2 * time.Second):
		}
	}
}
