// Package config turns flags, environment variables and the YAML config file into one plain,
// validated Config struct. It is the only package that knows about Cobra flags, Viper or the
// environment: everything else in crowsnest takes a Config and never looks anything up itself.
//
// Precedence is flag, then environment (CROWSNEST_*), then ~/.config/crowsnest/config.yaml, then
// the built-in default (DESIGN.md section 3). Load parses and validates every value; CheckStartup
// applies the fail-closed refusals that only matter when the server is about to listen.
package config

import (
	"log/slog"
	"net/netip"
	"path/filepath"
	"time"
)

// DefaultPort is C-R-O-W on a phone keypad.
const DefaultPort = 2769

// DefaultSession is the herdr session crowsnest creates or connects to when none is named.
const DefaultSession = "crowsnest"

// DefaultPermissionMode is the orchestrator's Claude Code permission mode.
const DefaultPermissionMode = "auto"

// DefaultPokeInterval is how often the timer considers poking the orchestrator.
const DefaultPokeInterval = 15 * time.Minute

// DefaultModel is the orchestrator's model. The orchestrator is a lead: it delegates, answers
// pokes and relays, and most of its turns are short, so a cheaper, faster model serves it better
// than the most capable one (DESIGN.md 17, "The orchestrator runs on sonnet"). Job agents keep
// Claude's own default unless --job-model or the job names one.
const DefaultModel = "sonnet"

// DefaultElevenLabsVoice is a stock ElevenLabs voice ("Rachel"), used until one is chosen.
const DefaultElevenLabsVoice = "21m00Tcm4TlvDq8ikWAM"

// DefaultElevenLabsURL is the ElevenLabs API base; tests point it at a fake.
const DefaultElevenLabsURL = "https://api.elevenlabs.io"

// Config is the fully resolved configuration. Every field has been parsed and validated by Load;
// secrets (Password, ElevenLabsAPIKey) must never be logged, which is why LogValue omits them.
// Client-IP headers a trusted proxy may name its client in (--trusted-proxy-header).
const (
	HeaderXForwardedFor  = "x-forwarded-for"
	HeaderCFConnectingIP = "cf-connecting-ip"
)

