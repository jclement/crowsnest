// Package tailscale reads this machine's Tailscale state through the `tailscale` CLI, read-only:
// `tailscale status --json` (the MagicDNS name, whether HTTPS certificates are enabled) and
// `tailscale serve status --json` (whether a serve config already proxies to crowsnest's port).
//
// crowsnest uses it in two places (DESIGN.md 4.3, 3.5): at start, to add the machine's MagicDNS
// name to the host allowlist, so requests arriving through `tailscale serve` (whose Host is that
// name) are accepted without --allowed-host; and in doctor, to say what is set up and print the
// manual command that serves crowsnest over HTTPS on the tailnet. Nothing here changes
// Tailscale's configuration: the one state-changing call crowsnest makes, the foreground
// `tailscale serve` that publishes it when the owner asks (--tailscale-serve), lives in
// internal/tsserve behind an argv allowlist.
//
// Every call has a short timeout: an absent, slow or logged-out tailscale must never delay or
// fail a start.
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
)

// Timeout bounds one `tailscale` call.
const Timeout = 2 * time.Second

// Info is what `tailscale status --json` says about this machine.
type Info struct {
	Installed bool     `json:"installed"`
	Running   bool     `json:"running"`         // BackendState Running (logged in, connected)
	State     string   `json:"state"`           // BackendState: Running, NeedsLogin, Stopped, …
	DNSName   string   `json:"dns_name"`        // MagicDNS name, trailing dot removed: quark.tail1234.ts.net
	HTTPS     bool     `json:"https"`           // HTTPS certificates enabled for the tailnet (CertDomains)
	IPs       []string `json:"ips,omitempty"`   // this node's tailnet addresses
	Error     string   `json:"error,omitempty"` // why it could not be read
	// MagicDNS is CurrentTailnet.MagicDNSEnabled; MagicDNSKnown is false when the CLI did not
	// say (older versions), so a missing field is not read as "off".
	MagicDNS      bool `json:"magic_dns"`
	MagicDNSKnown bool `json:"-"`
}

// Runner runs the tailscale CLI and returns its stdout. Tests give a fake.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Binary is the CLI's name on PATH.
const Binary = "tailscale"

// MacAppBinary is where the macOS app (App Store or standalone download) keeps the CLI. It is
// not on PATH unless the person linked it, but run by that path it acts as the CLI
// (tailscale.com/kb/1080/cli, "Using the CLI on macOS"). A variable so tests can turn the
// fallback off: on a developer's Mac the real app is there.
var MacAppBinary = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

// FindBinary returns the tailscale CLI's path: `tailscale` on PATH, then (on macOS) the app
// bundle's binary. ErrNotInstalled when neither exists.
func FindBinary() (string, error) {
	return findBinary(runtime.GOOS, exec.LookPath, os.Stat)
}

func findBinary(goos string, lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) (string, error) {
	if path, err := lookPath(Binary); err == nil {
		return path, nil
	}
	if goos == "darwin" && MacAppBinary != "" {
		if info, err := stat(MacAppBinary); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return MacAppBinary, nil
		}
	}
	return "", ErrNotInstalled
}

// CLI runs the real tailscale CLI (FindBinary) with Timeout and a scrubbed environment.
// ErrNotInstalled when it is not there.
func CLI(ctx context.Context, args ...string) ([]byte, error) {
	path, err := FindBinary()
	if err != nil {
		return nil, ErrNotInstalled
	}
	return RunBinary(ctx, path, args...)
}

