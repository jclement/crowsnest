// Run: the supervisor. While the setting is on it plans, starts the watchdog child, waits for
// `tailscale serve status` to show the foreground session, and then waits for the child to end,
// the setting to go off, or crowsnest to stop, looking for the session again every HealthPoll: a
// session tailscaled dropped under a live CLI (missed twice in a row) is a failure, shown as one,
// and the child is stopped and started again (R9-C13). A child that ends by itself is retried with
// backoff (2 s doubling to a minute, reset after a minute of health). Run returns only once the
// child is gone, so serve's shutdown can wait for it.
//
// The child record (kv tsserve.child: the watchdog's pid and process group, the target, the
// port) is what makes a leftover "unambiguously ours": at the next start, a live process with
// that pid whose command line still carries crowsnest's exact serve argv is crowsnest's own
// watchdog, orphaned somehow (it should not be: see child.go), and is stopped. Anything else,
// including a serve config with the same target but no record, is someone else's and is left
// alone and reported.

package tsserve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/tailscale"
)

// childKey is the kv row recording the running watchdog.
const childKey = "tsserve.child"

// childRecord is what childKey holds.
type childRecord struct {
	PID     int       `json:"pid"`
	PGID    int       `json:"pgid"`
	Port    int       `json:"port"`
	Target  string    `json:"target"`
	Started time.Time `json:"started"`
}

// Run supervises publishing until ctx ends; the child is stopped before it returns.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	s.cleanLeftover(ctx)
	backoff := s.opts.BackoffMin
	for ctx.Err() == nil {
		if !s.Enabled() {
			s.setView(View{State: StateOff, Note: s.currentNote()})
			s.wait(ctx, 0)
			continue
		}
		plan := s.plan(ctx)
		if plan.State != stateReady {
			if plan.State == StateUnavailable {
				plan.State = StateRefused // publishing is on: say why it cannot, rather than hide
			}
			plan.Show = true
			plan.Note = s.currentNote()
			s.setView(plan)
			s.wait(ctx, recheckRefused)
			continue
		}
		lived, ended := s.publish(ctx, plan)
		if ended {
			backoff = s.opts.BackoffMin
			continue
		}
		if lived >= healthyAfter {
			backoff = s.opts.BackoffMin
		}
		retry := s.opts.Now().Add(backoff)
		s.updateView(func(view *View) { view.RetryAt = &retry })
		s.wait(ctx, backoff)
		backoff = min(backoff*2, s.opts.BackoffMax)
	}
}

// wait blocks until a kick, ctx's end, or (for a positive duration) the timer.
func (s *Service) wait(ctx context.Context, duration time.Duration) {
	var timer <-chan time.Time
	if duration > 0 {
		t := time.NewTimer(duration)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
	case <-s.kick:
	case <-timer:
	}
}

// setBusy marks a child alive or gone; gone also drops the cached offer, which may predate it.
func (s *Service) setBusy(busy bool) {
	s.mu.Lock()
	s.busy = busy
	if !busy {
		s.offerAt = time.Time{}
	}
	s.mu.Unlock()
}

func (s *Service) currentNote() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view.Note
}

// ---- One child ------------------------------------------------------------------

// child is a running watchdog and its tailscale serve.
type child struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser // the only write end of the watchdog's lifeline pipe
	output  *tail
	exited  chan struct{}
	waitErr error
	started time.Time
}

// publish runs one child to its end. ended is true when it was stopped on purpose (the setting
// went off, or crowsnest is stopping); lived is how long it ran.
func (s *Service) publish(ctx context.Context, plan View) (lived time.Duration, ended bool) {
	running, err := s.startChild(ctx, plan)
	if err != nil {
		s.opts.Logger.Warn("starting tailscale serve", "err", err)
		plan.State, plan.Reason, plan.Fix = StateFailed, "crowsnest could not start tailscale serve.", "The server log has the detail; crowsnest tries again by itself."
		s.setView(plan)
		return 0, false
	}
	s.setBusy(true)
	defer s.setBusy(false)
	starting := plan
	starting.State, starting.Since = StateStarting, s.now()
	s.setView(starting)
	s.opts.Logger.Info("publishing to the tailnet", "url", plan.URL, "target", plan.Target)

	poll := time.NewTicker(s.opts.ConfirmPoll)
	defer poll.Stop()
	health := time.NewTicker(s.opts.HealthPoll)
	defer health.Stop()
	confirmed, missed := false, 0
	for {
		select {
		case <-running.exited:
			s.childEnded(ctx, plan, running, confirmed)
			return time.Since(running.started), false
		case <-health.C:
			if !confirmed {
				continue
			}
			if s.sessionAlive(ctx, plan) {
				missed = 0
				continue
			}
			if missed++; missed < healthMisses {
				continue
			}
			s.stopChild(running)
			s.sessionLost(ctx, plan)
			return time.Since(running.started), false
		case <-ctx.Done():
			s.stopChild(running)
			return 0, true
		case <-s.kick:
			if !s.Enabled() {
				s.stopChild(running)
				s.stoppedView(ctx, plan)
				return 0, true
			}
		case <-poll.C:
			if !confirmed {
				confirmed = s.confirm(ctx, plan, running)
			}
		}
	}
}

