// The heavy-command gate (DESIGN.md 6.12): `crowsnest heavy <command…>` runs a build, a test
// suite or a typecheck so the machine stays usable, the way Jeff's own `heavy` script does: at
// most Slots such commands at a time for this instance, each at low priority with its tools'
// parallelism capped, and anyone waiting told so and why.
//
// The slots are files in the instance's heavy directory (<state>/heavy), each taken with an
// exclusive, non-blocking flock(2). The kernel drops a lock when its holder dies, so a crashed or
// killed command never wedges a slot (the script's mkdir locks needed stale-holder detection for
// that). The holder writes who it is into its slot file (pid, job, command, directory, since)
// for `--status` and the System page; a waiting command writes a wait file the same way and
// removes it when it gets a slot or gives up.
//
// Reentrant: the command runs with CROWSNEST_HEAVY_HOLDER set, and a `crowsnest heavy` under it
// runs at once instead of deadlocking on its own parent's slot.

package throttle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// DefaultHeavySlots is how many heavy commands may run at once (Jeff's heavy: two).
	DefaultHeavySlots = 2
	// heavyConfigFile is what serve writes into the heavy directory so the CLI in an agent's pane
	// uses the instance's slot count without a flag.
	heavyConfigFile = "config.json"
	// HolderEnv marks a process tree that already holds a slot.
	HolderEnv = "CROWSNEST_HEAVY_HOLDER"
	// DirEnv names the heavy directory in an agent's pane.
	DirEnv = "CROWSNEST_HEAVY_DIR"
	// sayEvery is how often a waiting command repeats why it waits.
	sayEvery = time.Minute
	// maxCommandRunes bounds the command recorded in a slot file.
	maxCommandRunes = 200
)

// HeavyConfig is the instance's heavy settings as serve writes them.
type HeavyConfig struct {
	Slots int `json:"slots"`
}

// WriteHeavyConfig makes dir (0700) and records the slot count in it. serve calls it at start.
func WriteHeavyConfig(dir string, slots int) error {
	if slots < 1 {
		slots = DefaultHeavySlots
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating the heavy directory %s: %w", dir, err)
	}
	body, _ := json.Marshal(HeavyConfig{Slots: slots})
	tmp := filepath.Join(dir, heavyConfigFile+".tmp")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("writing the heavy configuration: %w", err)
	}
	return os.Rename(tmp, filepath.Join(dir, heavyConfigFile))
}

// ReadHeavyConfig reads dir's slot count; a missing or broken file is the default.
func ReadHeavyConfig(dir string) HeavyConfig {
	config := HeavyConfig{Slots: DefaultHeavySlots}
	raw, err := os.ReadFile(filepath.Join(dir, heavyConfigFile)) //nolint:gosec // the instance's own state directory
	if err == nil {
		var read HeavyConfig
		if json.Unmarshal(raw, &read) == nil && read.Slots > 0 {
			config = read
		}
	}
	return config
}

// HeavyOwner is who holds or waits for a slot.
type HeavyOwner struct {
	PID     int       `json:"pid"`
	Job     string    `json:"job,omitempty"` // CROWSNEST_JOB of the caller, when it is a job agent
	Command string    `json:"command"`
	Dir     string    `json:"dir"`
	Since   time.Time `json:"since"`
	Slot    int       `json:"slot,omitempty"` // 1-based; 0 for a waiter
}

// HeavyStatus is the gate as the API shows it.
type HeavyStatus struct {
	Slots   int          `json:"slots"`
	Running []HeavyOwner `json:"running"`
	Waiting []HeavyOwner `json:"waiting"`
	// Deferred names an outside `heavy` on PATH that `crowsnest heavy` hands commands to instead
	// (its slots are its own and are not listed here); empty when crowsnest gates them itself.
	Deferred string `json:"deferred,omitempty"`
}

// HeavyOptions configure one gated run.
type HeavyOptions struct {
	Dir     string
	Slots   int           // 0: the directory's configuration
	Poll    time.Duration // how often a waiter tries again; default 1 s
	Timeout time.Duration // how long to wait for a slot; 0: an hour
	Job     string        // recorded with the slot
	Stderr  io.Writer     // where waiting is announced; nil: os.Stderr
	Now     func() time.Time
}

