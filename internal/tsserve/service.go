// Package tsserve publishes crowsnest to the owner's tailnet with `tailscale serve`, when they
// ask (DESIGN.md 3.5.1): the System page's "Publish to your tailnet" card, the first-run
// checklist's step, or --tailscale-serve. It is the only code in crowsnest that changes
// Tailscale's state, and it does so in the narrowest way there is:
//
//   - Foreground only. `tailscale serve --https=<port> http://127.0.0.1:<port>` runs as a
//     supervised child (through the watchdog in child.go), never with --bg, so the published
//     address lives exactly as long as crowsnest: a stop, a crash or a SIGKILL all end it, and no
//     persistent serve config is ever written.
//   - Never clobber. It reads `tailscale serve status --json` first and never touches a port
//     another config uses: 443, else 8443, else 10000 (or the one --tailscale-serve-port names),
//     else it refuses and says why. A config someone else made that already proxies to crowsnest
//     is left alone and reported, not duplicated.
//   - Never Funnel, never reset: every command line passes argv.go's allowlist.
//   - Only with a login. --no-auth is refused (a loopback-only bind with no login would become a
//     shell for the whole tailnet), and nothing runs before first-run setup has an owner.
//
// The Service is a small state machine driven by Run: off (the offer), starting, publishing,
// failed (with the reason and the next retry), stopped (by the human), refused (cannot, with the
// fix) and external (someone else's config already publishes crowsnest). View answers the
// System page; the httpd routes turn the setting on and off.
package tsserve

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/tailscale"
)

// State is where publishing stands.
type State string

// States, as the System page shows them.
const (
	StateUnavailable State = "unavailable" // Tailscale absent or logged out and publishing off: no offer
	StateOff         State = "off"         // the offer: what it would publish, not publishing
	StateRefused     State = "refused"     // cannot publish: Reason and Fix say why
	StateStarting    State = "starting"    // tailscale serve started, not yet confirmed
	StatePublishing  State = "publishing"  // confirmed in `tailscale serve status`
	StateFailed      State = "failed"      // it stopped by itself; retried at RetryAt
	StateStopped     State = "stopped"     // the human stopped it this run
	StateExternal    State = "external"    // a serve config crowsnest did not make already proxies here
	stateReady       State = "ready"       // internal: the plan says go
)

// DefaultPorts are the HTTPS ports tried in order when --tailscale-serve-port is not given: the
// default HTTPS port, then the two other ports Tailscale suggests for serve and funnel.
var DefaultPorts = []int{443, 8443, 10000}

// Timing defaults.
const (
	defaultBackoffMin  = 2 * time.Second
	defaultBackoffMax  = time.Minute
	defaultConfirmPoll = time.Second
	// defaultHealthPoll is how often a confirmed session is looked for again (R9-C13): tailscaled
	// can drop it (a restart, a re-login) while the CLI lives on, and the card must not keep
	// saying "publishing" over a dead URL.
	defaultHealthPoll = 15 * time.Second
	defaultStopGrace   = 6 * time.Second // the watchdog's own interrupt-terminate-kill takes 4 s
	// healthyAfter: a child that ran this long resets the backoff, so a flake an hour in is
	// retried quickly rather than after a minute.
	healthyAfter = time.Minute
	// recheckRefused is how often a refused or external plan is looked at again while
	// publishing is on, so fixing the admin console switch is noticed without a click.
	recheckRefused = 30 * time.Second
	// offerCacheFor bounds how often the offer probes Tailscale (three CLI calls) for the page.
	offerCacheFor = 5 * time.Second
	// outputTail is how much of tailscale serve's output is kept for classification and the log.
	outputTail = 8 << 10
)

// View is GET /tailscale/serve: the card's whole state.
type View struct {
	Show     bool       `json:"show"` // offer or state worth showing: Tailscale ready, or publishing is on
	State    State      `json:"state"`
	Reason   string     `json:"reason,omitempty"`
	Fix      string     `json:"fix,omitempty"`
	FixURL   string     `json:"fix_url,omitempty"`
	URL      string     `json:"url,omitempty"`  // what it publishes (or would): https://name[:port]
	Port     int        `json:"port,omitempty"` // the HTTPS port on the tailnet
	PortNote string     `json:"port_note,omitempty"`
	Target   string     `json:"target,omitempty"` // http://127.0.0.1:2769
	Name     string     `json:"name,omitempty"`   // the machine's MagicDNS name
	Version  string     `json:"version,omitempty"`
	Binary   string     `json:"binary,omitempty"`
	Enabled  bool       `json:"enabled"` // the setting is on
	Since    *time.Time `json:"since,omitempty"`
	RetryAt  *time.Time `json:"retry_at,omitempty"`
	Note     string     `json:"note,omitempty"` // a leftover cleaned, or left alone
	// Locked, QR and LocalRisk are filled in by the HTTP layer (the setting's source; a QR of URL;
	// the sentence for publishing together with --tailscale-auth, R9-S3).
	Locked    string `json:"locked,omitempty"`
	QR        string `json:"qr,omitempty"`
	LocalRisk string `json:"local_risk,omitempty"`
}

