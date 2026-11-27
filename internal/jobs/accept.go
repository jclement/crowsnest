// Acceptance (DESIGN.md 6.15): an agent's done is its word; crowsnest's own evidence decides
// whether a coding job is accepted.
//
// When an agent holding a job in a git checkout calls done, crowsnest looks before it believes:
//
//   - the tree must be clean (refused at once, with the files named: uncommitted work is lost
//     work), and a job in a worktree of its own must have commits ahead of its base, or say why
//     not (no_commits_reason);
//   - then the project's check command (default: `mise run check` when the checkout's mise.toml
//     has that task; per project in the app, or none) is run by crowsnest itself in the job's
//     worktree, through the heavy gate, while the job waits in review with accept_state
//     checking. Its exit code, duration and the tail of its output are kept (job_checks),
//     pinned to the commit it ran against;
//   - a pass makes the job accepted (and then the review gate, if one applies, looks at that
//     revision); a failure types one line to the agent naming the output file, and the job goes
//     back to running; after MaxRounds failures it needs the human, with the failure shown;
//   - a project with no check command makes the job "finished, unverified": done, but shown
//     apart from accepted.
//
// Evidence goes stale when HEAD moves past the commit it ran against; Land refuses a stale,
// failed or missing check, and a base branch that moved on ("needs rebase"), unless the human
// overrides explicitly. "Ask an agent to rebase" starts a fix job in the worktree; when it
// finishes, the check is run again.

package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

// Accept states (jobs.accept_state).
const (
	AcceptNone        = ""
	AcceptChecking    = "checking"
	AcceptAccepted    = "accepted"
	AcceptUnverified  = "unverified"
	AcceptFailed      = "failed"
	AcceptStale       = "stale"
	AcceptNeedsRebase = "needs_rebase"
)

// Check statuses (job_checks.status).
const (
	CheckRunning = "running"
	CheckPassed  = "passed"
	CheckFailed  = "failed"
	CheckError   = "error"
)

// CheckNone is a project's check command meaning "no check".
const CheckNone = "none"

const (
	// DefaultCheckTimeout bounds one check run.
	DefaultCheckTimeout = 30 * time.Minute
	// DefaultCheckRounds is how many failed checks go back to the agent before the human decides.
	DefaultCheckRounds = 3
	// checkTailBytes is how much of the output is stored with the check (the file has more).
	checkTailBytes = 8 << 10
	// checkFileBytes is how much of the output is kept in the job's check file.
	checkFileBytes = 256 << 10
)

// AcceptOptions configure acceptance. Zero: off (done is the agent's word, as before 6.15).
type AcceptOptions struct {
	Enabled bool
	// Timeout bounds one check run (0: DefaultCheckTimeout).
	Timeout time.Duration
	// MaxRounds is how many failed checks go back to the agent (0: DefaultCheckRounds).
	MaxRounds int
	// Heavy is the command a check runs under, the heavy gate: e.g. ["/path/crowsnest", "heavy",
	// "--"]. Empty runs the check directly (tests).
	Heavy []string
	// DefaultCommand is the check used when a project names none and the checkout has no mise
	// check task (--demo and tests; empty: finished, unverified).
	DefaultCommand string
}

func (o AcceptOptions) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultCheckTimeout
}

func (o AcceptOptions) maxRounds() int {
	if o.MaxRounds > 0 {
		return o.MaxRounds
	}
	return DefaultCheckRounds
}

// SetAccept applies Settings' check timeout and rounds.
func (s *Service) SetAccept(timeout time.Duration, rounds int) {
	next := s.accept()
	next.Timeout, next.MaxRounds = timeout, rounds
	s.liveAccept.Store(&next)
}

func (s *Service) accept() AcceptOptions {
	if live := s.liveAccept.Load(); live != nil {
		return *live
	}
	return s.opts.Accept
}

// Evidence is a job's latest check as the job view shows it.
type Evidence struct {
	Command    string     `json:"command"`
	Status     string     `json:"status"`
	ExitCode   int        `json:"exit_code"`
	DurationMS int64      `json:"duration_ms"`
	Commit     string     `json:"commit"`
	OutputTail string     `json:"output_tail"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	// Head is the worktree's commit now, when it could be read; Stale says it moved past the
	// commit the check ran against.
	Head  string `json:"head,omitempty"`
	Stale bool   `json:"stale"`
	// DefinitionChanged lists files of the branch's own diff that define its check (R6-S4): the
	// branch changed what passing means, so the pass is the branch's own word about itself.
	DefinitionChanged []string `json:"definition_changed"`
	// Mode is where the check ran (R7-C01): "worktree" (the job's checkout, the default) or
	// "isolated" (a temporary copy of exactly Commit).
	Mode string `json:"mode"`
}

// workDirOf is where a job's agent works.
func workDirOf(r store.JobRecord) string {
	return firstNonEmptyDir(r.WorkDir, r.Dir)
}

// acceptApplies reports whether acceptance looks at r's done: a coding job (a git checkout),
// not a reviewer, not a fix job (its parent is checked instead), not a standing agent's.
func (s *Service) acceptApplies(ctx context.Context, r store.JobRecord) bool {
	if !s.accept().Enabled || r.ReviewOf != 0 || r.FixOf != 0 || IsStanding(r.AgentName) || r.Role == "reviewer" {
		return false
	}
	dir := workDirOf(r)
	return dir != "" && landing.IsRepo(ctx, dir)
}

// miseCheckTask matches a `check` task in a mise.toml: [tasks.check], [tasks."check"], or a
// `check = …` line inside [tasks].
var miseCheckTask = regexp.MustCompile(`(?m)^\s*\[tasks\.(?:"check"|'check'|check)\]|^\s*\[tasks\]\s*$(?:\n[^\[]*?)*?^\s*"?check"?\s*=`)

// AutoCheckCommand is the check crowsnest runs in dir when the project names none: `mise run
// check` when the checkout's mise configuration has a check task, else "".
func AutoCheckCommand(dir string) string {
	for _, name := range []string{"mise.toml", ".mise.toml", "mise/config.toml", ".mise/config.toml", ".config/mise.toml", ".config/mise/config.toml"} {
		body, ok := readSmallRegular(filepath.Join(dir, name))
		if ok && miseCheckTask.Match(body) {
			return "mise run check"
		}
	}
	for _, name := range []string{"mise-tasks/check", ".mise/tasks/check", "mise/tasks/check", ".mise-tasks/check"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return "mise run check"
		}
	}
	return ""
}

// readSmallRegular reads a regular file of at most 1 MiB, never following a link (R6-S4: a
// committed mise.toml that links to /dev/zero or a FIFO must not hang or exhaust crowsnest).
func readSmallRegular(path string) ([]byte, bool) {
	// R7-I6: opened, not read by path after an Lstat, so a FIFO swapped in between cannot block:
	// O_NOFOLLOW refuses a link, O_NONBLOCK keeps open(2) from waiting, and the opened file itself
	// is checked to be regular and small before it is read through a limit.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // the job's own checkout
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(file, 1<<20))
	return body, err == nil
}