type Config struct {
	// ---- Authentication ----

	Username string // login name; empty only when NoAuth
	Password string // login password, already read from PasswordFile when that was given
	NoAuth   bool   // serve without a login

	// ---- Network ----

	Binds        []string   // the bind values as given (localhost, tailnet, en0, an IP, ip:port)
	Listeners    []Listener // the concrete addresses those binds resolved to
	Port         int        // port used for binds that do not carry their own
	AllowedHosts []string   // extra Host/Origin names from --allowed-host, lowercased
	// TrustedProxies are the peers whose client-IP header is believed (--trusted-proxy, R2-L2):
	// a tunnel or proxy on loopback makes every internet client one peer otherwise.
	TrustedProxies []netip.Prefix
	// TrustedProxyHeader is the one header a trusted proxy's client is read from
	// (--trusted-proxy-header, R3-L1): HeaderXForwardedFor (its last hop, the default) or
	// HeaderCFConnectingIP (only for cloudflared, which overwrites it; any other proxy passes a
	// client's own CF-Connecting-IP through, so believing it would let a client pick its bucket).
	TrustedProxyHeader string
	TLSCert            string // path to a PEM certificate; empty means plain HTTP
	TLSKey             string // path to the matching PEM key

	// HostNames is every name the Host header may carry: loopback names, bound IPs, this
	// machine's hostname and AllowedHosts. The host check in httpd reads it (DESIGN.md 4.3).
	HostNames []string

	// ---- herdr and the orchestrator ----

	Session        string        // herdr session name
	Dir            string        // orchestrator working directory
	Model          string        // orchestrator model (sonnet by default); empty means Claude's default
	JobModel       string        // job agents' model when a job names none; empty means Claude's default
	PermissionMode string        // orchestrator permission mode
	PokeInterval   time.Duration // timer poke interval; 0 disables timer pokes
	NoOrchestrator bool          // serve the UI over the session without starting Claude

	// ---- Name, Remote Control and upkeep (upkeep.go) ----

	Name             string // this instance's name: --name, else the short hostname
	NoRemoteControl  bool   // start the orchestrator without Remote Control
	JobRemoteControl bool   // start job agents with Remote Control too
	CompactAt        int    // compact the orchestrator past this many tokens; 0 off
	FreshAt          int    // daily new conversation, minutes after local midnight; -1 off

	// ---- Talk mode and notices ----

	ElevenLabsAPIKey string // enables talk mode when set
	ElevenLabsVoice  string // TTS voice id: the default until one is chosen in the web app
	// ElevenLabsVoiceSource says where ElevenLabsVoice came from ("--elevenlabs-voice",
	// "CROWSNEST_ELEVENLABS_VOICE", the config file's name, or "default"), for the settings sheet.
	ElevenLabsVoiceSource string
	ElevenLabsURL         string // ElevenLabs API base URL
	NotifyURL             string // ntfy-style URL for one-line notices

	// ---- Modes and overrides ----

	Demo            bool // fake herdr and scripted orchestrator
	IKnowThisIsOpen bool // allow --no-auth on a non-loopback, non-tailnet bind
	// NoAuthFromSuppressAuth is true when NoAuth came from the generic SUPPRESS_AUTH variable
	// rather than --no-auth or CROWSNEST_NO_AUTH; it is then honoured on loopback only (R1 L4).
	NoAuthFromSuppressAuth bool
	AllowDefaultSession    bool   // allow --session default
	WebDev                 bool   // UI is served by Vite; do not require embedded assets
	DataDir                string // where the SQLite database lives
	StateDir               string // logs and runtime state for this session: ~/.local/state/crowsnest/<session>
	ConfigFile             string // the config file that was read, empty if none

	// Warnings are problems that do not stop the start but should be logged, such as a config
	// file holding a password that other users can read (R1 L5).
	Warnings []string
}

// Listener is one address the server will listen on.
type Listener struct {
	Addr netip.AddrPort
	// Optional listeners may fail to bind without stopping the server. `localhost` resolves to
	// both 127.0.0.1 and ::1, and a machine without IPv6 should not refuse to start over ::1.
	Optional bool
	// Source is the bind value this address came from, for error messages.
	Source string
}

// DatabasePath is the SQLite file for this session: one database per herdr session, so two
// crowsnests on two sessions never share state.
func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, c.Session+".db")
}

// TLS reports whether the server terminates HTTPS itself.
func (c Config) TLS() bool { return c.TLSCert != "" }

// VoiceEnabled reports whether talk mode can be offered.
func (c Config) VoiceEnabled() bool { return c.ElevenLabsAPIKey != "" }

// LogValue makes Config safe to pass to slog: secrets are reduced to whether they are set.
func (c Config) LogValue() slog.Value {
	addrs := make([]string, 0, len(c.Listeners))
	for _, listener := range c.Listeners {
		addrs = append(addrs, listener.Addr.String())
	}
	return slog.GroupValue(
		slog.Bool("auth", !c.NoAuth),
		slog.String("username", c.Username),
		slog.Bool("password_set", c.Password != ""),
		slog.Any("listen", addrs),
		slog.Bool("tls", c.TLS()),
		slog.String("name", c.Name),
		slog.String("session", c.Session),
		slog.String("dir", c.Dir),
		slog.String("data_dir", c.DataDir),
		slog.String("state_dir", c.StateDir),
		slog.Bool("voice", c.VoiceEnabled()),
		slog.Bool("demo", c.Demo),
		slog.Bool("no_orchestrator", c.NoOrchestrator),
		slog.String("config_file", c.ConfigFile),
	)
}