// KV is the part of the store the service keeps its child record in.
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
}

// Options configure a Service.
type Options struct {
	// Binary is the tailscale CLI. Empty: FindReal decides. A test or --demo passes its fake.
	Binary string
	// FindReal looks the real CLI up (PATH, then the macOS app) when Binary is empty. Only a real
	// serve sets it; it is ignored under `go test`, so no test can reach the real tailscale.
	FindReal bool
	// Self, SelfArgs and SelfEnv start the watchdog: Self SelfArgs... <tailscale> serve …
	// (crowsnest: os.Executable, [tailscale-serve-child --]).
	Self     string
	SelfArgs []string
	SelfEnv  []string
	// Target is the loopback URL tailscale serve proxies to, or an error saying why there is
	// none (crowsnest listens on no loopback address).
	Target func() (string, error)
	// Port is --tailscale-serve-port: 0 tries DefaultPorts.
	Port int
	// Auth is "" when a login protects crowsnest, else the sentence saying why publishing is
	// refused (--no-auth).
	Auth func() string
	// AllowHost makes the Host check accept the MagicDNS name, or says why it will not
	// (--no-tailscale-detect). Nil: accepted.
	AllowHost func(name string) string
	KV        KV
	Logger    *slog.Logger
	// Journal records publish, stop and failure lines (alert for failures). Nil: not journalled.
	Journal func(ctx context.Context, alert bool, summary string)
	// OnPublished is told the URL each time publishing is confirmed (serve prints it).
	OnPublished func(url string)
	// ProcessCommand returns a process's command line, for recognising a leftover watchdog.
	ProcessCommand func(pid int) (string, error)
	// Timing; zero values take the defaults.
	BackoffMin, BackoffMax, ConfirmPoll, HealthPoll, StopGrace time.Duration
	Now                                            func() time.Time
}

// Service publishes crowsnest on the tailnet while its setting is on.
type Service struct {
	opts Options
	kick chan struct{}

	mu             sync.Mutex
	enabled        bool
	stoppedByHuman bool
	running        bool // Run owns the state; otherwise View computes the offer
	busy           bool // a child is alive (starting, publishing or being stopped)
	view           View
	offer          View
	offerAt        time.Time
}

// New returns a Service; enabled is the setting at start.
func New(opts Options, enabled bool) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ProcessCommand == nil {
		opts.ProcessCommand = processCommand
	}
	opts.BackoffMin = orDefault(opts.BackoffMin, defaultBackoffMin)
	opts.BackoffMax = orDefault(opts.BackoffMax, defaultBackoffMax)
	opts.ConfirmPoll = orDefault(opts.ConfirmPoll, defaultConfirmPoll)
	opts.HealthPoll = orDefault(opts.HealthPoll, defaultHealthPoll)
	opts.StopGrace = orDefault(opts.StopGrace, defaultStopGrace)
	if opts.Binary == "" && opts.FindReal && !testing.Testing() {
		if path, err := tailscale.FindBinary(); err == nil {
			opts.Binary = path
		}
	}
	return &Service{opts: opts, kick: make(chan struct{}, 1), enabled: enabled}
}

func orDefault(value, def time.Duration) time.Duration {
	if value <= 0 {
		return def
	}
	return value
}

// SetEnabled turns publishing on or off (the setting changed). Off stops the child at once.
func (s *Service) SetEnabled(on bool) {
	s.mu.Lock()
	s.stoppedByHuman = !on && s.enabled
	if on && !s.enabled && !s.busy {
		// Until Run has planned again, say what is about to happen rather than the stale "off".
		starting := s.offer
		starting.Show, starting.State, starting.Enabled = true, StateStarting, true
		starting.Reason, starting.Fix, starting.FixURL = "", "", ""
		s.view = starting
	}
	s.enabled = on
	s.offerAt = time.Time{}
	s.mu.Unlock()
	s.Kick()
}