// checkCommandFor is the command that accepts r's work ("" for none) and where it came from.
func (s *Service) checkCommandFor(ctx context.Context, r store.JobRecord) (command, source string) {
	if r.ProjectID != 0 {
		if project, err := s.opts.Store.GetProjectAccept(ctx, r.ProjectID); err == nil {
			switch strings.TrimSpace(project.CheckCommand) {
			case CheckNone:
				return "", "the project has no check"
			case "":
			default:
				return strings.TrimSpace(project.CheckCommand), "the project's check"
			}
		}
	}
	if command := AutoCheckCommand(workDirOf(r)); command != "" {
		return command, "the checkout's mise check task"
	}
	if command := s.accept().DefaultCommand; command != "" {
		return command, "the server's default check"
	}
	return "", "no check command (no mise check task, and none set on a project)"
}

// acceptPlan is what Done found before it records anything.
type acceptPlan struct {
	applies bool
	command string // "" with applies: finished, unverified
	source  string
	head    string
}

// planAccept looks at the job's tree before done is recorded and refuses what crowsnest can see
// is not finished: uncommitted changes, or no commits without a reason.
func (s *Service) planAccept(ctx context.Context, id string, noCommitsReason string) (acceptPlan, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return acceptPlan{}, ErrNotFound
	}
	r, err := s.opts.Store.GetJob(ctx, numeric)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return acceptPlan{}, ErrNotFound
		}
		return acceptPlan{}, err
	}
	if State(r.State).Terminal() || State(r.State) == StateInReview || !s.acceptApplies(ctx, r) {
		return acceptPlan{}, nil
	}
	base, baseCommit := "", ""
	if r.WorktreeBranch != "" {
		base, baseCommit = r.BaseBranch, r.BaseCommit
	}
	snap, err := landing.Snap(ctx, workDirOf(r), base, baseCommit)
	if err != nil {
		// git could not be read: say so rather than wave it through or refuse for ever.
		return acceptPlan{}, invalid("crowsnest could not read your worktree to accept this done (%s); check the directory and call done again", oneLine(err.Error(), 200))
	}
	if !snap.Clean {
		return acceptPlan{}, invalid("your worktree has uncommitted changes, so this is not finished: %s. Commit what belongs to the job (or remove what does not), then call done again",
			strings.Join(capList(snap.Dirty, 8), "; "))
	}
	if r.WorktreeBranch != "" && snap.Based && snap.Ahead == 0 && strings.TrimSpace(noCommitsReason) == "" {
		return acceptPlan{}, invalid("your branch %s has no commits ahead of %s. Commit your work and call done again; if the job needed no change, call done with no_commits_reason saying why",
			r.WorktreeBranch, firstNonEmptyDir(r.BaseBranch, "its base"))
	}
	command, source := s.checkCommandFor(ctx, r)
	return acceptPlan{applies: true, command: command, source: source, head: snap.Head}, nil
}

func capList(items []string, limit int) []string {
	if len(items) <= limit {
		return items
	}
	return append(append([]string{}, items[:limit]...), fmt.Sprintf("and %d more", len(items)-limit))
}

// applyAcceptLocked records the plan on a done being recorded (s.mu held): checking with a
// command, else finished-unverified. It reports whether a check must start.
func applyAcceptLocked(r *store.JobRecord, c *change, plan acceptPlan, noCommitsReason string) bool {
	if !plan.applies {
		return false
	}
	r.NoCommitsReason = oneLine(noCommitsReason, 500)
	if plan.command == "" {
		r.AcceptState, r.AcceptDetail = AcceptUnverified, "Finished, unverified: "+plan.source
		c.event(EventAccept, "Finished, unverified: "+plan.source+"; the tree is clean", nil)
		return false
	}
	r.State, r.AcceptState = string(StateInReview), AcceptChecking
	r.AcceptDetail = fmt.Sprintf("crowsnest is running `%s` at %s", oneLine(plan.command, 120), shortHash(plan.head))
	c.event(EventAccept, fmt.Sprintf("Checking: crowsnest runs `%s` (%s) at %s before the job is accepted", oneLine(plan.command, 120), plan.source, shortHash(plan.head)), nil)
	return true
}

// ---- Running a check -----------------------------------------------------------------------

// checkResult is what one run produced.
type checkResult struct {
	status   string
	exit     int
	duration time.Duration
	output   []byte // the last checkFileBytes
	note     string // why it is an error, or a timeout
}

// startCheck runs a job's check in the background (once per job at a time).
func (s *Service) startCheck(ctx context.Context, id string) {
	s.checksMu.Lock()
	if s.checks == nil {
		s.checks = map[string]bool{}
	}
	if s.checks[id] {
		s.checksMu.Unlock()
		return
	}
	s.checks[id] = true
	s.checksMu.Unlock()
	run := func(ctx context.Context) {
		defer func() {
			s.checksMu.Lock()
			delete(s.checks, id)
			s.checksMu.Unlock()
		}()
		s.runCheck(ctx, id)
	}
	if s.opts.Synchronous {
		run(ctx)
		return
	}
	go run(context.WithoutCancel(ctx))
}

// checkRunning reports whether this process is running id's check.
func (s *Service) checkRunning(id string) bool {
	s.checksMu.Lock()
	defer s.checksMu.Unlock()
	return s.checks[id]
}

