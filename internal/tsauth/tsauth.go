// Package tsauth is "sign in with Tailscale" (DESIGN.md 4.20): which tailnet logins the owner
// allowed, and whether this machine's `tailscale serve` really proxies to crowsnest's port.
//
// `tailscale serve` adds Tailscale-User-Login (and -Name, -Profile-Pic) to every request it
// proxies from a tailnet user, and removes any copy a client sent, so behind it those headers
// are the tailnet's word for who is asking (tailscale.com/kb/1312/serve; not set for tagged
// devices or Funnel). They are worth nothing from anywhere else: anyone who reaches the port
// directly can type them. So the HTTP layer believes them only when all of these hold: the
// option is on (--tailscale-auth), the TCP peer is loopback (serve runs on this machine), a
// serve config on this machine proxies to crowsnest's port (checked here, cached), and the
// request's Host is this machine's MagicDNS name. The login must then be on the owner's list.
//
// The list is kept in kv `tailscale.allow`. It starts empty: the owner adds their own login from
// the Settings sheet while signed in with the password through serve (the request then carries
// it), and may add others. Password sign-in always remains.
package tsauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/tailscale"
)

const (
	// allowKey holds the allowed logins as a JSON list.
	allowKey = "tailscale.allow"
	// serveCacheFor is how long a serve-config check is believed; a config removed is noticed
	// within this time.
	serveCacheFor = 30 * time.Second
	// maxLogins bounds the list; maxLoginBytes one login.
	maxLogins     = 50
	maxLoginBytes = 254
)

// KV is the part of the store the list needs.
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
}

// Options configure a Gate.
type Options struct {
	Enabled bool // --tailscale-auth
	KV      KV
	Run     tailscale.Runner // nil: the real CLI
	Port    int
	Now     func() time.Time
}

// Gate answers the two questions the HTTP layer asks.
type Gate struct {
	opts Options

	mu        sync.Mutex
	checkedAt time.Time
	hosts     []string // the host names serve proxies to our port under
}

// New returns a Gate.
func New(opts Options) *Gate {
	if opts.Run == nil {
		opts.Run = tailscale.CLI
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Gate{opts: opts}
}

// Enabled reports whether --tailscale-auth is on. A nil Gate is off.
func (g *Gate) Enabled() bool { return g != nil && g.opts.Enabled }

// ServesHost reports whether a serve config on this machine proxies host (a request's Host,
// with or without a port) to crowsnest's port, asking `tailscale serve status --json` at most
// every 30 seconds. Any failure is "no".
func (g *Gate) ServesHost(ctx context.Context, host string) bool {
	if !g.Enabled() {
		return false
	}
	host = strings.ToLower(hostOnly(host))
	return host != "" && slices.Contains(g.servedHosts(ctx), host)
}

// ServeConfigured reports whether any serve config proxies to crowsnest's port.
func (g *Gate) ServeConfigured(ctx context.Context) bool {
	return g.Enabled() && len(g.servedHosts(ctx)) > 0
}

func (g *Gate) servedHosts(ctx context.Context) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.opts.Now()
	if !g.checkedAt.IsZero() && now.Sub(g.checkedAt) < serveCacheFor {
		return g.hosts
	}
	g.hosts, g.checkedAt = nil, now
	serves, err := tailscale.ServesPort(ctx, g.opts.Run, g.opts.Port)
	if err != nil {
		return nil
	}
	for _, serve := range serves {
		rest := strings.TrimPrefix(serve.URL, "https://")
		rest, _, _ = strings.Cut(rest, "/")
		g.hosts = append(g.hosts, strings.ToLower(hostOnly(rest)))
	}
	return g.hosts
}

// hostOnly drops a port from host:port.
func hostOnly(hostPort string) string {
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return host
	}
	return hostPort
}

// NormalizeLogin trims and lower-cases a login; "" when it is not one (too long, spaces,
// control characters).
func NormalizeLogin(login string) string {
	login = strings.ToLower(strings.TrimSpace(login))
	if login == "" || len(login) > maxLoginBytes {
		return ""
	}
	for _, r := range login {
		if r <= ' ' || r == 0x7f {
			return ""
		}
	}
	return login
}

// Allowed reports whether login is on the owner's list.
func (g *Gate) Allowed(ctx context.Context, login string) bool {
	login = NormalizeLogin(login)
	if login == "" || !g.Enabled() {
		return false
	}
	list, err := g.List(ctx)
	return err == nil && slices.Contains(list, login)
}

// List is the allowed logins.
func (g *Gate) List(ctx context.Context) ([]string, error) {
	raw, err := g.opts.KV.GetKV(ctx, allowKey)
	if errors.Is(err, store.ErrNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the Tailscale allowlist: %w", err)
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("reading the Tailscale allowlist: %w", err)
	}
	return list, nil
}

// ErrInvalidLogin is returned for a login that is not one.
var ErrInvalidLogin = errors.New("a Tailscale login is one word such as you@github or you@example.com")

// SetList replaces the list (normalized, deduplicated, at most 50).
func (g *Gate) SetList(ctx context.Context, logins []string) ([]string, error) {
	out := []string{}
	for _, login := range logins {
		normal := NormalizeLogin(login)
		if normal == "" {
			return nil, fmt.Errorf("%w: %q", ErrInvalidLogin, login)
		}
		if !slices.Contains(out, normal) {
			out = append(out, normal)
		}
	}
	if len(out) > maxLogins {
		return nil, fmt.Errorf("at most %d logins", maxLogins)
	}
	body, _ := json.Marshal(out)
	if err := g.opts.KV.SetKV(ctx, allowKey, string(body)); err != nil {
		return nil, fmt.Errorf("saving the Tailscale allowlist: %w", err)
	}
	return out, nil
}
