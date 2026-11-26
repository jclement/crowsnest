// Package tailscale reads this machine's Tailscale state through the `tailscale` CLI, read-only:
// `tailscale status --json` (the MagicDNS name, whether HTTPS certificates are enabled) and
// `tailscale serve status --json` (whether a serve config already proxies to crowsnest's port).
//
// crowsnest uses it in two places (DESIGN.md 4.3, 3.5): at start, to add the machine's MagicDNS
// name to the host allowlist, so requests arriving through `tailscale serve` (whose Host is that
// name) are accepted without --allowed-host; and in doctor, to say what is set up and print the
// one command that serves crowsnest over HTTPS on the tailnet. It never changes Tailscale's
// configuration: crowsnest only ever prints `tailscale serve --bg <port>` for the person to run.
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
	"os/exec"
	"strconv"
	"strings"
	"time"
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
}

// Runner runs the tailscale CLI and returns its stdout. Tests give a fake.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Binary is the CLI's name on PATH.
const Binary = "tailscale"

// CLI runs the real `tailscale` from PATH with Timeout. ErrNotInstalled when it is not there.
func CLI(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath(Binary)
	if err != nil {
		return nil, ErrNotInstalled
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // fixed read-only argv
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A child the CLI started can hold the output pipe after the CLI is killed at the timeout;
	// WaitDelay stops waiting for it rather than for the child.
	cmd.WaitDelay = 200 * time.Millisecond
	err = cmd.Run()
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
	CertDomains []string `json:"CertDomains"`
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
	return info
}

// serveJSON is the part of `tailscale serve status --json` read here: web handlers by
// host:port and path, each proxying somewhere.
type serveJSON struct {
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

// Serve is a serve config that proxies to a local port.
type Serve struct {
	URL    string `json:"url"`    // https://quark.tail1234.ts.net (plus a path or port when not /, 443)
	Target string `json:"target"` // http://127.0.0.1:2769
}

// ServesPort reads `tailscale serve status --json` and returns the served URLs whose proxy
// target is a local address on port. Nothing found, or no serve config at all, is an empty list.
func ServesPort(ctx context.Context, run Runner, port int) ([]Serve, error) {
	output, err := run(ctx, "serve", "status", "--json")
	if err != nil {
		return nil, err
	}
	var status serveJSON
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, fmt.Errorf("could not read `tailscale serve status --json`: %w", err)
	}
	var out []Serve
	suffix := ":" + strconv.Itoa(port)
	for hostPort, web := range status.Web {
		for path, handler := range web.Handlers {
			target := strings.TrimRight(handler.Proxy, "/")
			if !strings.HasSuffix(target, suffix) && !strings.Contains(target, suffix+"/") {
				continue
			}
			url := "https://" + strings.TrimSuffix(hostPort, ":443")
			if path != "/" {
				url += path
			}
			out = append(out, Serve{URL: url, Target: handler.Proxy})
		}
	}
	return out, nil
}

// ServeCommand is the command that serves crowsnest on port over HTTPS on the tailnet
// (`tailscale serve --help`, 1.104: `tailscale serve --bg 3000` proxies to 127.0.0.1:3000).
func ServeCommand(port int) string { return "tailscale serve --bg " + strconv.Itoa(port) }
