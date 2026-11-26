// The session manager: find a named herdr session's socket, and start the session's server when
// nothing answers there. Crowsnest only ever starts servers; it never stops one (DESIGN.md
// section 5), because herdr and its agents must outlive crowsnest.
//
// Every herdr child process gets an environment with HERDR_* removed (DESIGN.md 4.10): crowsnest
// started from inside a herdr pane would otherwise inherit HERDR_SOCKET_PATH and HERDR_SESSION
// and could end up driving the very session it is sitting in.

package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	// DefaultStartTimeout is how long a freshly started server gets to answer ping.
	DefaultStartTimeout = 10 * time.Second
	// startPollInterval is how often a starting server is probed.
	startPollInterval = 100 * time.Millisecond
	// ServerLogName is the file under the log directory that receives the server's output.
	ServerLogName = "herdr-server.log"
	// DefaultSessionName is herdr's own session, Jeff's main one. Crowsnest refuses it unless
	// explicitly allowed, so it is never pointed there by accident.
	DefaultSessionName = "default"
)

// sessionNamePattern is herdr's session directory naming; anything else could escape it.
var sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// SessionOptions configure EnsureSession.
type SessionOptions struct {
	Name         string        // session name; required
	AllowDefault bool          // permit the "default" session
	Binary       string        // herdr executable; "herdr" (from PATH) when empty
	LogDir       string        // directory for the server log; required to start a server
	StartTimeout time.Duration // DefaultStartTimeout when zero
	Environ      []string      // base environment for children; os.Environ() when nil
	Logger       *slog.Logger  // slog.Default() when nil
}

// Session is a connected herdr session.
type Session struct {
	Name       string
	SocketPath string
	Client     *SocketClient
	Pong       Pong
	Started    bool // true when this call started the server
}

// paneEnvNames are the CROWSNEST_* variables ScrubEnv keeps: the ones crowsnest deliberately
// puts in an agent's pane (DESIGN.md 7 step 2, and the agent socket and job id M6/M7 add). None
// of them is a secret.
var paneEnvNames = map[string]bool{"CROWSNEST": true, "CROWSNEST_SESSION": true, "CROWSNEST_SOCKET": true, "CROWSNEST_JOB": true}

// ScrubEnv returns env without the variables no child of crowsnest may inherit:
//
//   - HERDR_*: a crowsnest started inside a herdr pane must not be able to drive that pane's
//     session (DESIGN.md 4.10);
//   - CROWSNEST_*, except the pane variables above: they carry the login password and the auth
//     switches;
//   - ELEVENLABS_*: the talk-mode API key.
//
// Every child gets this: the herdr server (which spawns every pane and agent, so they inherit
// it), the TUI client and each pane-control process (R1 H1).
func ScrubEnv(env []string) []string {
	scrubbed := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		switch {
		case strings.HasPrefix(name, "HERDR_"), strings.HasPrefix(name, "ELEVENLABS_"):
			continue
		case strings.HasPrefix(name, "CROWSNEST_") && !paneEnvNames[name]:
			continue
		}
		scrubbed = append(scrubbed, entry)
	}
	return scrubbed
}

// ValidateSessionName applies crowsnest's rules for a session name: herdr's naming, and
// "default" only when allowed.
func ValidateSessionName(name string, allowDefault bool) error {
	if !sessionNamePattern.MatchString(name) {
		return fmt.Errorf("herdr session name %q is not valid; use letters, digits, '.', '_' or '-'", name)
	}
	if name == DefaultSessionName && !allowDefault {
		return errors.New("refusing to use herdr's default session; pass --allow-default-session if you really mean it")
	}
	return nil
}

// EnsureSession connects to the named session, starting its server first if nothing answers.
func EnsureSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	opts = opts.withDefaults()
	if err := ValidateSessionName(opts.Name, opts.AllowDefault); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath(opts.Binary); err != nil {
		return nil, fmt.Errorf("herdr is not installed (no %q on PATH); install it, or run with --demo", opts.Binary)
	}
	if session, err := connect(ctx, opts); err == nil {
		return session, nil
	}
	opts.Logger.Info("herdr session is not running; starting it", "session", opts.Name)
	exited, err := startServer(opts)
	if err != nil {
		return nil, err
	}
	session, err := waitForServer(ctx, opts, exited)
	if err != nil {
		return nil, err
	}
	session.Started = true
	return session, nil
}

