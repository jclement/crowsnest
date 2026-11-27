// Package retention keeps day thirty tidy (DESIGN.md 4.19): it archives settled jobs off the
// boards, deletes settled history past its time, removes job state directories nothing needs,
// and reports what uses how much for the System page's Storage card.
//
// The rule it never breaks: nothing unlanded or unreviewed is deleted without the human's
// explicit confirmation. A settled job that worked in a worktree or on its own branch may hold
// the only copy of work nobody has merged, so it is "held": listed in the preview with the
// reason, and deleted only when its id is confirmed in a "clean up now" (never by the nightly
// run). Worktrees themselves are not removed here: the landing branch's sweep owns that (it
// knows what landed), and the Storage card points to it.
//
// The same Plan runs for the preview and the real thing, so the preview is exactly what happens.
package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/store"
)

const (
	// lastRunKey records the last sweep (kv), for the nightly schedule and the Storage card.
	lastRunKey = "retention.last_run"
	// pendingUploadAge is how long an upload never sent with anything is kept.
	pendingUploadAge = 24 * time.Hour
	// deadPushAge is how long a device that keeps failing is kept since its last success.
	deadPushAge = 30 * 24 * time.Hour
	// maxJobsPerRun bounds one run's job deletions, so a first sweep of a large history is a
	// few short transactions over a few nights rather than one long one.
	maxJobsPerRun = 500
	// nightlyHour is the local hour the nightly sweep runs in; checkEvery is how often it looks.
	nightlyHour = 3
	checkEvery  = 15 * time.Minute
	// overdueAfter runs a sweep at any hour when none has run for this long (a machine asleep
	// at 03:00 every night).
	overdueAfter = 48 * time.Hour
)

// Policy is the retention settings in force (they change live from the Settings sheet).
type Policy struct {
	ArchiveAfterDays int // 0 never
	DeleteAfterDays  int // 0 never
}

// Options configure a Service.
type Options struct {
	Store          *store.Store
	AttachmentsDir string // <data-dir>/attachments
	StateDir       string // <state>: jobs/j_N live under it
	DataDir        string
	DatabasePath   string
	BackupDir      string
	Policy         func() Policy
	Journal        *journal.Journal // nil: nothing journalled
	Logger         *slog.Logger
	Now            func() time.Time
}

// Service runs retention. Build it with New.
type Service struct {
	opts Options
	mu   sync.Mutex // one run at a time
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts}
}

// ---- Plans and runs -------------------------------------------------------------

// HeldJob is a settled job past its delete time that is kept until the human confirms it.
type HeldJob struct {
	ID        string    `json:"id"` // j_<n>
	Title     string    `json:"title"`
	State     string    `json:"state"`
	SettledAt time.Time `json:"settled_at"`
	Reason    string    `json:"reason"`
}

// SweepCount is one kind of row and how many go.
type SweepCount struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Report is a plan (DryRun) or what a run did.
type Report struct {
	DryRun     bool         `json:"dry_run"`
	At         time.Time    `json:"at"`
	Policy     Policy       `json:"policy"`
	Archive    int          `json:"archive"`     // jobs archived (or to archive)
	DeleteJobs int          `json:"delete_jobs"` // settled jobs deleted (or to delete)
	Held       []HeldJob    `json:"held"`        // past their time, kept until confirmed
	Confirmed  int          `json:"confirmed"`   // held jobs deleted because their ids were confirmed
	Sweeps     []SweepCount `json:"sweeps"`      // rows by kind
	StateDirs  int          `json:"state_dirs"`  // job state directories removed (or to remove)
	StateBytes int64        `json:"state_bytes"` // their size
	BlobsFreed int          `json:"blobs_freed"` // attachment files removed (runs only)
	Errors     []string     `json:"errors,omitempty"`
}

// Plan reports what a run would do now, changing nothing.
func (s *Service) Plan(ctx context.Context) (Report, error) {
	return s.run(ctx, true, nil)
}

// Run sweeps now. confirm lists held job ids (j_<n>) the human confirmed may be deleted; the
// nightly run passes none.
func (s *Service) Run(ctx context.Context, confirm []string) (Report, error) {
	return s.run(ctx, false, confirm)
}