// RunBinary runs the tailscale CLI at path with args, Timeout and a scrubbed environment (no
// HERDR_*, CROWSNEST_* secrets or ELEVENLABS_*), and returns its stdout.
func RunBinary(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // fixed read-only argv chosen by the callers
	cmd.Env = herdr.ScrubEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A child the CLI started can hold the output pipe after the CLI is killed at the timeout;
	// WaitDelay stops waiting for it rather than for the child.
	cmd.WaitDelay = 200 * time.Millisecond
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("tailscale %s did not answer within %s", strings.Join(args, " "), Timeout)
	}
	if err != nil && stdout.Len() == 0 {
		return nil, fmt.Errorf("tailscale %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// ErrNotInstalled: no tailscale on PATH.
var ErrNotInstalled = errors.New("tailscale is not installed")

// statusJSON is the part of `tailscale status --json` read here.
type statusJSON struct {
	BackendState string `json:"BackendState"`
	Self         *struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	CertDomains    []string `json:"CertDomains"`
	CurrentTailnet *struct {
		MagicDNSEnabled *bool `json:"MagicDNSEnabled"`
	} `json:"CurrentTailnet"`
}

// Status reads `tailscale status --json`. It never fails: what went wrong is in Info.Error.
func Status(ctx context.Context, run Runner) Info {
	output, err := run(ctx, "status", "--json")
	if errors.Is(err, ErrNotInstalled) {
		return Info{}
	}
	info := Info{Installed: true}
	if err != nil {
		info.Error = err.Error()
		return info
	}
	var status statusJSON
	if err := json.Unmarshal(output, &status); err != nil {
		info.Error = "could not read `tailscale status --json`: " + err.Error()
		return info
	}
	info.State = status.BackendState
	info.Running = status.BackendState == "Running"
	if status.Self != nil && info.Running {
		info.DNSName = strings.ToLower(strings.TrimSuffix(status.Self.DNSName, "."))
		info.IPs = status.Self.TailscaleIPs
	}
	info.HTTPS = len(status.CertDomains) > 0
	if status.CurrentTailnet != nil && status.CurrentTailnet.MagicDNSEnabled != nil {
		info.MagicDNS, info.MagicDNSKnown = *status.CurrentTailnet.MagicDNSEnabled, true
	}
	return info
}

// ServeConfig is the part of `tailscale serve status --json` (ipn.ServeConfig) read here: TCP
// listeners by port, web handlers by host:port and path, and the same again for each foreground
// `tailscale serve` session (Foreground, keyed by an opaque session id). tailscaled removes a
// foreground session's entry when its CLI process goes away, so those are live by construction.
type ServeConfig struct {
	TCP         map[string]TCPHandler  `json:"TCP"`
	Web         map[string]WebConfig   `json:"Web"`
	Foreground  map[string]ServeConfig `json:"Foreground"`
	Services    map[string]any         `json:"Services"`
	AllowFunnel map[string]bool        `json:"AllowFunnel"`
}

// TCPHandler is one TCP listener of a serve config.
type TCPHandler struct {
	HTTPS      bool   `json:"HTTPS"`
	HTTP       bool   `json:"HTTP"`
	TCPForward string `json:"TCPForward"`
}

// WebConfig is the handlers of one host:port.
type WebConfig struct {
	Handlers map[string]WebHandler `json:"Handlers"`
}

// WebHandler is one path's handler: a proxy target, a directory or a text.
type WebHandler struct {
	Proxy string `json:"Proxy"`
	Path  string `json:"Path"`
	Text  string `json:"Text"`
}

// Handler is one web handler of a serve config, flattened: where it listens and what it does.
type Handler struct {
	HostPort   string // quark.tail1234.ts.net:443
	Path       string // "/"
	Proxy      string // http://127.0.0.1:2769
	Foreground bool   // from a foreground `tailscale serve` session
}

// ReadServeConfig runs `tailscale serve status --json` and parses it. No config at all is an
// empty ServeConfig.
func ReadServeConfig(ctx context.Context, run Runner) (ServeConfig, error) {
	output, err := run(ctx, "serve", "status", "--json")
	if err != nil {
		return ServeConfig{}, err
	}
	return ParseServeConfig(output)
}

// ParseServeConfig parses the output of `tailscale serve status --json`.
func ParseServeConfig(output []byte) (ServeConfig, error) {
	var config ServeConfig
	if len(bytes.TrimSpace(output)) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(output, &config); err != nil {
		return ServeConfig{}, fmt.Errorf("could not read `tailscale serve status --json`: %w", err)
	}
	return config, nil
}

// Handlers lists every web handler, background and foreground, sorted for stable output.
func (c ServeConfig) Handlers() []Handler {
	var out []Handler
	c.collect(&out, false)
	slices.SortFunc(out, func(a, b Handler) int {
		return strings.Compare(a.HostPort+a.Path+a.Proxy, b.HostPort+b.Path+b.Proxy)
	})
	return out
}

func (c ServeConfig) collect(out *[]Handler, foreground bool) {
	for hostPort, web := range c.Web {
		for path, handler := range web.Handlers {
			*out = append(*out, Handler{HostPort: hostPort, Path: path, Proxy: handler.Proxy, Foreground: foreground})
		}
	}
	for _, session := range c.Foreground {
		session.collect(out, true)
	}
}

// PortsInUse is every TCP port this node's serve config (background and foreground) listens
// on: the TCP map's keys and the ports of the web handlers' host:port keys.
func (c ServeConfig) PortsInUse() map[int]bool {
	used := map[int]bool{}
	c.ports(used)
	return used
}

func (c ServeConfig) ports(used map[int]bool) {
	for port := range c.TCP {
		if number, err := strconv.Atoi(port); err == nil {
			used[number] = true
		}
	}
	for hostPort := range c.Web {
		if _, port, err := net.SplitHostPort(hostPort); err == nil {
			if number, err := strconv.Atoi(port); err == nil {
				used[number] = true
			}
		}
	}
	for _, session := range c.Foreground {
		session.ports(used)
	}
}

// Serve is a serve config that proxies to a local port.
type Serve struct {
	URL        string `json:"url"`    // https://quark.tail1234.ts.net (plus a path or port when not /, 443)
	Target     string `json:"target"` // http://127.0.0.1:2769
	Foreground bool   `json:"foreground,omitempty"`
}

// ServesPort reads `tailscale serve status --json` and returns the served URLs whose proxy
// target is a local address on port, foreground sessions included. Nothing found, or no serve
// config at all, is an empty list.
func ServesPort(ctx context.Context, run Runner, port int) ([]Serve, error) {
	config, err := ReadServeConfig(ctx, run)
	if err != nil {
		return nil, err
	}
	return config.ServesPort(port), nil
}

// ServesPort is the served URLs whose proxy target is a local address on port.
func (c ServeConfig) ServesPort(port int) []Serve {
	var out []Serve
	suffix := ":" + strconv.Itoa(port)
	for _, handler := range c.Handlers() {
		target := strings.TrimRight(handler.Proxy, "/")
		if !strings.HasSuffix(target, suffix) && !strings.Contains(target, suffix+"/") {
			continue
		}
		url := "https://" + strings.TrimSuffix(handler.HostPort, ":443")
		if handler.Path != "/" {
			url += handler.Path
		}
		out = append(out, Serve{URL: url, Target: handler.Proxy, Foreground: handler.Foreground})
	}
	return out
}

// ServeCommand is the command that serves crowsnest on port over HTTPS on the tailnet
// (`tailscale serve --help`, 1.104: `tailscale serve --bg 3000` proxies to 127.0.0.1:3000).
func ServeCommand(port int) string { return "tailscale serve --bg " + strconv.Itoa(port) }