// runCheck runs the check for a job waiting on it (accept_state checking) and applies the result.
func (s *Service) runCheck(ctx context.Context, id string) {
	numeric, err := ParseID(id)
	if err != nil {
		return
	}
	r, err := s.opts.Store.GetJob(ctx, numeric)
	if err != nil || r.AcceptState != AcceptChecking {
		return
	}
	dir := workDirOf(r)
	command, _ := s.checkCommandFor(ctx, r)
	if command == "" {
		s.checkFinished(ctx, id, store.CheckRecord{Status: CheckError, Command: "", OutputTail: "no check command is configured any more"})
		return
	}
	base, baseCommit := "", ""
	if r.WorktreeBranch != "" {
		base, baseCommit = r.BaseBranch, r.BaseCommit
	}
	before, err := landing.Snap(ctx, dir, base, baseCommit)
	if err != nil {
		s.checkFinished(ctx, id, store.CheckRecord{Status: CheckError, Command: command, OutputTail: "the worktree could not be read: " + err.Error()})
		return
	}
	if !before.Clean {
		// Evidence is about a commit: a check over uncommitted files would pass code that is not
		// what lands (R6-C02).
		s.checkFinished(ctx, id, store.CheckRecord{Status: CheckError, Command: command, Commit: before.Head,
			OutputTail: "the worktree has uncommitted changes, so a check now would not test the commit: " + strings.Join(capList(before.Dirty, 8), "; ") +
				". Commit them (or remove them) and run the check again"})
		return
	}
	defs := landing.CheckDefinitionChanges(ctx, dir, base, baseCommit, commandPaths(dir, command))
	isolated, prepare := s.checkIsolationFor(ctx, r)
	record := store.CheckRecord{JobID: numeric, Commit: before.Head, Command: command, Status: CheckRunning,
		ExitCode: -1, Owner: s.instance, StartedAt: s.opts.Now(), DefinitionChanged: strings.Join(defs, ","), Mode: CheckModeWorktree}
	if isolated {
		record.Mode = CheckModeIsolated
	} else {
		// R7-C01: a content fingerprint of the checkout (tracked and untracked files) at the start,
		// compared with one at the end.
		record.TreeBefore, _ = landing.TreeFingerprint(ctx, dir)
	}
	if record.ID, err = s.opts.Store.InsertCheck(ctx, record); err != nil {
		// A check that cannot be recorded cannot be evidence (R6-C13): nothing is run.
		s.opts.Logger.Warn("recording a check", "job", id, "err", err)
		s.checkFinished(ctx, id, store.CheckRecord{Status: CheckError, Command: command, Commit: before.Head,
			OutputTail: "crowsnest could not record the check, so it cannot count as evidence: " + err.Error()})
		return
	}
	var result checkResult
	if isolated {
		result = s.execIsolatedCheck(ctx, id, dir, before.Head, prepare, command)
	} else {
		result = s.execWorktreeCheck(ctx, id, dir, command)
	}
	after, err := landing.Snap(ctx, dir, base, baseCommit)
	if !isolated && err == nil {
		record.TreeAfter, _ = landing.TreeFingerprint(ctx, dir)
	}
	moved := false
	switch {
	case result.status == CheckError && result.note != "":
		// The run itself could not be made (the copy, the prepare command): that is the result.
	case err != nil:
		result.status, result.note = CheckError, "the worktree could not be read after the check: "+err.Error()
	case after.Head != before.Head:
		// A commit landed while the check ran: this evidence is about a revision that is gone.
		moved = true
		result.status, result.note = CheckError, fmt.Sprintf("HEAD moved from %s to %s while the check ran; it will be run again", shortHash(before.Head), shortHash(after.Head))
	case !isolated && !after.Clean:
		// The tree changed under the check (an agent's process, or the check itself writing
		// tracked files): what passed is not the commit (R6-C02, R6-S4). An isolated check ran
		// in its own copy, so the job's checkout changing does not touch it.
		result.status, result.note = CheckError, "the worktree changed while the check ran, so this run is not evidence for the commit: "+strings.Join(capList(after.Dirty, 8), "; ")
	case !isolated && record.TreeBefore != "" && record.TreeAfter != record.TreeBefore:
		result.status, result.note = CheckError, fmt.Sprintf("the checkout's content changed while the check ran (fingerprint %s, then %s), so this run is not evidence for the commit",
			shortHash(record.TreeBefore), shortHash(record.TreeAfter))
	}
	record.Status, record.ExitCode, record.Duration, record.FinishedAt = result.status, result.exit, result.duration, s.opts.Now()
	record.OutputTail = tailText(result.output, checkTailBytes)
	if result.note != "" {
		record.OutputTail = strings.TrimSpace(record.OutputTail + "\n[crowsnest] " + result.note)
	}
	if err := s.opts.Store.FinishCheck(ctx, record); err != nil {
		if err = s.opts.Store.FinishCheck(ctx, record); err != nil {
			s.opts.Logger.Warn("recording a check's result", "job", id, "err", err)
			// Not recorded, not evidence (R6-C13): the job is not accepted on a result that is
			// only in memory.
			record.Status, record.ID = CheckError, 0
			record.OutputTail = strings.TrimSpace(record.OutputTail + "\n[crowsnest] the result could not be recorded, so it does not count: " + err.Error())
		}
	}
	path := s.writeCheckOutput(id, record, result.output)
	watch := s.watchOf(id)
	if moved && record.ID != 0 {
		// Re-runs for a moving HEAD are bounded (R6-S8): a busy agent committing in the
		// background cannot hold the heavy slots for ever.
		watch.checkReruns++
		if watch.checkReruns <= maxCheckReruns {
			_ = s.Journal(ctx, id, EventAccept, "The check is run again: "+result.note, nil)
			s.goCheckAgain(ctx, id)
			return
		}
		record.OutputTail = strings.TrimSpace(record.OutputTail + fmt.Sprintf("\n[crowsnest] HEAD moved during %d runs in a row; the check is not run again until the branch is still", watch.checkReruns))
	}
	watch.checkReruns = 0
	record.Owner = path // carried to checkFinished: where the full output is
	s.checkFinished(ctx, id, record)
}

// Check modes (R7-C01): where crowsnest ran a check.
const (
	CheckModeWorktree = "worktree" // the job's own checkout (the default)
	CheckModeIsolated = "isolated" // a temporary detached worktree of exactly the commit
)

// checkIsolationFor reads whether r's project runs its check in an isolated copy, and the
// command that prepares the copy ("" for none).
func (s *Service) checkIsolationFor(ctx context.Context, r store.JobRecord) (bool, string) {
	if r.ProjectID == 0 {
		return false, ""
	}
	project, err := s.opts.Store.GetProjectAccept(ctx, r.ProjectID)
	if err != nil {
		return false, ""
	}
	return project.CheckIsolated, strings.TrimSpace(project.CheckPrepare)
}