func (o SessionOptions) withDefaults() SessionOptions {
	if o.Binary == "" {
		o.Binary = "herdr"
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = DefaultStartTimeout
	}
	if o.Environ == nil {
		o.Environ = os.Environ()
	}
	o.Environ = ScrubEnv(o.Environ)
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}

// connect resolves the socket and pings it once.
func connect(ctx context.Context, opts SessionOptions) (*Session, error) {
	path, err := ResolveSocketPath(ctx, opts)
	if err != nil {
		return nil, err
	}
	client := NewSocketClient(path)
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	pong, err := client.Ping(pingCtx)
	if err != nil {
		return nil, err
	}
	return &Session{Name: opts.Name, SocketPath: path, Client: client, Pong: pong}, nil
}

// sessionList is `herdr session list --json`.
type sessionList struct {
	Sessions []struct {
		Name       string `json:"name"`
		Running    bool   `json:"running"`
		SocketPath string `json:"socket_path"`
	} `json:"sessions"`
}

// ResolveSocketPath asks herdr where the named session's socket is. A session herdr does not
// list yet (never started) gets the conventional path under ~/.config/herdr/sessions; the
// definitive path is re-read once the server is up.
func ResolveSocketPath(ctx context.Context, opts SessionOptions) (string, error) {
	opts = opts.withDefaults()
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(listCtx, opts.Binary, "session", "list", "--json") //nolint:gosec // fixed argv; binary is configuration
	cmd.Env = opts.Environ
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("listing herdr sessions: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	var list sessionList
	if err := json.Unmarshal(output, &list); err != nil {
		return "", fmt.Errorf("reading `herdr session list --json`: %w", err)
	}
	for _, session := range list.Sessions {
		if session.Name == opts.Name && session.SocketPath != "" {
			return session.SocketPath, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding the home directory for the herdr socket: %w", err)
	}
	return filepath.Join(home, ".config", "herdr", "sessions", opts.Name, "herdr.sock"), nil
}

// startServer runs `herdr --session <name> server` detached: its own session and process group
// (so a Ctrl+C aimed at crowsnest never reaches it), stdin from /dev/null, output appended to the
// log file, HERDR_* scrubbed. The returned channel closes if the process exits.
func startServer(opts SessionOptions) (<-chan error, error) {
	if opts.LogDir == "" {
		return nil, errors.New("starting herdr: no log directory configured")
	}
	if err := os.MkdirAll(opts.LogDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating herdr log directory %s: %w", opts.LogDir, err)
	}
	logPath := filepath.Join(opts.LogDir, ServerLogName)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // path is built from configuration
	if err != nil {
		return nil, fmt.Errorf("opening herdr log %s: %w", logPath, err)
	}
	defer func() { _ = logFile.Close() }() // the child holds its own descriptor

	cmd := exec.Command(opts.Binary, "--session", opts.Name, "server") //nolint:gosec // fixed argv; name is validated
	cmd.Env = opts.Environ
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting `herdr --session %s server`: %w", opts.Name, err)
	}
	opts.Logger.Info("started herdr server", "session", opts.Name, "pid", cmd.Process.Pid, "log", logPath)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait(); close(exited) }() // reap it if it ever exits while we run
	return exited, nil
}

// waitForServer polls until the new server answers ping, it exits, or the timeout passes.
func waitForServer(ctx context.Context, opts SessionOptions, exited <-chan error) (*Session, error) {
	deadline := time.NewTimer(opts.StartTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(startPollInterval)
	defer ticker.Stop()
	logPath := filepath.Join(opts.LogDir, ServerLogName)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-exited:
			if err == nil {
				exited = nil // exited cleanly: it may have daemonised, so keep probing the socket
				continue
			}
			return nil, fmt.Errorf("herdr server for session %q exited during start-up; see %s: %w", opts.Name, logPath, err)
		case <-deadline.C:
			return nil, fmt.Errorf("herdr server for session %q did not answer within %s; see %s", opts.Name, opts.StartTimeout, logPath)
		case <-ticker.C:
			if session, err := connect(ctx, opts); err == nil {
				return session, nil
			}
		}
	}
}
