// The machine: crowsnest's directories, its database, the agent socket's path length, the port,
// what the bind resolves to, the secure-context note for voice and push, git, disk space and
// the clock.

package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/store"
)

// Disk thresholds for the data volume: below lowDisk is a warning, below criticalDisk a failure
// (SQLite and attachments start failing writes; agents' builds fail sooner).
const (
	lowDisk      = 2 << 30   // 2 GiB
	criticalDisk = 200 << 20 // 200 MiB
)

// earliestSaneTime is a date the clock cannot honestly be before: the build of this code.
// A clock behind it breaks TLS (update checks, ElevenLabs) and session expiry.
var earliestSaneTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// SocketPathMax is the longest unix socket path the OS accepts, by GOOS (sun_path minus the
// terminating NUL).
func SocketPathMax(goos string) int {
	if goos == "linux" {
		return 107
	}
	return 103
}

func (r *runner) machineChecks(ctx context.Context) {
	if r.configOK() {
		cfg := r.in.Config
		r.report.Paths = Paths{
			Data: cfg.DataDir, State: cfg.StateDir, Config: r.configDir(), Database: cfg.DatabasePath(),
			Orchestrator: cfg.Dir, AgentSocket: r.in.AgentSocket,
			Herdr: r.report.Paths.Herdr, Claude: r.report.Paths.Claude, Codex: r.report.Paths.Codex,
		}
		r.dirCheck("machine.data_dir", "Data directory", cfg.DataDir)
		r.dirCheck("machine.state_dir", "State directory", cfg.StateDir)
		r.dirCheck("machine.config_dir", "Config directory", r.configDir())
		r.databaseCheck(ctx)
		r.socketCheck()
		r.portCheck(ctx)
		r.bindCheck()
		r.secureContextCheck()
		r.diskCheck()
	}
	r.gitCheck()
	r.clockCheck()
}

// configDir is $XDG_CONFIG_HOME/crowsnest, else ~/.config/crowsnest (the default config file's
// directory and the roles directory).
func (r *runner) configDir() string {
	if r.in.Config.ConfigFile != "" {
		return filepath.Dir(r.in.Config.ConfigFile)
	}
	if value, ok := r.env.Getenv("XDG_CONFIG_HOME"); ok && filepath.IsAbs(value) {
		return filepath.Join(value, "crowsnest")
	}
	return filepath.Join(r.env.Home, ".config", "crowsnest")
}

// dirCheck: exists and writable, and private. A missing directory is fine (serve creates it,
// 0700); a missing one whose nearest parent is not writable is not.
func (r *runner) dirCheck(id, title, dir string) {
	check := Check{ID: id, Group: GroupMachine, Title: title}
	info, err := r.env.Stat(dir)
	switch {
	case err != nil:
		parent := nearestExisting(r.env, dir)
		if r.env.Writable(parent) {
			check.Status, check.Detail = StatusOK, dir+" (created at first start, mode 0700)"
		} else {
			check.Status, check.Detail = StatusFail, dir+" does not exist and "+parent+" is not writable"
			check.Fix = "Create it yourself (`mkdir -p " + dir + "`) or point crowsnest elsewhere with --data-dir / XDG_*"
		}
	case !info.IsDir():
		check.Status, check.Detail, check.Fix = StatusFail, dir+" is not a directory", "Move it aside"
	case !r.env.Writable(dir):
		check.Status, check.Detail, check.Fix = StatusFail, dir+" is not writable", "`chmod u+rwx "+dir+"` or fix its owner"
	case info.Mode().Perm()&0o077 != 0:
		check.Status = StatusInfo
		check.Detail = fmt.Sprintf("%s (mode %s; crowsnest tightens it to 0700 at start)", dir, info.Mode().Perm())
	default:
		check.Status, check.Detail = StatusOK, dir+" (private)"
	}
	r.add(check)
}

func nearestExisting(env Env, dir string) string {
	for path := dir; ; path = filepath.Dir(path) {
		if _, err := env.Stat(path); err == nil || filepath.Dir(path) == path {
			return path
		}
	}
}