// execWorktreeCheck runs the check in the job's own checkout. While it runs, the job's agent is
// held from using tools: the marker file beside its guard rules makes its PreToolUse hook answer
// "wait" (R7-C01). That is not airtight: a process the agent started in the background earlier is
// not a tool call, and the agent can remove the file; the fingerprints and the clean-tree checks
// are what void such a run, and the isolated mode is what the project chooses to rule it out.
func (s *Service) execWorktreeCheck(ctx context.Context, id, dir, command string) checkResult {
	marker := s.checkingMarker(id)
	if marker != "" {
		deadline := s.opts.Now().Add(s.accept().timeout() + time.Minute)
		_ = os.WriteFile(marker, []byte(deadline.UTC().Format(time.RFC3339)+"\n"), 0o600)
		defer func() { _ = os.Remove(marker) }()
	}
	return s.execCheck(ctx, dir, command)
}

// execIsolatedCheck makes the isolated copy of commit, runs prepare (if any) and then the check in
// it, and removes the copy. A copy that cannot be made, or a prepare command that fails, is the
// run's result (an error with what happened); it never falls back to the job's checkout.
func (s *Service) execIsolatedCheck(ctx context.Context, id, dir, commit, prepare, command string) checkResult {
	copied, err := landing.IsolatedCheckout(ctx, dir, commit)
	if err != nil {
		return checkResult{status: CheckError, exit: -1, note: "the isolated copy of the commit could not be made, so nothing was checked: " + err.Error()}
	}
	defer copied.Remove(ctx)
	_ = s.Journal(ctx, id, EventAccept, "Checking an isolated copy of "+shortHash(commit), nil)
	if prepare != "" {
		prepared := s.execCheck(ctx, copied.Dir, prepare)
		if prepared.status != CheckPassed {
			prepared.status = CheckError
			prepared.note = strings.TrimSpace(fmt.Sprintf("the prepare command `%s` did not succeed (exit %d), so the check was not run. %s", oneLine(prepare, 120), prepared.exit, prepared.note))
			return prepared
		}
	}
	return s.execCheck(ctx, copied.Dir, command)
}

// checkingMarker is the file whose presence (with a deadline in it) holds the job's agent's tools
// while crowsnest checks its worktree; it sits beside the job's guard rules ("" when the job has
// no directory of its own).
func (s *Service) checkingMarker(id string) string {
	dir, err := s.jobDir(id)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, guard.CheckingMarkerFile)
}

// maxCheckReruns is how many times in a row a check is run again because HEAD moved under it.
const maxCheckReruns = 3

// commandPaths are the files a check command names (relative to dir, existing), so a branch that
// rewrites the script its check runs is shown as changing its check (R6-S4).
func commandPaths(dir, command string) []string {
	var out []string
	for _, word := range strings.Fields(command) {
		word = strings.Trim(word, `"'`)
		if !strings.Contains(word, "/") || filepath.IsAbs(word) || strings.Contains(word, "..") {
			continue
		}
		clean := filepath.Clean(strings.TrimPrefix(word, "./"))
		if info, err := os.Lstat(filepath.Join(dir, clean)); err == nil && info.Mode().IsRegular() {
			out = append(out, filepath.ToSlash(clean))
		}
	}
	return out
}

// goCheckAgain runs the check once more after the current run returns.
func (s *Service) goCheckAgain(ctx context.Context, id string) {
	go func() {
		for i := 0; i < 100 && s.checkRunning(id); i++ {
			time.Sleep(50 * time.Millisecond)
		}
		s.startCheck(context.WithoutCancel(ctx), id)
	}()
}

// execCheck runs command in dir under the heavy gate, its process group killed at the timeout.
func (s *Service) execCheck(ctx context.Context, dir, command string) checkResult {
	opts := s.accept()
	ctx, cancel := context.WithTimeout(ctx, opts.timeout())
	defer cancel()
	args := append(append([]string{}, opts.Heavy...), "sh", "-c", command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // the project's own check command, set by the human
	cmd.Dir = dir
	cmd.Env = checkEnv(s.opts.Launch.ExtraEnv)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	out := &tailBuffer{limit: checkFileBytes}
	cmd.Stdout, cmd.Stderr = out, out
	started := time.Now()
	err := cmd.Run()
	result := checkResult{duration: time.Since(started), output: out.Bytes(), exit: -1}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		result.status, result.note = CheckFailed, fmt.Sprintf("the check timed out after %s and was stopped", opts.timeout())
	case err == nil:
		result.status, result.exit = CheckPassed, 0
	case errors.As(err, &exitErr):
		result.status, result.exit = CheckFailed, exitErr.ExitCode()
	default:
		result.status, result.note = CheckError, "the check could not be started: "+err.Error()
	}
	return result
}

// checkEnv is the environment a check runs in: crowsnest's own, without herdr's session, any
// crowsnest variable (socket, session, token, job), Claude's markers, or a credential the operator
// exported (R6-S12: *_TOKEN, *_SECRET, *_PASSWORD, *_API_KEY), plus the pane extras (the heavy
// directory).
func checkEnv(extra map[string]string) []string {
	env := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		switch {
		case strings.HasPrefix(name, "HERDR_"), strings.HasPrefix(name, "CROWSNEST_"), strings.HasPrefix(name, "ELEVENLABS_"),
			name == "CLAUDECODE", strings.HasPrefix(name, "CLAUDE_CODE_"):
			continue
		case strings.HasSuffix(upper, "_TOKEN"), strings.HasSuffix(upper, "_SECRET"), strings.Contains(upper, "PASSWORD"),
			strings.HasSuffix(upper, "_API_KEY"), strings.HasSuffix(upper, "_ACCESS_KEY"):
			continue
		}
		env = append(env, entry)
	}
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return append(env, "CI=1", "CROWSNEST_CHECK=1")
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
	cut   bool
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = append([]byte{}, b.buf[len(b.buf)-b.limit:]...)
		b.cut = true
	}
	return len(p), nil
}

func (b *tailBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte{}, b.buf...)
}

// tailText is the last limit bytes of output as text, starting on a line boundary.
func tailText(output []byte, limit int) string {
	if len(output) > limit {
		output = output[len(output)-limit:]
		if i := bytes.IndexByte(output, '\n'); i >= 0 && i < len(output)-1 {
			output = output[i+1:]
		}
	}
	return strings.TrimRight(strings.ToValidUTF8(string(output), "?"), "\n ")
}