// ErrHeavyTimeout is returned when no slot came free in time; nothing was run.
var ErrHeavyTimeout = errors.New("no heavy slot came free in time; nothing was run")

// HeavySlot is a slot held by this process. Release gives it back.
type HeavySlot struct {
	file  *os.File
	index int
}

// Index is the slot's number, from 1.
func (s *HeavySlot) Index() int { return s.index }

// Release gives the slot back. Safe to call twice.
func (s *HeavySlot) Release() {
	if s == nil || s.file == nil {
		return
	}
	_ = s.file.Truncate(0)
	_ = unix.Flock(int(s.file.Fd()), unix.LOCK_UN)
	_ = s.file.Close()
	s.file = nil
}

// AcquireHeavy waits for a free slot and takes it, announcing on Stderr what it waits for (once,
// then every minute). It gives up when ctx ends or the timeout passes.
func AcquireHeavy(ctx context.Context, opts HeavyOptions, command string) (*HeavySlot, error) {
	opts = opts.withDefaults()
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the heavy directory %s: %w", opts.Dir, err)
	}
	owner := HeavyOwner{PID: os.Getpid(), Job: opts.Job, Command: clip(command, maxCommandRunes), Since: opts.Now()}
	owner.Dir, _ = os.Getwd()
	start := opts.Now()
	waitFile := ""
	defer func() {
		if waitFile != "" {
			_ = os.Remove(waitFile)
		}
	}()
	var lastSaid time.Time
	for {
		if slot, err := tryHeavySlots(opts.Dir, opts.Slots, owner); err != nil || slot != nil {
			if slot != nil && !lastSaid.IsZero() {
				_, _ = fmt.Fprintf(opts.Stderr, "crowsnest heavy: got slot %d after %s\n", slot.index, opts.Now().Sub(start).Round(time.Second))
			}
			return slot, err
		}
		now := opts.Now()
		if now.Sub(start) >= opts.Timeout {
			return nil, ErrHeavyTimeout
		}
		if waitFile == "" {
			waitFile = writeWaitFile(opts.Dir, owner)
		}
		if lastSaid.IsZero() || now.Sub(lastSaid) >= sayEvery {
			status := ReadHeavyStatus(opts.Dir, opts.Slots)
			_, _ = fmt.Fprintf(opts.Stderr, "crowsnest heavy: all %d slots busy, waiting for %s (%s so far)\n",
				opts.Slots, describeHolders(status.Running), now.Sub(start).Round(time.Second))
			lastSaid = now
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(opts.Poll):
		}
	}
}

func (o HeavyOptions) withDefaults() HeavyOptions {
	if o.Slots <= 0 {
		o.Slots = ReadHeavyConfig(o.Dir).Slots
	}
	if o.Poll <= 0 {
		o.Poll = time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = time.Hour
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// tryHeavySlots takes the first free slot, or returns nil when every one is held.
func tryHeavySlots(dir string, slots int, owner HeavyOwner) (*HeavySlot, error) {
	for i := 1; i <= slots; i++ {
		path := slotPath(dir, i)
		file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // the instance's own state directory
		if err != nil {
			return nil, fmt.Errorf("opening heavy slot %d: %w", i, err)
		}
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			_ = file.Close()
			if errors.Is(err, unix.EWOULDBLOCK) {
				continue
			}
			return nil, fmt.Errorf("locking heavy slot %d: %w", i, err)
		}
		owner.Slot = i
		body, _ := json.Marshal(owner)
		_ = file.Truncate(0)
		_, _ = file.WriteAt(body, 0)
		return &HeavySlot{file: file, index: i}, nil
	}
	return nil, nil
}

func slotPath(dir string, i int) string { return filepath.Join(dir, "slot."+strconv.Itoa(i)) }

func writeWaitFile(dir string, owner HeavyOwner) string {
	path := filepath.Join(dir, fmt.Sprintf("wait.%d", owner.PID))
	body, _ := json.Marshal(owner)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return ""
	}
	return path
}