// confirm checks whether tailscale serve status shows the session yet, and while it does not,
// whether tailscale serve is waiting on something it printed (Serve not enabled).
func (s *Service) confirm(ctx context.Context, plan View, running *child) bool {
	config, err := tailscale.ReadServeConfig(ctx, s.runner())
	if err == nil && servesOn(config, plan) {
		since := s.now()
		published := plan
		published.State, published.Since = StatePublishing, since
		s.setView(published)
		s.opts.Logger.Info("published to the tailnet", "url", plan.URL)
		s.journal(ctx, false, "Tailscale: published to the tailnet at "+plan.URL)
		if s.opts.OnPublished != nil {
			s.opts.OnPublished(plan.URL)
		}
		return true
	}
	if problem, ok := classify(running.output.String()); ok && problem.Waiting {
		s.updateView(func(view *View) { view.Reason, view.Fix, view.FixURL = problem.Reason, problem.Fix, problem.FixURL })
	}
	return false
}

// healthMisses is how many health polls in a row must miss the session before it counts as lost:
// one miss can be tailscaled answering slowly or restarting for a moment.
const healthMisses = 2

// sessionAlive reports whether serve status still shows plan's foreground session. A status that
// cannot be read counts as a miss: tailscaled is not answering, so neither is the URL.
func (s *Service) sessionAlive(ctx context.Context, plan View) bool {
	config, err := tailscale.ReadServeConfig(ctx, s.runner())
	return err == nil && servesOn(config, plan)
}

// sessionLost shows a confirmed session that tailscaled no longer has: failed, with the reason;
// Run starts the child again after the backoff.
func (s *Service) sessionLost(ctx context.Context, plan View) {
	s.opts.Logger.Warn("tailscale serve lost its session; restarting it", "url", plan.URL)
	failed := plan
	failed.State, failed.Since = StateFailed, s.now()
	failed.Reason = "tailscale serve was running, but Tailscale no longer shows its session (tailscaled restarted or signed out?), so " + plan.URL + " stopped answering."
	failed.Fix = "crowsnest restarts it by itself; if this keeps happening, check that Tailscale is running and signed in."
	failed.FixURL = ""
	s.setView(failed)
	s.journal(ctx, true, "Tailscale: publishing at "+plan.URL+" lost its session; restarting it")
}

// servesOn reports whether a foreground session proxies plan's port to its target.
func servesOn(config tailscale.ServeConfig, plan View) bool {
	for _, handler := range config.Handlers() {
		if !handler.Foreground || strings.TrimRight(handler.Proxy, "/") != plan.Target {
			continue
		}
		if strings.HasSuffix(handler.HostPort, ":"+strconv.Itoa(plan.Port)) {
			return true
		}
	}
	return false
}

// childEnded records a child that ended by itself: failed, with the reason.
func (s *Service) childEnded(ctx context.Context, plan View, running *child, confirmed bool) {
	s.clearRecord(ctx)
	output := running.output.String()
	s.opts.Logger.Warn("tailscale serve stopped", "err", running.waitErr, "output", strings.TrimSpace(lastLines(output, 6)))
	problem, ok := classify(output)
	if !ok {
		problem = Problem{Reason: "tailscale serve stopped by itself (" + exitText(running.waitErr) + ").", Fix: "The server log has its output; crowsnest tries again by itself."}
	}
	failed := plan
	failed.State, failed.Reason, failed.Fix, failed.FixURL, failed.Since = StateFailed, problem.Reason, problem.Fix, problem.FixURL, s.now()
	s.setView(failed)
	if confirmed {
		s.journal(ctx, true, "Tailscale: publishing at "+plan.URL+" stopped by itself: "+problem.Reason)
	}
}

func (s *Service) stoppedView(ctx context.Context, plan View) {
	stopped := plan
	stopped.State, stopped.Since = StateStopped, s.now()
	stopped.Reason, stopped.Fix, stopped.FixURL = "", "", ""
	s.setView(stopped)
	s.journal(ctx, false, "Tailscale: stopped publishing "+plan.URL)
}