// writeCheckOutput keeps a check's output beside the job's files and returns the path.
func (s *Service) writeCheckOutput(id string, record store.CheckRecord, output []byte) string {
	dir, err := s.jobDir(id)
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("check-%d.txt", record.ID))
	header := fmt.Sprintf("# crowsnest check for %s: `%s` at %s: %s (exit %d, %s)\n\n", id, record.Command, record.Commit, record.Status, record.ExitCode,
		record.Duration.Round(time.Second))
	// Never through a link an agent left at that name (R6-S12): remove it, then create afresh.
	_ = os.Remove(path)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // crowsnest's own job directory
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append([]byte(header), output...)); err != nil {
		return ""
	}
	return path
}

// checkFinished applies a check's result to its job. record.Owner carries the output file.
func (s *Service) checkFinished(ctx context.Context, id string, record store.CheckRecord) {
	path := record.Owner
	review := ""
	message := ""
	opts := s.accept()
	summary := fmt.Sprintf("`%s` %s", oneLine(record.Command, 120), record.Status)
	if record.Status == CheckPassed || record.Status == CheckFailed {
		summary = fmt.Sprintf("`%s` %s at %s (exit %d, %s)", oneLine(record.Command, 120), record.Status, shortHash(record.Commit), record.ExitCode,
			record.Duration.Round(time.Second))
	}
	if record.DefinitionChanged != "" {
		summary += "; the branch changed its own check definition (" + oneLine(strings.ReplaceAll(record.DefinitionChanged, ",", ", "), 160) + ")"
	}
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if r.AcceptState != AcceptChecking {
			return errSkip
		}
		if record.Status == CheckPassed {
			// Accepted only on evidence that is in the database (R6-C13): the job's newest check
			// must be this one, passed, at this commit.
			latest, err := s.opts.Store.LatestCheck(ctx, r.ID)
			if err != nil || record.ID == 0 || latest.ID != record.ID || latest.Status != CheckPassed || latest.Commit != record.Commit {
				record.Status = CheckError
				record.OutputTail = "the passing check could not be found in crowsnest's records, so it does not count; run the check again"
			}
		}
		if State(r.State) == StateDone {
			// A re-check of a finished job (after a fix job, or the human's Re-run): evidence only.
			switch record.Status {
			case CheckPassed:
				r.AcceptState, r.AcceptDetail = AcceptAccepted, "Accepted: "+summary
			default:
				r.AcceptState, r.AcceptDetail = AcceptFailed, "The check failed: "+summary
			}
			c.event(EventAccept, "Re-checked: "+summary, map[string]any{"output": path})
			s.applyBaseLocked(ctx, r, c)
			return nil
		}
		if State(r.State) != StateInReview {
			return errSkip
		}
		switch record.Status {
		case CheckPassed:
			liftHoldLocked(r, c, HoldChecking, LiftCrowsnest, nil)
			r.AcceptState, r.AcceptDetail = AcceptAccepted, "Accepted: "+summary
			c.event(EventAccept, "Accepted: "+summary, map[string]any{"output": path, "commit": record.Commit})
			switch {
			case r.ReviewVerdict == VerdictApprove && r.ReviewCommit == record.Commit:
				r.State = string(StateDone)
			default:
				if review = s.reviewerFor(*r); review != "" {
					r.ReviewRound++
					r.ReviewCommit = record.Commit
					s.placeHoldLocked(r, c, HoldReviewing, fmt.Sprintf("a %s reviewer looks at the change (round %d)", review, r.ReviewRound), "")
					c.event(EventReview, fmt.Sprintf("In review (round %d of %d): a %s reviewer looks at the change that passed", r.ReviewRound, maxReviewRounds, review), nil)
				} else {
					r.State = string(StateDone)
				}
			}
		case CheckFailed:
			r.CheckRounds++
			if r.CheckRounds >= opts.maxRounds() {
				liftHoldLocked(r, c, HoldChecking, LiftCrowsnest, nil)
				r.State, r.AcceptState = string(StateRunning), AcceptFailed
				r.AcceptDetail = fmt.Sprintf("The check still fails after %d rounds: %s", r.CheckRounds, summary)
				s.placeHoldLocked(r, c, HoldCheck, r.AcceptDetail, "")
				c.event(EventAccept, r.AcceptDetail+"; the human decides", map[string]any{"output": path})
				c.notify = fmt.Sprintf("crowsnest: job %s's check still fails after %d rounds: %s", FormatID(r.ID), r.CheckRounds, oneLine(r.Title, 80))
				return nil
			}
			liftHoldLocked(r, c, HoldChecking, LiftCrowsnest, nil)
			r.State, r.AcceptState = string(StateRunning), AcceptFailed
			r.AcceptDetail = fmt.Sprintf("The check failed (round %d of %d): %s", r.CheckRounds, opts.maxRounds(), summary)
			s.markProgressLocked(r) // a fresh start for the nudge budget: there is new work
			c.event(EventAccept, "Not accepted: "+r.AcceptDetail+"; the output was sent to the agent", map[string]any{"output": path})
			message = fmt.Sprintf("[crowsnest check] Your done for job %s was not accepted: `%s` failed (exit %d, %s) at %s. The output is in %s. "+
				"Fix what it reports, commit, and call done again (round %d of %d).",
				FormatID(r.ID), oneLine(record.Command, 120), record.ExitCode, record.Duration.Round(time.Second), shortHash(record.Commit),
				firstNonEmptyDir(path, "job_get (the check output)"), r.CheckRounds, opts.maxRounds())
			if last := lastMeaningfulLine(record.OutputTail); last != "" {
				message += " Its last line: " + oneLine(last, 200)
			}
			// Kept until it is typed (R6-C07): sending it starts a builder turn, which waits for
			// the pause to end and for any hold to be lifted (deliverFeedback).
			r.PendingFeedback = message
		default: // error: it could not run at all; rounds are not the agent's fault
			liftHoldLocked(r, c, HoldChecking, LiftCrowsnest, nil)
			r.State, r.AcceptState = string(StateRunning), AcceptFailed
			r.AcceptDetail = "The check could not run: " + oneLine(record.OutputTail, 300)
			s.placeHoldLocked(r, c, HoldCheck, r.AcceptDetail, "")
			c.event(EventAccept, r.AcceptDetail, map[string]any{"output": path})
			c.notify = fmt.Sprintf("crowsnest: job %s's check could not run: %s", FormatID(r.ID), oneLine(r.Title, 80))
		}
		return nil
	})
	if err != nil {
		return
	}
	if review != "" {
		s.startReview(ctx, job, review)
	}
	if message != "" {
		s.deliverFeedback(ctx, id)
	}
	if job.State == StateDone {
		s.afterSettled(ctx, job)
	}
}