func (r *runner) databaseCheck(ctx context.Context) {
	path := r.in.Config.DatabasePath()
	check := Check{ID: "machine.database", Group: GroupMachine, Title: "Database"}
	if r.in.Config.Demo {
		check.Status, check.Detail = StatusOK, "demo mode: a throwaway database"
		r.add(check)
		return
	}
	inspection, err := store.Inspect(ctx, path)
	switch {
	case err != nil:
		check.Status, check.Detail = StatusFail, fmt.Sprintf("%s does not open: %v", path, err)
		check.Fix = "Move the file aside to start fresh (you will set up a login again), or restore a backup"
	case !inspection.Exists:
		check.Status, check.Detail = StatusOK, path+" (created at first start)"
	case inspection.SchemaVersion > inspection.KnownVersion:
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("schema %d is newer than this crowsnest knows (%d)", inspection.SchemaVersion, inspection.KnownVersion)
		check.Fix, check.Command = "Update crowsnest: `crowsnest update`", "crowsnest update"
	default:
		check.Status = StatusOK
		check.Detail = fmt.Sprintf("%s, schema %d of %d", path, inspection.SchemaVersion, inspection.KnownVersion)
		if inspection.SchemaVersion < inspection.KnownVersion {
			check.Detail += " (migrated at next start)"
		}
		if !inspection.Owner && r.in.Config.Username == "" && !r.in.Config.NoAuth {
			check.Detail += "; no login stored yet: first start opens setup"
		}
		if inspection.Mode&fs.FileMode(0o077) != 0 {
			check.Detail += fmt.Sprintf("; mode %s, tightened to 0600 at start", inspection.Mode)
		}
	}
	r.add(check)
}

func (r *runner) socketCheck() {
	path := r.in.AgentSocket
	limit := SocketPathMax(r.env.GOOS)
	check := Check{ID: "machine.socket", Group: GroupMachine, Title: "Agent socket path"}
	if len(path) > limit {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("%s is %d bytes; the OS allows %d", path, len(path), limit)
		check.Fix = "Use a shorter --session name, or set XDG_RUNTIME_DIR (or XDG_STATE_HOME) to a short directory"
	} else {
		check.Status, check.Detail = StatusOK, fmt.Sprintf("%s (%d of %d bytes)", path, len(path), limit)
	}
	r.add(check)
}

// portCheck binds each listener for a moment. A port in use is fine when it answers as
// crowsnest's own health endpoint: then this is a running instance (and Live says so).
func (r *runner) portCheck(ctx context.Context) {
	cfg := r.in.Config
	check := Check{ID: "machine.port", Group: GroupMachine, Title: fmt.Sprintf("Port %d", cfg.Port)}
	if r.in.Running {
		check.Status, check.Detail = StatusOK, "served by this crowsnest"
		r.add(check)
		return
	}
	for _, listener := range cfg.Listeners {
		err := r.env.Listen(listener.Addr.String())
		if err == nil {
			continue
		}
		if listener.Optional && !strings.Contains(err.Error(), "in use") {
			continue // ::1 on a machine without IPv6
		}
		if live := r.probeHealth(ctx, listener); live != nil {
			r.report.Live = live
			check.Status, check.Detail = StatusOK, fmt.Sprintf("in use by a running crowsnest %s at %s", live.Version, listener.Addr)
			r.add(check)
			return
		}
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("%s is taken by another program", listener.Addr)
		check.Fix = fmt.Sprintf("Stop that program, or run crowsnest on another port: `crowsnest --port %d` (or CROWSNEST_PORT)", cfg.Port+1)
		r.add(check)
		return
	}
	check.Status, check.Detail = StatusOK, "free"
	r.add(check)
}

// healthEnvelope is GET /api/v1/health's JSON.
type healthEnvelope struct {
	Data struct {
		Status        string `json:"status"`
		Version       string `json:"version"`
		SetupRequired bool   `json:"setup_required"`
		LatestVersion string `json:"latest_version"`
	} `json:"data"`
}

func (r *runner) probeHealth(ctx context.Context, listener config.Listener) *Live {
	scheme := "http"
	if r.in.Config.TLS() {
		scheme = "https"
	}
	host := listener.Addr.String()
	if listener.Addr.Addr().IsUnspecified() {
		host = fmt.Sprintf("127.0.0.1:%d", listener.Addr.Port())
	}
	status, body, err := r.env.HTTPGet(ctx, scheme+"://"+host+"/api/v1/health", map[string]string{"Host": host})
	if err != nil || status != 200 {
		return nil
	}
	var health healthEnvelope
	if json.Unmarshal(body, &health) != nil || health.Data.Status != "ok" {
		return nil
	}
	return &Live{Running: true, Version: health.Data.Version, SetupRequired: health.Data.SetupRequired, UpdateLatest: health.Data.LatestVersion}
}