// Enabled reports the setting as the service holds it.
func (s *Service) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// Kick makes Run look again now (a retry wait ends, a refused plan is re-read).
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// View is the card's state: Run's while publishing is on, else the offer, probed at most every
// few seconds.
func (s *Service) View(ctx context.Context) View {
	s.mu.Lock()
	// While a child is alive (even one being stopped) Run's view is the truth: a probe then
	// would see crowsnest's own foreground session and call it someone else's.
	if s.running && (s.enabled || s.busy) {
		view := s.view
		s.mu.Unlock()
		return view
	}
	if !s.offerAt.IsZero() && s.opts.Now().Sub(s.offerAt) < offerCacheFor {
		view := s.offer
		s.mu.Unlock()
		return view
	}
	s.mu.Unlock()
	view := s.offerView(s.plan(ctx))
	s.mu.Lock()
	s.offer, s.offerAt = view, s.opts.Now()
	s.mu.Unlock()
	return view
}

// offerView turns a plan into what the card shows while publishing is off.
func (s *Service) offerView(plan View) View {
	s.mu.Lock()
	stopped, enabled := s.stoppedByHuman, s.enabled
	note := s.view.Note
	s.mu.Unlock()
	plan.Enabled = enabled
	if plan.Note == "" {
		plan.Note = note
	}
	switch plan.State {
	case stateReady:
		plan.State = StateOff
		if stopped {
			plan.State = StateStopped
		}
	case StateUnavailable:
		plan.Show = false
	}
	return plan
}

// ---- The plan: read-only, before anything runs --------------------------------

// plan reads the machine and says what publishing would do, or why it cannot. Nothing in it
// changes anything.
func (s *Service) plan(ctx context.Context) View {
	view := View{Show: true, Binary: s.opts.Binary}
	if s.opts.Binary == "" {
		return refuse(view, StateUnavailable, problemNotInstalled)
	}
	run := s.runner()
	output, err := run(ctx, argsVersion...)
	if errors.Is(err, tailscale.ErrNotInstalled) || errors.Is(err, os.ErrNotExist) {
		return refuse(view, StateUnavailable, problemNotInstalled)
	}
	have, ok := parseVersion(string(output))
	if ok {
		view.Version = have.String()
	}
	if minimum, _ := parseVersion(MinVersion); err != nil || !ok || !have.atLeast(minimum) {
		return refuse(view, StateRefused, problemTooOld(view.Version))
	}
	info := tailscale.Status(ctx, run)
	if problem, bad := statusProblem(info); bad {
		return refuse(view, StateUnavailable, problem)
	}
	view.Name = info.DNSName
	if view.Name == "" || (info.MagicDNSKnown && !info.MagicDNS) {
		return refuse(view, StateRefused, problemMagicDNS)
	}
	if !info.HTTPS {
		return refuse(view, StateRefused, problemHTTPS)
	}
	return s.planServe(ctx, view, run)
}

// planServe is the plan's second half: crowsnest's own preconditions, then the port.
func (s *Service) planServe(ctx context.Context, view View, run tailscale.Runner) View {
	if s.opts.Auth != nil {
		if reason := s.opts.Auth(); reason != "" {
			return refuse(view, StateRefused, Problem{Reason: reason, Fix: "Start crowsnest with a login (the owner from first-run setup, or --username with CROWSNEST_PASSWORD) and without --no-auth."})
		}
	}
	target, err := s.target()
	if err != nil {
		return refuse(view, StateRefused, Problem{Reason: err.Error() + ".", Fix: "Add --bind localhost (the default) so tailscale serve has a loopback address to proxy to."})
	}
	view.Target = target
	if s.opts.AllowHost != nil {
		if reason := s.opts.AllowHost(view.Name); reason != "" {
			return refuse(view, StateRefused, Problem{Reason: reason, Fix: "Drop --no-tailscale-detect, or pass --allowed-host " + view.Name + "."})
		}
	}
	config, err := tailscale.ReadServeConfig(ctx, run)
	if err != nil {
		s.opts.Logger.Warn("reading tailscale serve status", "err", err)
		return refuse(view, StateRefused, Problem{Reason: "crowsnest could not read `tailscale serve status`, so it cannot tell which ports are free.", Fix: "Check that `tailscale serve status --json` works on this machine; crowsnest tries again by itself."})
	}
	if url, port, ok := servesTarget(config, target); ok {
		view.URL, view.Port = url, port
		return refuse(view, StateExternal, Problem{
			Reason: "A tailscale serve config that crowsnest did not start already publishes it at " + url + ". crowsnest leaves it alone.",
			Fix:    "To let crowsnest publish it instead, remove that config yourself: `tailscale serve --https=" + strconv.Itoa(port) + " off`.",
		})
	}
	port, note, problem := s.choosePort(config)
	if problem != nil {
		return refuse(view, StateRefused, *problem)
	}
	view.Port, view.PortNote, view.URL = port, note, publicURL(view.Name, port)
	view.State = stateReady
	return view
}