func (s *Service) run(ctx context.Context, dryRun bool, confirm []string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	policy := s.policy()
	report := Report{DryRun: dryRun, At: now.UTC(), Policy: policy, Held: []HeldJob{}, Sweeps: []SweepCount{}}
	if err := s.archive(ctx, now, policy, dryRun, &report); err != nil {
		return report, err
	}
	if err := s.deleteJobs(ctx, now, policy, dryRun, confirm, &report); err != nil {
		return report, err
	}
	if policy.DeleteAfterDays > 0 {
		if err := s.sweep(ctx, now, policy, dryRun, &report); err != nil {
			return report, err
		}
	}
	if err := s.stateDirs(ctx, now, policy, dryRun, &report); err != nil {
		return report, err
	}
	if !dryRun {
		s.finish(ctx, report)
	}
	return report, nil
}

func (s *Service) policy() Policy {
	if s.opts.Policy == nil {
		return Policy{}
	}
	return s.opts.Policy()
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func (s *Service) archive(ctx context.Context, now time.Time, policy Policy, dryRun bool, report *Report) error {
	if policy.ArchiveAfterDays <= 0 {
		return nil
	}
	cutoff := now.Add(-days(policy.ArchiveAfterDays))
	var err error
	if dryRun {
		report.Archive, err = s.opts.Store.CountArchivable(ctx, cutoff)
	} else {
		report.Archive, err = s.opts.Store.ArchiveSettledJobs(ctx, cutoff, now)
	}
	return err
}

// heldReason says why a settled job is not deleted without confirmation, or "" when it may go.
func heldReason(job store.SettledJob) string {
	switch {
	case job.State == "done" && job.WorktreeBranch != "":
		return "worked on branch " + job.WorktreeBranch + ", which may hold unlanded work"
	case job.State == "done" && job.Isolate:
		return "worked in its own worktree, which may hold unlanded work"
	}
	return ""
}

func (s *Service) deleteJobs(ctx context.Context, now time.Time, policy Policy, dryRun bool, confirm []string, report *Report) error {
	if policy.DeleteAfterDays <= 0 {
		return nil
	}
	settled, err := s.opts.Store.SettledJobsBefore(ctx, now.Add(-days(policy.DeleteAfterDays)), maxJobsPerRun)
	if err != nil {
		return err
	}
	var safe, confirmed []int64
	for _, job := range settled {
		ref := "j_" + strconv.FormatInt(job.ID, 10)
		reason := heldReason(job)
		switch {
		case reason == "":
			safe = append(safe, job.ID)
		case slices.Contains(confirm, ref):
			confirmed = append(confirmed, job.ID)
		default:
			report.Held = append(report.Held, HeldJob{ID: ref, Title: job.Title, State: job.State, SettledAt: job.SettledAt.UTC(), Reason: reason})
		}
	}
	report.DeleteJobs, report.Confirmed = len(safe), len(confirmed)
	if dryRun {
		return nil
	}
	deleted, orphans, err := s.opts.Store.DeleteJobs(ctx, append(safe, confirmed...))
	if err != nil {
		return err
	}
	report.DeleteJobs = deleted - len(confirmed)
	s.removeBlobs(ctx, orphans, report)
	for _, id := range append(safe, confirmed...) {
		s.removeStateDir("j_"+strconv.FormatInt(id, 10), report)
	}
	return nil
}

func (s *Service) sweep(ctx context.Context, now time.Time, policy Policy, dryRun bool, report *Report) error {
	for _, sweep := range store.Sweeps(now.Add(-days(policy.DeleteAfterDays)), now.Add(-pendingUploadAge), now.Add(-deadPushAge), now) {
		var count int
		var orphans []string
		var err error
		if dryRun {
			count, err = s.opts.Store.CountSweep(ctx, sweep)
		} else {
			count, orphans, err = s.opts.Store.RunSweep(ctx, sweep)
		}
		if err != nil {
			return err
		}
		s.removeBlobs(ctx, orphans, report)
		report.Sweeps = append(report.Sweeps, SweepCount{Name: sweep.Name, Label: sweep.Label, Count: count})
	}
	return nil
}

// blobFile is an attachment blob name; anything else in the directory is not ours to remove.
var blobFile = regexp.MustCompile(`^[0-9a-f]{64}$`)

// removeBlobs deletes blobs (and thumbnails) no attachment row refers to, checking again just
// before each removal: an upload of the same bytes may have arrived since.
func (s *Service) removeBlobs(ctx context.Context, shas []string, report *Report) {
	for _, sha := range shas {
		if !blobFile.MatchString(sha) {
			continue
		}
		if exists, err := s.opts.Store.AttachmentShaExists(ctx, sha); err != nil || exists {
			continue
		}
		path := filepath.Join(s.opts.AttachmentsDir, sha)
		if err := os.Remove(path); err == nil {
			report.BlobsFreed++
		} else if !errors.Is(err, fs.ErrNotExist) {
			report.Errors = append(report.Errors, err.Error())
		}
		_ = os.Remove(path + ".thumb.jpg")
	}
}

// jobDirName is a job's state directory name.
var jobDirName = regexp.MustCompile(`^j_([0-9]+)$`)

// stateDirs removes job state directories nothing needs: those of jobs that no longer exist,
// and of settled jobs past the archive time (their agent is gone; the record keeps the result).
func (s *Service) stateDirs(ctx context.Context, now time.Time, policy Policy, dryRun bool, report *Report) error {
	root := filepath.Join(s.opts.StateDir, "jobs")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing job state directories: %w", err)
	}
	for _, entry := range entries {
		if !s.stateDirRemovable(ctx, entry, now, policy) {
			continue
		}
		if dryRun {
			report.StateDirs++
			report.StateBytes += dirSize(filepath.Join(root, entry.Name()))
			continue
		}
		s.removeStateDir(entry.Name(), report)
	}
	return nil
}