func (r *runner) bindCheck() {
	cfg := r.in.Config
	check := Check{ID: "machine.bind", Group: GroupMachine, Title: "Listens on"}
	var addrs []string
	tailnet := false
	for _, listener := range cfg.Listeners {
		addrs = append(addrs, listener.Addr.String())
		tailnet = tailnet || config.IsTailnet(listener.Addr.Addr().Unmap())
	}
	check.Status = StatusOK
	check.Detail = strings.Join(addrs, ", ") + " (--bind " + strings.Join(cfg.Binds, ",") + ")"
	if !tailnet && cfg.LoopbackOnly() {
		if addr, ok := r.tailnetAddress(); ok {
			check.Detail += "; a tailnet address " + addr + " is available: --bind tailnet reaches this from your phone"
		}
	}
	r.add(check)
}

// tailnetAddress finds this machine's Tailscale address, if any.
func (r *runner) tailnetAddress() (string, bool) {
	interfaces, err := config.SystemInterfaces()
	if err != nil {
		return "", false
	}
	for _, iface := range interfaces {
		for _, addr := range iface.Addrs {
			if addr.Is4() && config.IsTailnet(addr) {
				return addr.String(), true
			}
		}
	}
	return "", false
}

// secureContextCheck: browsers give the microphone (talk mode) and Web Push only to a secure
// context: HTTPS, or a page on localhost.
func (r *runner) secureContextCheck() {
	cfg := r.in.Config
	check := Check{ID: "machine.secure_context", Group: GroupMachine, Title: "Secure context (voice, push)"}
	switch {
	case cfg.TLS():
		check.Status, check.Detail = StatusOK, "served over HTTPS"
	case cfg.LoopbackOnly():
		check.Status, check.Detail = StatusOK, "localhost counts as secure; from another device use HTTPS (`tailscale serve`)"
	default:
		check.Status = StatusInfo
		check.Detail = "plain HTTP on a network address: browsers there refuse the microphone and notifications"
		check.Fix = "Put HTTPS in front: `tailscale serve --bg " + fmt.Sprint(cfg.Port) + "`, or --tls-cert/--tls-key"
	}
	r.add(check)
}

func (r *runner) diskCheck() {
	check := Check{ID: "machine.disk", Group: GroupMachine, Title: "Disk space"}
	free, err := r.env.DiskFree(r.in.Config.DataDir)
	switch {
	case err != nil:
		check.Status, check.Detail = StatusSkip, "could not read free space"
	case free < criticalDisk:
		check.Status, check.Detail, check.Fix = StatusFail, HumanBytes(free)+" free on the data volume", "Free some space: the database, attachments and agents' builds need it"
	case free < lowDisk:
		check.Status, check.Detail, check.Fix = StatusWarn, HumanBytes(free)+" free on the data volume", "Free some space soon"
	default:
		check.Status, check.Detail = StatusOK, HumanBytes(free)+" free on the data volume"
	}
	r.add(check)
}

func (r *runner) gitCheck() {
	check := Check{ID: "machine.git", Group: GroupMachine, Title: "git"}
	if path, err := r.env.LookPath("git"); err == nil {
		check.Status, check.Detail = StatusOK, path
	} else {
		check.Status, check.Detail = StatusWarn, "not installed: jobs cannot get their own worktree"
		check.Fix = "Install git (macOS: `xcode-select --install`)"
	}
	r.add(check)
}

func (r *runner) clockCheck() {
	now := r.env.Now()
	check := Check{ID: "machine.clock", Group: GroupMachine, Title: "Clock"}
	if now.Before(earliestSaneTime) {
		check.Status, check.Detail = StatusFail, "the clock says "+now.Format(time.RFC3339)
		check.Fix = "Set the time (enable NTP): TLS, sign-in sessions and schedules depend on it"
	} else {
		check.Status, check.Detail = StatusOK, now.Format("2006-01-02 15:04 MST")
	}
	r.add(check)
}

// HumanBytes renders a byte count: 1.5 GB, 320 MB.
func HumanBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := uint64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(value)/float64(div), "KMGTPE"[exp])
}