// lastMeaningfulLine is the last line of output that is not blank or a bare prompt.
func lastMeaningfulLine(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "[crowsnest]") {
			return line
		}
	}
	return ""
}

// applyBaseLocked marks a finished job needs_rebase when its base branch has moved on (s.mu held).
func (s *Service) applyBaseLocked(ctx context.Context, r *store.JobRecord, c *change) {
	if r.WorktreeBranch == "" || r.BaseBranch == "" || r.AcceptState != AcceptAccepted {
		return
	}
	snap, err := landing.Snap(ctx, workDirOf(*r), r.BaseBranch, r.BaseCommit)
	if err == nil && snap.Based && snap.Behind > 0 {
		r.AcceptState = AcceptNeedsRebase
		r.AcceptDetail = fmt.Sprintf("%s moved on by %d commit(s) since the branch started: rebase or merge it and check again", r.BaseBranch, snap.Behind)
		c.event(EventAccept, "Needs rebase: "+r.AcceptDetail, nil)
	}
}

// ---- Evidence, staleness and the human's actions -------------------------------------------

// EvidenceOf is a job's latest check, with whether the worktree has moved past it. A finished
// job whose evidence went stale is marked so (accept_state stale).
func (s *Service) EvidenceOf(ctx context.Context, job Job) *Evidence {
	numeric, err := ParseID(job.ID)
	if err != nil {
		return nil
	}
	check, err := s.opts.Store.LatestCheck(ctx, numeric)
	if err != nil {
		return nil
	}
	evidence := &Evidence{Command: check.Command, Status: check.Status, ExitCode: check.ExitCode, DurationMS: check.Duration.Milliseconds(),
		Commit: check.Commit, OutputTail: check.OutputTail, StartedAt: check.StartedAt.UTC(), FinishedAt: timePtr(check.FinishedAt),
		DefinitionChanged: splitList(check.DefinitionChanged), Mode: firstNonEmptyDir(check.Mode, CheckModeWorktree)}
	if job.TornDownAt != nil || job.WorkDir == "" || check.Status == CheckRunning {
		return evidence
	}
	if head, err := landing.Head(ctx, firstNonEmptyDir(job.WorkDir, job.Dir)); err == nil {
		evidence.Head = head
		evidence.Stale = head != check.Commit
	}
	if evidence.Stale && job.State == StateDone && (job.AcceptState == AcceptAccepted || job.AcceptState == AcceptFailed) {
		_, _ = s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
			if r.AcceptState != AcceptAccepted && r.AcceptState != AcceptFailed {
				return errSkip
			}
			r.AcceptState = AcceptStale
			r.AcceptDetail = fmt.Sprintf("Stale: the check ran at %s, the branch is now at %s; run it again", shortHash(check.Commit), shortHash(evidence.Head))
			c.event(EventAccept, r.AcceptDetail, nil)
			return nil
		})
	}
	return evidence
}

// Recheck runs a finished (or waiting) job's check again: the human's "Run the check again".
func (s *Service) Recheck(ctx context.Context, id string) (Job, error) {
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if !r.TornDownAt.IsZero() {
			return invalid("job %s's worktree is gone; there is nothing to check", FormatID(r.ID))
		}
		if !s.acceptApplies(ctx, *r) {
			return invalid("job %s is not checked by crowsnest (no git checkout, or acceptance is off)", FormatID(r.ID))
		}
		if r.AcceptState == AcceptChecking {
			return invalid("job %s's check is already running", FormatID(r.ID))
		}
		// The same prerequisite as done (R6-C02): only committed work is checked.
		if snap, err := landing.Snap(ctx, workDirOf(*r), "", ""); err != nil {
			return invalid("job %s's worktree could not be read: %v", FormatID(r.ID), err)
		} else if !snap.Clean {
			return invalid("job %s's worktree has uncommitted changes (%s); a check now would not test what lands. Commit or remove them first",
				FormatID(r.ID), strings.Join(capList(snap.Dirty, 6), "; "))
		}
		switch State(r.State) {
		case StateDone:
		case StateNeedsYou:
			if !hasHold(*r, HoldCheck) {
				return invalid("job %s needs you for something else (%s)", FormatID(r.ID), r.Cause)
			}
			liftHoldLocked(r, c, HoldCheck, LiftHuman, nil)
			s.placeHoldLocked(r, c, HoldChecking, "crowsnest is running the check again", "")
			r.State, r.Cause, r.CauseDetail = string(StateInReview), "", ""
			r.CheckRounds = 0
		default:
			return invalid("job %s is %s; the check runs when its agent calls done", FormatID(r.ID), r.State)
		}
		if command, _ := s.checkCommandFor(ctx, *r); command == "" {
			return invalid("job %s's project has no check command", FormatID(r.ID))
		}
		r.AcceptState, r.AcceptDetail = AcceptChecking, "crowsnest is running the check again"
		c.event(EventAccept, "You asked for the check to run again", nil)
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	s.startCheck(ctx, id)
	return s.jobNow(ctx, id, job), nil
}