func (s *Service) stateDirRemovable(ctx context.Context, entry fs.DirEntry, now time.Time, policy Policy) bool {
	match := jobDirName.FindStringSubmatch(entry.Name())
	if match == nil || !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
		return false
	}
	id, _ := strconv.ParseInt(match[1], 10, 64)
	exists, settled, settledAt, err := s.opts.Store.JobExists(ctx, id)
	if err != nil {
		return false
	}
	if !exists {
		info, err := entry.Info()
		// A directory younger than a minute may belong to a job being created right now.
		return err == nil && now.Sub(info.ModTime()) > time.Minute
	}
	// Settled past the archive time: the same rule archiving uses, so a preview counts what the
	// run (which archives first) removes.
	return settled && policy.ArchiveAfterDays > 0 && !settledAt.IsZero() && settledAt.Before(now.Add(-days(policy.ArchiveAfterDays)))
}

func (s *Service) removeStateDir(name string, report *Report) {
	if !jobDirName.MatchString(name) {
		return
	}
	path := filepath.Join(s.opts.StateDir, "jobs", name)
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return
	}
	size := dirSize(path)
	// Files in a job directory are created read-only (0400) and their directories may be too;
	// make them writable so RemoveAll can unlink them.
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && d.IsDir() {
			_ = os.Chmod(p, 0o700) //nolint:gosec // our own private directory, removed next
		}
		return nil
	})
	if err := os.RemoveAll(path); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return
	}
	report.StateDirs++
	report.StateBytes += size
}

// dirSize sums the sizes of regular files under path (links are not followed).
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func (s *Service) finish(ctx context.Context, report Report) {
	body, _ := json.Marshal(report)
	if err := s.opts.Store.SetKV(ctx, lastRunKey, string(body)); err != nil {
		s.opts.Logger.Warn("recording the retention run", "err", err)
	}
	if s.opts.Journal == nil {
		return
	}
	rows := 0
	for _, sweep := range report.Sweeps {
		rows += sweep.Count
	}
	summary := fmt.Sprintf("Clean-up: %d jobs archived, %d deleted, %d rows of old history, %d job folders, %d files", report.Archive,
		report.DeleteJobs+report.Confirmed, rows, report.StateDirs, report.BlobsFreed)
	if len(report.Held) > 0 {
		summary += fmt.Sprintf("; %d kept until you confirm", len(report.Held))
	}
	s.opts.Journal.Add(ctx, journal.KindStorage, summary, map[string]any{"report": report})
}

// ---- Nightly ----------------------------------------------------------------------

// RunNightly sweeps once a night (in the 03:00 hour, local time), or at any hour once none has
// run for two days, until ctx ends.
func (s *Service) RunNightly(ctx context.Context) {
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.due(ctx, s.opts.Now()) {
				if _, err := s.Run(ctx, nil); err != nil {
					s.opts.Logger.Warn("nightly clean-up failed", "err", err)
				}
			}
		}
	}
}

// due reports whether the nightly sweep should run at now.
func (s *Service) due(ctx context.Context, now time.Time) bool {
	last := s.LastRun(ctx)
	if last == nil {
		return now.Local().Hour() == nightlyHour
	}
	since := now.Sub(last.At)
	return (since > 20*time.Hour && now.Local().Hour() == nightlyHour) || since > overdueAfter
}

// LastRun is the last run's report, or nil.
func (s *Service) LastRun(ctx context.Context) *Report {
	raw, err := s.opts.Store.GetKV(ctx, lastRunKey)
	if err != nil {
		return nil
	}
	var report Report
	if json.Unmarshal([]byte(raw), &report) != nil {
		return nil
	}
	return &report
}