func refuse(view View, state State, problem Problem) View {
	view.State, view.Reason, view.Fix, view.FixURL = state, problem.Reason, problem.Fix, problem.FixURL
	return view
}

// statusProblem is what `tailscale status --json` says is wrong, if anything.
func statusProblem(info tailscale.Info) (Problem, bool) {
	switch {
	case !info.Installed:
		return problemNotInstalled, true
	case info.Error != "":
		return problemNotRunning, true
	case info.State == "NeedsLogin" || info.State == "NeedsMachineAuth":
		return problemLoggedOut, true
	case !info.Running:
		return problemStopped, true
	}
	return Problem{}, false
}

// choosePort picks the HTTPS port: the configured one if it is free, else the first free one
// of DefaultPorts. It never picks a port any serve config on this node (background or
// foreground) listens on.
func (s *Service) choosePort(config tailscale.ServeConfig) (int, string, *Problem) {
	used := config.PortsInUse()
	if s.opts.Port != 0 {
		if used[s.opts.Port] {
			return 0, "", &Problem{
				Reason: "Port " + strconv.Itoa(s.opts.Port) + " (--tailscale-serve-port) already serves something on this node, and crowsnest never touches another serve config.",
				Fix:    "Choose a free port with --tailscale-serve-port, or leave it unset so crowsnest picks one of 443, 8443 and 10000.",
			}
		}
		return s.opts.Port, "", nil
	}
	for _, port := range DefaultPorts {
		if used[port] {
			continue
		}
		note := ""
		if port != DefaultPorts[0] {
			taken := make([]string, 0, len(DefaultPorts))
			for _, earlier := range DefaultPorts {
				if earlier == port {
					break
				}
				taken = append(taken, strconv.Itoa(earlier))
			}
			note = "Port " + strings.Join(taken, " and ") + " already serves something else on this node, so crowsnest uses " + strconv.Itoa(port) + "."
		}
		return port, note, nil
	}
	return 0, "", &Problem{
		Reason: "Ports 443, 8443 and 10000 all serve something else on this node, and crowsnest never touches another serve config.",
		Fix:    "Free one of them, or choose another with --tailscale-serve-port.",
	}
}

// servesTarget finds a handler (background or foreground) that proxies exactly to target.
func servesTarget(config tailscale.ServeConfig, target string) (string, int, bool) {
	for _, handler := range config.Handlers() {
		if strings.TrimRight(handler.Proxy, "/") != target || handler.Path != "/" {
			continue
		}
		name, portText, ok := strings.Cut(handler.HostPort, ":")
		port, err := strconv.Atoi(portText)
		if !ok || err != nil {
			continue
		}
		return publicURL(name, port), port, true
	}
	return "", 0, false
}

// publicURL is https://name, with the port unless it is 443.
func publicURL(name string, port int) string {
	if port == 443 {
		return "https://" + name
	}
	return "https://" + name + ":" + strconv.Itoa(port)
}

func (s *Service) target() (string, error) {
	if s.opts.Target == nil {
		return "", errors.New("crowsnest has no loopback address for tailscale serve to proxy to")
	}
	target, err := s.opts.Target()
	if err != nil {
		return "", err
	}
	if !targetPattern.MatchString(target) {
		return "", errors.New("crowsnest has no loopback address for tailscale serve to proxy to")
	}
	return target, nil
}

// runner runs read-only tailscale commands through the allowlist.
func (s *Service) runner() tailscale.Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if err := CheckArgs(args); err != nil {
			return nil, err
		}
		if slices.Contains(args, "--https") || (len(args) > 0 && args[0] == "serve" && len(args) == 3 && args[1] != "status") {
			return nil, errors.New("tsserve: the runner is read-only; serve runs only as the supervised child")
		}
		return tailscale.RunBinary(ctx, s.opts.Binary, args...)
	}
}

// ---- State --------------------------------------------------------------------

func (s *Service) setView(view View) {
	s.mu.Lock()
	view.Enabled = s.enabled
	s.view = view
	s.mu.Unlock()
}

func (s *Service) updateView(change func(*View)) {
	s.mu.Lock()
	change(&s.view)
	s.mu.Unlock()
}

func (s *Service) journal(ctx context.Context, alert bool, summary string) {
	if s.opts.Journal != nil {
		s.opts.Journal(ctx, alert, summary)
	}
}

func (s *Service) now() *time.Time {
	now := s.opts.Now()
	return &now
}