// AskRebase starts a fix job in a finished job's worktree: merge its base in, resolve, check,
// commit. When it is done, crowsnest checks the job again.
func (s *Service) AskRebase(ctx context.Context, id string) (Job, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	job := detail.Job
	switch {
	case job.WorktreeBranch == "" || job.WorkDir == "" || job.TornDownAt != nil:
		return Job{}, invalid("job %s has no worktree to rebase", job.ID)
	case job.State != StateDone:
		return Job{}, invalid("job %s is %s; only a finished job is rebased this way", job.ID, job.State)
	case job.LandState != "":
		return Job{}, invalid("job %s was already %s", job.ID, landWord(job.LandState))
	case job.BaseBranch == "":
		return Job{}, invalid("crowsnest does not know the branch job %s started from", job.ID)
	}
	numeric, _ := ParseID(job.ID)
	applied := int64(0)
	if record, err := s.opts.Store.GetJob(ctx, numeric); err == nil {
		applied = record.FixApplied
	}
	if fixes, err := s.opts.Store.ListFixJobs(ctx, numeric); err == nil {
		for _, fix := range fixes {
			if !State(fix.State).Terminal() {
				return Job{}, invalid("job %s is already being rebased by %s", job.ID, FormatID(fix.ID))
			}
			if fix.ID > applied {
				// R6-C11: a finished rebase whose result is not applied yet; the tick applies it.
				return Job{}, invalid("job %s's rebase by %s has finished and is being checked; wait for that result", job.ID, FormatID(fix.ID))
			}
		}
	}
	off := false
	brief := fmt.Sprintf("Bring job %s's branch up to date with %s.\n\n"+
		"You are in the job's own worktree (%s, branch %s). %s has moved on since the branch started. "+
		"Merge %s into %s (`git merge %s`), resolve any conflicts keeping both sides' intent, run the project's checks "+
		"(through `crowsnest heavy`), commit, and call done saying what conflicted and how you resolved it. "+
		"Do not push, and do not switch branches. crowsnest runs the check again when you are done.\n\n"+
		"## The job's brief\n\n%s", job.ID, job.BaseBranch, job.WorkDir, job.WorktreeBranch, job.BaseBranch, job.BaseBranch, job.WorktreeBranch,
		job.BaseBranch, indent(job.BriefMD))
	fix, err := s.Create(ctx, CreateInput{Brief: brief, Dir: job.WorkDir, Role: "builder", Engine: job.Engine, Model: job.Model, Isolate: &off,
		Reviewer: ReviewerOff, fixOf: numeric}, job.ID)
	if err != nil {
		return Job{}, err
	}
	_ = s.Journal(ctx, job.ID, EventAccept, "Rebase asked of "+fix.ID+": merge "+job.BaseBranch+" in, then check again", map[string]any{"fix": fix.ID})
	return fix, nil
}

// fixSettled runs when a fix job finishes: its parent is checked again.
func (s *Service) fixSettled(ctx context.Context, fix Job) {
	if fix.FixOf == "" {
		return
	}
	fixID := mustParse(fix.ID)
	parent, err := s.mutate(ctx, fix.FixOf, func(r *store.JobRecord, c *change) error {
		if State(r.State) != StateDone || r.LandState != "" || r.FixApplied >= fixID {
			return errSkip // already taken in (the tick's replay and the callback can both arrive)
		}
		r.FixApplied = fixID
		if fix.State != StateDone {
			c.event(EventAccept, "The rebase by "+fix.ID+" did not finish ("+string(fix.State)+"); the job still needs one", nil)
			return nil
		}
		r.AcceptState, r.AcceptDetail = AcceptChecking, "Rebased by "+fix.ID+"; crowsnest is running the check again"
		c.event(EventAccept, r.AcceptDetail, nil)
		return nil
	})
	if err == nil && parent.AcceptState == AcceptChecking {
		s.startCheck(ctx, parent.ID)
	}
}

// reconcileFixes replays finished fix jobs whose parent never took them in (crowsnest stopped
// between the fix's done and its parent's re-check): exactly one re-check each (R6-C11).
func (s *Service) reconcileFixes(ctx context.Context) {
	fixes, err := s.opts.Store.ListUnappliedFixes(ctx, superviseBatch)
	if err != nil {
		return
	}
	for _, fix := range fixes {
		job := s.toJob(fix, 0)
		_ = s.Journal(ctx, job.FixOf, EventAccept, "The rebase by "+job.ID+" finished while its result was not applied (crowsnest stopped); applying it now", nil)
		s.fixSettled(ctx, job)
	}
}

// LandEvidence says whether a job's evidence lets Land merge or push it: code is "" when it
// does, else one of stale, failed, missing, checking, needs_rebase, with a sentence.
func (s *Service) LandEvidence(ctx context.Context, job Job, snap *landing.Snapshot) (code, why string) {
	if job.AcceptState == AcceptNone || job.WorktreeBranch == "" || job.WorkDir == "" {
		return "", ""
	}
	if job.AcceptState == AcceptChecking {
		return "checking", "crowsnest is running the check; wait for it"
	}
	if snap == nil {
		if read, err := landing.Snap(ctx, job.WorkDir, job.BaseBranch, s.baseCommitOf(ctx, job.ID)); err == nil {
			snap = &read
		}
	}
	if snap != nil && snap.Based && snap.Behind > 0 && job.BaseBranch != "" {
		return AcceptNeedsRebase, fmt.Sprintf("%s has moved on by %d commit(s): the branch needs a rebase (or merge) and a fresh check before it lands", job.BaseBranch, snap.Behind)
	}
	if job.AcceptState == AcceptUnverified {
		return "", ""
	}
	numeric, _ := ParseID(job.ID)
	check, err := s.opts.Store.LatestCheck(ctx, numeric)
	switch {
	case err != nil:
		return "missing", "crowsnest has no check of this branch"
	case check.Status != CheckPassed:
		return "failed", fmt.Sprintf("the last check (`%s` at %s) %s", oneLine(check.Command, 80), shortHash(check.Commit), check.Status)
	case snap != nil && snap.Head != "" && snap.Head != check.Commit:
		return AcceptStale, fmt.Sprintf("the check passed at %s, but the branch is now at %s: run it again", shortHash(check.Commit), shortHash(snap.Head))
	}
	return "", ""
}

func (s *Service) baseCommitOf(ctx context.Context, id string) string {
	numeric, err := ParseID(id)
	if err != nil {
		return ""
	}
	r, err := s.opts.Store.GetJob(ctx, numeric)
	if err != nil {
		return ""
	}
	return r.BaseCommit
}

// tickChecking finds jobs waiting on a check nobody is running (crowsnest restarted mid-check)
// and runs it again.
func (s *Service) tickChecking(ctx context.Context, r store.JobRecord) {
	if r.AcceptState != AcceptChecking {
		return
	}
	id := FormatID(r.ID)
	if s.checkRunning(id) {
		return
	}
	if check, err := s.opts.Store.LatestCheck(ctx, r.ID); err == nil && check.Status == CheckRunning && check.Owner == s.instance {
		return // this process's run, between its last write and the job's
	}
	_ = s.Journal(ctx, id, EventAccept, "The check had nobody running it (crowsnest restarted); running it again", nil)
	s.startCheck(ctx, id)
}

