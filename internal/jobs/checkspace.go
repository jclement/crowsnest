// Where an isolated check runs, and making sure nothing of a check outlives crowsnest (R9-C7).
//
// An isolated check's copy (landing.IsolatedCheckout) is made in <state>/checks/<job>-<check>-<n>,
// a directory crowsnest owns, recorded in the check's row before the copy exists and cleared once
// it is removed. Every ordinary exit path removes it (the run's defer, also after a timeout or a
// prepare failure). What a crash leaves, start-up removes: every directory under <state>/checks
// that no check of this process is running, and every row still naming one, journalled on the
// job. The check's own processes are tied to crowsnest by internal/lifeline's pipe, so they end
// when crowsnest does, however it ends; StopChecks ends them and removes their copies on a clean
// shutdown.

package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/landing"
)

// checksDir is where isolated checks make their copies.
func (s *Service) checksDir() string {
	if s.opts.Launch.StateDir == "" {
		return filepath.Join(os.TempDir(), "crowsnest-checks")
	}
	return filepath.Join(s.opts.Launch.StateDir, "checks")
}

// newCheckWorkspace names a fresh directory for check checkID of job id and records it on the
// check's row before anything is made in it (an unrecorded copy could leak).
func (s *Service) newCheckWorkspace(ctx context.Context, id string, checkID int64) (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	dir := filepath.Join(s.checksDir(), fmt.Sprintf("%s-%d-%s", id, checkID, hex.EncodeToString(raw)))
	if err := os.MkdirAll(s.checksDir(), 0o700); err != nil {
		return "", fmt.Errorf("making %s: %w", s.checksDir(), err)
	}
	if checkID != 0 {
		if err := s.opts.Store.SetCheckWorkspace(ctx, checkID, dir); err != nil {
			return "", err
		}
	}
	s.checksMu.Lock()
	if s.workspaces == nil {
		s.workspaces = map[string]bool{}
	}
	s.workspaces[dir] = true
	s.checksMu.Unlock()
	return dir, nil
}

// clearCheckWorkspace forgets check checkID's directory once it is removed.
func (s *Service) clearCheckWorkspace(ctx context.Context, checkID int64) {
	if checkID == 0 {
		return
	}
	if dirs, err := s.opts.Store.CheckWorkspaces(ctx); err == nil {
		s.checksMu.Lock()
		delete(s.workspaces, dirs[checkID])
		s.checksMu.Unlock()
	}
	if err := s.opts.Store.SetCheckWorkspace(context.WithoutCancel(ctx), checkID, ""); err != nil {
		s.opts.Logger.Warn("forgetting an isolated check's copy", "check", checkID, "err", err)
	}
}

// reconcileCheckWorkspaces removes the isolated copies no check of this process is running: what a
// crowsnest that stopped mid-check left behind. Run once, at the first tick after start.
func (s *Service) reconcileCheckWorkspaces(ctx context.Context) {
	recorded, err := s.opts.Store.CheckWorkspaces(ctx)
	if err != nil {
		s.opts.Logger.Warn("listing isolated check copies", "err", err)
		return
	}
	s.checksMu.Lock()
	live := map[string]bool{}
	for dir := range s.workspaces {
		live[dir] = true
	}
	s.checksMu.Unlock()
	for checkID, dir := range recorded {
		if live[dir] {
			continue
		}
		jobID := ""
		if id, err := s.opts.Store.CheckJob(ctx, checkID); err == nil {
			jobID = FormatID(id)
		}
		s.removeLeftCopy(ctx, jobID, dir)
		// Forgotten either way: a path that is not a copy is never tried again.
		if err := s.opts.Store.SetCheckWorkspace(ctx, checkID, ""); err != nil {
			s.opts.Logger.Warn("forgetting an isolated check's copy", "check", checkID, "err", err)
		}
	}
	// A directory no row names (an older copy, or a row a restore cleared): removed when it is a
	// copy crowsnest made (it carries the marker), otherwise left and said.
	entries, err := os.ReadDir(s.checksDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		dir := filepath.Join(s.checksDir(), entry.Name())
		if entry.IsDir() && !live[dir] {
			id, _, _ := strings.Cut(entry.Name(), "-")
			s.removeLeftCopy(ctx, id, dir)
		}
	}
}

// removeLeftCopy removes one copy a crash left and journals it on its job (jobID, when it names
// one). A path that is not one of crowsnest's copies (landing.ErrNotACheckCopy: outside the checks
// directory, a link, or without the marker, as a restored backup's row can name) is left alone and
// said (R10 new-H1).
func (s *Service) removeLeftCopy(ctx context.Context, jobID, dir string) {
	err := landing.RemoveIsolatedDir(ctx, s.checksDir(), dir)
	if _, parseErr := ParseID(jobID); parseErr != nil {
		if err != nil {
			s.opts.Logger.Warn("an isolated check's recorded copy was not removed", "dir", dir, "err", err)
		}
		return
	}
	switch {
	case errors.Is(err, landing.ErrNotACheckCopy):
		_ = s.Journal(ctx, jobID, EventAccept, "Left alone, not removed: a check's recorded copy is not one crowsnest made ("+
			oneLine(err.Error(), 300)+")", nil)
	case err != nil:
		_ = s.Journal(ctx, jobID, EventAccept, "An isolated check's copy left by a crowsnest that stopped could not be removed: "+oneLine(err.Error(), 200), nil)
	default:
		_ = s.Journal(ctx, jobID, EventAccept, "Removed an isolated check's copy left by a crowsnest that stopped mid-check ("+dir+")", nil)
	}
}

// stopChecksWait bounds how long StopChecks waits for running checks to end and remove their copies.
const stopChecksWait = 15 * time.Second

// StopChecks ends every check this process is running (their process groups are killed by the
// run's context) and waits, up to stopChecksWait, for each to remove its isolated copy: serve calls
// it on a clean shutdown. Their jobs stay waiting on a check; the next start runs it again.
func (s *Service) StopChecks() {
	s.checkCancel()
	done := make(chan struct{})
	go func() {
		s.checkRuns.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopChecksWait):
		s.opts.Logger.Warn("checks did not stop in time; start-up removes what they leave")
	}
}

// checkContext is the context a background check runs under: it outlives the request that
// started it, and StopChecks cancels it.
func (s *Service) checkContext(ctx context.Context) (context.Context, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(s.checkCtx, cancel)
	return runCtx, func() {
		stop()
		cancel()
	}
}