// ReadHeavyStatus lists who holds and who waits for this directory's slots. A slot is held when
// its lock cannot be taken; a waiter is listed while its process lives.
func ReadHeavyStatus(dir string, slots int) HeavyStatus {
	if slots <= 0 {
		slots = ReadHeavyConfig(dir).Slots
	}
	status := HeavyStatus{Slots: slots, Running: []HeavyOwner{}, Waiting: []HeavyOwner{}}
	for i := 1; i <= slots; i++ {
		if owner, held := slotHolder(dir, i); held {
			status.Running = append(status.Running, owner)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "wait.") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name())) //nolint:gosec // the instance's own state directory
		var owner HeavyOwner
		if err != nil || json.Unmarshal(raw, &owner) != nil || !processAlive(owner.PID) {
			continue
		}
		status.Waiting = append(status.Waiting, owner)
	}
	sort.Slice(status.Waiting, func(i, j int) bool { return status.Waiting[i].Since.Before(status.Waiting[j].Since) })
	return status
}

// slotHolder reports whether slot i is held, and by whom. Testing takes a shared lock for an
// instant; an acquirer that loses that race simply takes the slot on its next poll.
func slotHolder(dir string, i int) (HeavyOwner, bool) {
	file, err := os.Open(slotPath(dir, i)) //nolint:gosec // the instance's own state directory
	if err != nil {
		return HeavyOwner{}, false
	}
	defer func() { _ = file.Close() }()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err == nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		return HeavyOwner{}, false
	}
	raw, _ := io.ReadAll(io.LimitReader(file, 4096))
	owner := HeavyOwner{Slot: i, Command: "another command"}
	_ = json.Unmarshal(raw, &owner)
	owner.Slot = i
	return owner, true
}

func describeHolders(owners []HeavyOwner) string {
	if len(owners) == 0 {
		return "another command"
	}
	parts := make([]string, 0, len(owners))
	for _, owner := range owners {
		part := owner.Command
		if owner.Job != "" {
			part += " (job " + owner.Job + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// ---- Running a command under a slot ---------------------------------------------------------

// CapEnv returns the environment caps a heavy command runs with: each tool's parallelism held to
// a third of the cores (at least 2), set only where the caller has not set them. Copied from
// Jeff's heavy, which found that environment caps are what most tools obey.
func CapEnv(environ []string) []string {
	n := runtime.NumCPU() / 3
	if n < 2 {
		n = 2
	}
	value := strconv.Itoa(n)
	caps := [][2]string{
		{"MAKEFLAGS", "-j" + value}, {"GOFLAGS", "-p=" + value}, {"CARGO_BUILD_JOBS", value},
		{"VITEST_MAX_THREADS", value}, {"VITEST_MAX_FORKS", value},
		{"MSBUILDDISABLENODEREUSE", "1"}, {"DOTNET_CLI_USE_MSBUILD_SERVER", "0"}, {"UseSharedCompilation", "false"},
	}
	out := append([]string{}, environ...)
	for _, pair := range caps {
		if !hasEnv(out, pair[0]) {
			out = append(out, pair[0]+"="+pair[1])
		}
	}
	return out
}

func hasEnv(environ []string, name string) bool {
	for _, entry := range environ {
		if strings.HasPrefix(entry, name+"=") {
			return true
		}
	}
	return false
}

// NiceArgs wraps argv in `nice -n 10` when nice exists (it does on macOS and Linux), so a heavy
// command yields to the human's own work.
func NiceArgs(argv []string, lookPath func(string) (string, error)) []string {
	if path, err := lookPath("nice"); err == nil {
		return append([]string{path, "-n", "10"}, argv...)
	}
	return argv
}

// OutsideHeavy finds a `heavy` on PATH that is not crowsnest itself (Jeff's machines have one):
// `crowsnest heavy` hands commands to it rather than wrapping twice, so one machine has one set
// of slots. self is this binary's resolved path. Empty: none, or CROWSNEST_HEAVY_DEFER=0.
func OutsideHeavy(lookPath func(string) (string, error), self string, deferSetting string) string {
	if deferSetting == "0" || deferSetting == "false" {
		return ""
	}
	path, err := lookPath("heavy")
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	if self != "" && resolved == self {
		return ""
	}
	return path
}

func clip(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}