// ProjectAccept is a project's acceptance and budget defaults as the app edits them.
type ProjectAccept struct {
	ProjectID     int64   `json:"project_id"`
	CheckCommand  string  `json:"check_command"` // "" automatic, "none", or a command
	BudgetUSD     float64 `json:"budget_usd"`
	BudgetMinutes int     `json:"budget_minutes"`
	// Effective is the check that would run now in the project's directory, and why.
	Effective       string `json:"effective"`
	EffectiveSource string `json:"effective_source"`
	// RestoredCheckCommand is the command a restore set aside (R7-S4): not run until the human
	// saves a check command (this one, or another), which clears it.
	RestoredCheckCommand string `json:"restored_check_command,omitempty"`
	// CheckIsolated runs the check in a temporary detached worktree of exactly the commit, after
	// CheckPrepare (installing dependencies; "" for none). R7-C01. RestoredCheckPrepare is a prepare
	// command a restore set aside, like RestoredCheckCommand.
	CheckIsolated        bool   `json:"check_isolated"`
	CheckPrepare         string `json:"check_prepare"`
	RestoredCheckPrepare string `json:"restored_check_prepare,omitempty"`
}

// ProjectAcceptOf reads a project's acceptance defaults; dir is its directory, for Effective.
func (s *Service) ProjectAcceptOf(ctx context.Context, projectID int64, dir string) (ProjectAccept, error) {
	stored, err := s.opts.Store.GetProjectAccept(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ProjectAccept{}, ErrNotFound
		}
		return ProjectAccept{}, err
	}
	out := ProjectAccept{ProjectID: projectID, CheckCommand: stored.CheckCommand, BudgetUSD: stored.BudgetUSD, BudgetMinutes: stored.BudgetMinutes,
		CheckIsolated: stored.CheckIsolated, CheckPrepare: stored.CheckPrepare}
	out.Effective, out.EffectiveSource = s.checkCommandFor(ctx, store.JobRecord{ProjectID: projectID, Dir: dir})
	if aside, err := s.opts.Store.GetKV(ctx, store.RestoredCheckKey(projectID)); err == nil {
		out.RestoredCheckCommand = aside
	}
	if aside, err := s.opts.Store.GetKV(ctx, store.RestoredPrepareKey(projectID)); err == nil {
		out.RestoredCheckPrepare = aside
	}
	return out, nil
}

// SetProjectAccept writes a project's acceptance defaults (the human's, from the app).
func (s *Service) SetProjectAccept(ctx context.Context, projectID int64, input ProjectAccept) error {
	command := strings.TrimSpace(input.CheckCommand)
	prepare := strings.TrimSpace(input.CheckPrepare)
	switch {
	case strings.ContainsAny(command, "\n\r\x00"), strings.ContainsAny(prepare, "\n\r\x00"):
		return invalid("the check and prepare commands are one line each (use && to chain, or a mise task)")
	case len(command) > 500 || len(prepare) > 500:
		return invalid("a check or prepare command is longer than 500 characters; put it in a mise task or a script")
	case input.BudgetUSD < 0 || input.BudgetMinutes < 0:
		return invalid("a budget is a positive number, or 0 for the server's default")
	}
	err := s.opts.Store.SetProjectAccept(ctx, projectID, store.ProjectAccept{CheckCommand: command, BudgetUSD: input.BudgetUSD, BudgetMinutes: input.BudgetMinutes,
		CheckIsolated: input.CheckIsolated, CheckPrepare: prepare})
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// The human has now said which check runs: a command a restore set aside is settled (R7-S4).
	if err := s.opts.Store.DeleteKV(ctx, store.RestoredCheckKey(projectID)); err != nil {
		return err
	}
	return s.opts.Store.DeleteKV(ctx, store.RestoredPrepareKey(projectID))
}

// deliverFeedback types a job's pending findings (a check's failure, R6-C07, or a review's
// changes) to its agent. It is a lifecycle step (opFeedback, lifecycle.go), so exactly one caller
// owns a delivery: the check's callback, the review's and the tick can all call it, and the claim
// on the row lets one through (R7-C05). It passes the continuation gate before the claim and again
// just before typing (R7-C04): not while paused, not under any hold, not while a budget above the
// job or the fleet's usage limit holds it. The message is cleared only when it was (or may have
// been) typed, and only if it is still the one typed; a crowsnest that stopped mid-send leaves a
// stale claim, which is settled as "may have arrived, not typed again" (resolveStaleLocked), never
// replayed blindly. A send that typed nothing is journalled (at most every few minutes) and tried
// again by the tick.
func (s *Service) deliverFeedback(ctx context.Context, id string) {
	if why := s.continuationRefusal(ctx, id); why != "" {
		s.noteHeldContinuation(ctx, id, "the findings for the agent", why)
		return
	}
	var message, paneID, agent string
	cl, err := s.claimStep(ctx, id, opFeedback, func(r *store.JobRecord, c *change) error {
		if r.PendingFeedback == "" || r.PaneID == "" || State(r.State) != StateRunning || len(effectiveHolds(*r, c.questions)) > 0 {
			return errSkip
		}
		message, paneID, agent = r.PendingFeedback, r.PaneID, r.AgentName
		return nil
	})
	if err != nil {
		return
	}
	if why := s.continuationRefusal(ctx, id); why != "" {
		s.finishQuietly(ctx, cl, nil)
		s.noteHeldContinuation(ctx, id, "the findings for the agent", why)
		return
	}
	var result orch.PromptResult
	var sendErr error
	if err := s.effect(ctx, cl, paneID, func(ctx context.Context) {
		result, sendErr = orch.Prompt(ctx, s.opts.Herdr, paneID, orch.PromptRequest{Text: message, ForceOverDraft: true, ExpectAgent: agent})
	}); err != nil {
		return
	}
	outcome := typedOutcome(result, sendErr)
	watch := s.watchOf(id)
	s.finishQuietly(ctx, cl, func(rec *store.JobRecord, c *change) {
		switch {
		case outcome == typedNo:
			if now := s.opts.Now(); now.Sub(watch.feedbackAt) > 5*time.Minute {
				watch.feedbackAt = now
				c.event(EventAccept, "The findings could not be typed to the agent yet: "+oneLine(sendErr.Error(), 160)+"; crowsnest tries again", nil)
			}
		case rec.PendingFeedback != message:
			// Replaced while it was typed (a newer check or review): the newer one is still due.
		default:
			rec.PendingFeedback = ""
			if outcome == typedMaybe {
				c.event(EventAccept, "crowsnest lost track of typing the findings; counted as delivered, not typed again", nil)
			} else {
				c.event(EventAccept, "The findings reached the agent", nil)
			}
		}
	})
}

// splitList reads a comma-separated list ("" is empty).
func splitList(text string) []string {
	out := []string{}
	for _, item := range strings.Split(text, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