// startChild starts the watchdog in a process group of its own, with the lifeline pipe as its
// stdin, and records it.
func (s *Service) startChild(ctx context.Context, plan View) (*child, error) {
	args := ServeArgs(plan.Port, plan.Target)
	if err := CheckArgs(args); err != nil {
		return nil, err
	}
	if s.opts.Self == "" || s.opts.Binary == "" {
		return nil, errors.New("tsserve: no watchdog or tailscale binary configured")
	}
	argv := append(append(append([]string{}, s.opts.SelfArgs...), s.opts.Binary), args...)
	cmd := exec.Command(s.opts.Self, argv...) //nolint:gosec // our own binary; the tailscale argv passed the allowlist
	cmd.Env = append(herdr.ScrubEnv(os.Environ()), s.opts.SelfEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output := &tail{limit: outputTail}
	cmd.Stdout, cmd.Stderr = output, output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	running := &child{cmd: cmd, stdin: stdin, output: output, exited: make(chan struct{}), started: time.Now()}
	go func() {
		running.waitErr = cmd.Wait()
		close(running.exited)
	}()
	s.saveRecord(ctx, childRecord{PID: cmd.Process.Pid, PGID: cmd.Process.Pid, Port: plan.Port, Target: plan.Target, Started: s.opts.Now()})
	return running, nil
}

// stopChild asks the watchdog to stop tailscale (SIGTERM: it interrupts, then terminates, then
// kills), and kills the whole group if that takes longer than StopGrace.
func (s *Service) stopChild(running *child) {
	pgid := running.cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	select {
	case <-running.exited:
	case <-time.After(s.opts.StopGrace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-running.exited
	}
	// The watchdog is gone; anything left in its group (it should be nothing) goes too.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	_ = running.stdin.Close()
	s.clearRecord(context.WithoutCancel(context.Background()))
}

// ---- The child record and leftovers ----------------------------------------------

func (s *Service) saveRecord(ctx context.Context, record childRecord) {
	if s.opts.KV == nil {
		return
	}
	body, _ := json.Marshal(record)
	if err := s.opts.KV.SetKV(context.WithoutCancel(ctx), childKey, string(body)); err != nil {
		s.opts.Logger.Warn("recording the tailscale serve child", "err", err)
	}
}

func (s *Service) clearRecord(ctx context.Context) {
	if s.opts.KV != nil {
		_ = s.opts.KV.DeleteKV(context.WithoutCancel(ctx), childKey)
	}
}

// cleanLeftover stops a watchdog an earlier crowsnest recorded and left running, when it is
// unambiguously that watchdog, and says what it did or did not do.
func (s *Service) cleanLeftover(ctx context.Context) {
	if s.opts.KV == nil {
		return
	}
	raw, err := s.opts.KV.GetKV(ctx, childKey)
	if err != nil || raw == "" {
		return
	}
	defer s.clearRecord(ctx)
	var record childRecord
	if json.Unmarshal([]byte(raw), &record) != nil || record.PID <= 1 {
		return
	}
	if syscall.Kill(record.PID, 0) != nil {
		return // gone, as it should be: tailscaled dropped its session with it
	}
	command, err := s.opts.ProcessCommand(record.PID)
	want := strings.Join(ServeArgs(record.Port, record.Target), " ")
	if err != nil || !strings.Contains(command, want) {
		note := "An earlier crowsnest recorded a tailscale serve child as process " + strconv.Itoa(record.PID) + ", but that process is now something else, so crowsnest left it alone."
		s.updateView(func(view *View) { view.Note = note })
		return
	}
	_ = syscall.Kill(record.PID, syscall.SIGTERM)
	deadline := time.Now().Add(s.opts.StopGrace)
	for time.Now().Before(deadline) && syscall.Kill(record.PID, 0) == nil {
		time.Sleep(50 * time.Millisecond)
	}
	if record.PGID > 1 {
		_ = syscall.Kill(-record.PGID, syscall.SIGKILL)
	}
	note := "Stopped a tailscale serve that an earlier crowsnest left running (process " + strconv.Itoa(record.PID) + ")."
	s.updateView(func(view *View) { view.Note = note })
	s.opts.Logger.Warn(note)
	s.journal(ctx, true, "Tailscale: "+note)
}

// processCommand is `ps -o command= -p <pid>` (macOS and Linux alike).
func processCommand(pid int) (string, error) {
	output, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // a fixed argv with a number
	return strings.TrimSpace(string(output)), err
}

// ---- Output ------------------------------------------------------------------------

// tail keeps the last limit bytes written to it.
type tail struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if over := len(t.data) - t.limit; over > 0 {
		t.data = append([]byte(nil), t.data[over:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.data)
}

func lastLines(text string, count int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}

func exitText(err error) string {
	code := exitCode(err)
	if code < 0 {
		return "killed by a signal"
	}
	return "exit " + strconv.Itoa(code)
}
