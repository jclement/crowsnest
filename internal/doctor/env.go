// Env is everything the checks touch outside the process. The zero value means the real
// machine; tests replace single functions.

package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/tailscale"
)

// commandTimeout bounds every `--version`, `auth status` or `--help` call: a hung agent CLI
// must not hang doctor (and the System page's re-check).
const commandTimeout = 8 * time.Second

// Env is the outside world, injected.
type Env struct {
	GOOS     string
	Home     string
	LookPath func(name string) (string, error)
	// Run runs a command with a scrubbed environment (no HERDR_*, CROWSNEST_* secrets or
	// ELEVENLABS_*) and returns its combined output.
	Run     func(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error)
	Getenv  func(name string) (string, bool)
	Environ func() []string
	Now     func() time.Time
	// Listen tries to bind addr ("127.0.0.1:2769") and closes it at once.
	Listen func(addr string) error
	// HTTPGet fetches url; status, body.
	HTTPGet func(ctx context.Context, url string, header map[string]string) (int, []byte, error)
	// HerdrSocket finds a session's socket path; HerdrPing pings it.
	HerdrSocket func(ctx context.Context, session string) (string, error)
	HerdrPing   func(ctx context.Context, socket string) (herdr.Pong, error)
	// DiskFree is the free bytes on the volume holding path (or its nearest existing parent).
	DiskFree func(path string) (uint64, error)
	// Writable reports whether this user may create files in dir (dir exists).
	Writable    func(dir string) bool
	Readable    func(path string) bool
	Stat        func(path string) (os.FileInfo, error)
	ReadFile    func(path string) ([]byte, error)
	InContainer func() bool
	// Tailscale reads `tailscale status --json`; TailscaleServes reads `tailscale serve status`
	// for proxies to a port. Both read-only.
	Tailscale       func(ctx context.Context) tailscale.Info
	TailscaleServes func(ctx context.Context, port int) ([]tailscale.Serve, error)
}

func (e Env) withDefaults() Env {
	if e.GOOS == "" {
		e.GOOS = runtime.GOOS
	}
	if e.Home == "" {
		e.Home, _ = os.UserHomeDir()
	}
	if e.LookPath == nil {
		e.LookPath = exec.LookPath
	}
	if e.Run == nil {
		e.Run = runCommand
	}
	if e.Getenv == nil {
		e.Getenv = os.LookupEnv
	}
	if e.Environ == nil {
		e.Environ = os.Environ
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Listen == nil {
		e.Listen = tryListen
	}
	if e.HTTPGet == nil {
		e.HTTPGet = httpGet
	}
	if e.HerdrSocket == nil {
		e.HerdrSocket = func(ctx context.Context, session string) (string, error) {
			return herdr.ResolveSocketPath(ctx, herdr.SessionOptions{Name: session})
		}
	}
	if e.HerdrPing == nil {
		e.HerdrPing = func(ctx context.Context, socket string) (herdr.Pong, error) {
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			return herdr.NewSocketClient(socket).Ping(pingCtx)
		}
	}
	if e.DiskFree == nil {
		e.DiskFree = diskFree
	}
	if e.Writable == nil {
		e.Writable = func(dir string) bool { return unix.Access(dir, unix.W_OK|unix.X_OK) == nil }
	}
	if e.Readable == nil {
		e.Readable = func(path string) bool { return unix.Access(path, unix.R_OK) == nil }
	}
	if e.Stat == nil {
		e.Stat = os.Stat
	}
	if e.ReadFile == nil {
		e.ReadFile = os.ReadFile
	}
	if e.InContainer == nil {
		e.InContainer = InContainer
	}
	if e.Tailscale == nil {
		e.Tailscale = func(ctx context.Context) tailscale.Info { return tailscale.Status(ctx, tailscale.CLI) }
	}
	if e.TailscaleServes == nil {
		e.TailscaleServes = func(ctx context.Context, port int) ([]tailscale.Serve, error) {
			return tailscale.ServesPort(ctx, tailscale.CLI, port)
		}
	}
	return e
}

// runCommand is Env.Run on the real machine.
func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed argv chosen by the checks
	cmd.Env = herdr.ScrubEnv(os.Environ())
	cmd.Stdin = nil
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = time.Second // a grandchild holding the pipe must not outlive the timeout
	err := cmd.Run()
	if ctx.Err() != nil {
		return output.String(), fmt.Errorf("%s did not answer within %s", name, timeout)
	}
	return output.String(), err
}

func tryListen(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return listener.Close()
}

func httpGet(ctx context.Context, url string, header map[string]string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	for key, value := range header {
		req.Header.Set(key, value)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, body, err
}

// diskFree reports free bytes for the nearest existing directory at or above path.
func diskFree(path string) (uint64, error) {
	for {
		var stat unix.Statfs_t
		err := unix.Statfs(path, &stat)
		if err == nil {
			return uint64(stat.Bavail) * uint64(stat.Bsize), nil //nolint:gosec,unconvert // sizes are non-negative; types differ by OS
		}
		if !errors.Is(err, unix.ENOENT) {
			return 0, err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, err
		}
		path = parent
	}
}

// InContainer guesses whether this process runs in a container: Docker's marker file, or a
// cgroup path naming a container runtime. Used to decide against opening a browser, and to say
// that system figures are the container's.
func InContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	text := string(data)
	return strings.Contains(text, "docker") || strings.Contains(text, "kubepods") || strings.Contains(text, "containerd") || strings.Contains(text, "libpod")
}
