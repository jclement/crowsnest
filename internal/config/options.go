// The option table: one row per setting, naming its flag, its environment variables, its
// config-file key and its default. Flags, env lookup, the config-file key check and the README
// table all derive from this list, so a new setting is one new row here plus one field in Config.

package config

import (
	"fmt"
	"strconv"

	"github.com/spf13/pflag"
)

// kind is how an option's raw value is parsed.
type kind int

const (
	kindString kind = iota
	kindBool
	kindInt
	kindDuration
	kindList // repeatable flag; comma-separated in env; a YAML list or string in the file
)

// option describes one setting. name is both the flag name and the config-file key.
type option struct {
	name   string
	env    []string // environment variables, first set one wins
	kind   kind
	def    string // default, in the same text form a flag would take
	usage  string
	hidden bool // dev-only; not shown in --help
	// emptyIsValue makes an environment variable that is set but empty count as given (the empty
	// value), rather than as unset. For options whose empty value means something different from
	// their default: CROWSNEST_MODEL= means "Claude's own default", not "sonnet".
	emptyIsValue bool
}

// Option names, used by the loader to pick values out by name.
const (
	optConfig              = "config"
	optUsername            = "username"
	optPassword            = "password"
	optPasswordFile        = "password-file"
	optNoAuth              = "no-auth"
	optBind                = "bind"
	optPort                = "port"
	optSession             = "session"
	optDir                 = "dir"
	optModel               = "model"
	optJobModel            = "job-model"
	optPermissionMode      = "permission-mode"
	optPokeInterval        = "poke-interval"
	optNoOrchestrator      = "no-orchestrator"
	optAllowedHost         = "allowed-host"
	optTLSCert             = "tls-cert"
	optTLSKey              = "tls-key"
	optElevenLabsAPIKey    = "elevenlabs-api-key"      //nolint:gosec // a flag name, not a credential
	optElevenLabsKeyFile   = "elevenlabs-api-key-file" //nolint:gosec // a flag name, not a credential
	optElevenLabsVoice     = "elevenlabs-voice"
	optElevenLabsURL       = "elevenlabs-url"
	optNotifyURL           = "notify-url"
	optNotifyURLFile       = "notify-url-file"
	optTrustedProxy        = "trusted-proxy"
	optTrustedProxyHeader  = "trusted-proxy-header"
	optDemo                = "demo"
	optIKnowThisIsOpen     = "i-know-this-is-open"
	optAllowDefaultSession = "allow-default-session"
	optDataDir             = "data-dir"
	optWebDev              = "web-dev"
)

// envSuppressAuth is the generic dev-bypass variable of Jeff's other projects. It may well be
// exported by some other project's environment, so it only ever turns auth off on a
// loopback-only bind (R1 L4); CROWSNEST_NO_AUTH and --no-auth are the deliberate switches.
const envSuppressAuth = "SUPPRESS_AUTH"

// options is the table. Order here is the order of --help.
var options = []option{
	{name: optUsername, env: []string{"CROWSNEST_USERNAME"}, usage: "login name"},
	{name: optPassword, env: []string{"CROWSNEST_PASSWORD"}, usage: "login password (visible in ps: prefer CROWSNEST_PASSWORD or --password-file)"},
	{name: optPasswordFile, env: []string{"CROWSNEST_PASSWORD_FILE"}, usage: "file holding the password (first line)"},
	{name: optNoAuth, env: []string{"CROWSNEST_NO_AUTH", envSuppressAuth}, kind: kindBool, def: "false", usage: "serve without a login (trusted tailnet or localhost only)"},
	{name: optBind, env: []string{"CROWSNEST_BIND"}, kind: kindList, def: "localhost", usage: "localhost, tailnet, an interface name, an IP, or ip:port (repeatable)"},
	{name: optPort, env: []string{"CROWSNEST_PORT"}, kind: kindInt, def: strconv.Itoa(DefaultPort), usage: "port for binds that do not name one"},
	{name: optSession, env: []string{"CROWSNEST_SESSION"}, def: DefaultSession, usage: "herdr session to create or connect to"},
	{name: optDir, env: []string{"CROWSNEST_DIR"}, usage: "orchestrator working directory (default <data-dir>/orchestrator)"},
	{name: optModel, env: []string{"CROWSNEST_MODEL"}, def: DefaultModel, emptyIsValue: true, usage: "orchestrator model; an empty value means Claude's own default"},
	{name: optJobModel, env: []string{"CROWSNEST_JOB_MODEL"}, usage: "job agents' model when a job names none (default: Claude's own)"},
	{name: optPermissionMode, env: []string{"CROWSNEST_PERMISSION_MODE"}, def: DefaultPermissionMode, usage: "orchestrator permission mode"},
	{name: optPokeInterval, env: []string{"CROWSNEST_POKE_INTERVAL"}, kind: kindDuration, def: "15m", usage: "timer poke interval; 0 disables timer pokes"},
	{name: optNoOrchestrator, env: []string{"CROWSNEST_NO_ORCHESTRATOR"}, kind: kindBool, def: "false", usage: "serve the UI over the session without starting Claude"},
	{name: optAllowedHost, env: []string{"CROWSNEST_ALLOWED_HOSTS"}, kind: kindList, usage: "extra Host/Origin name, e.g. a reverse proxy or MagicDNS name (repeatable)"},
	{name: optTrustedProxy, env: []string{"CROWSNEST_TRUSTED_PROXY"}, kind: kindList, usage: "loopback, or an IP or CIDR: a reverse proxy whose client-IP header (--trusted-proxy-header) is believed (repeatable)"},
	{name: optTrustedProxyHeader, env: []string{"CROWSNEST_TRUSTED_PROXY_HEADER"}, def: "x-forwarded-for", usage: "the header a trusted proxy names its client in: x-forwarded-for (its last hop) or cf-connecting-ip (cloudflared only)"},
	{name: optTLSCert, env: []string{"CROWSNEST_TLS_CERT"}, usage: "PEM certificate: serve HTTPS directly"},
	{name: optTLSKey, env: []string{"CROWSNEST_TLS_KEY"}, usage: "PEM key for --tls-cert"},
	{name: optElevenLabsAPIKey, env: []string{"ELEVENLABS_API_KEY"}, usage: "ElevenLabs API key: enables talk mode (prefer the env var or --elevenlabs-api-key-file)"},
	{name: optElevenLabsKeyFile, env: []string{"ELEVENLABS_API_KEY_FILE"}, usage: "file holding the ElevenLabs API key (first line)"},
	{name: optElevenLabsVoice, env: []string{"CROWSNEST_ELEVENLABS_VOICE"}, def: DefaultElevenLabsVoice, usage: "ElevenLabs voice id for spoken replies"},
	{name: optElevenLabsURL, env: []string{"CROWSNEST_ELEVENLABS_URL"}, def: DefaultElevenLabsURL, usage: "ElevenLabs API base URL (https, or http only on loopback)"},
	{name: optNotifyURL, env: []string{"CROWSNEST_NOTIFY_URL"}, usage: "POST a one-line notice here when something needs you (ntfy-style)"},
	{name: optNotifyURLFile, env: []string{"CROWSNEST_NOTIFY_URL_FILE"}, usage: "file holding the notify URL (first line)"},
	{name: optDemo, env: []string{"CROWSNEST_DEMO"}, kind: kindBool, def: "false", usage: "in-memory fake herdr and scripted orchestrator"},
	{name: optIKnowThisIsOpen, env: []string{"CROWSNEST_I_KNOW_THIS_IS_OPEN"}, kind: kindBool, def: "false", usage: "allow --no-auth on a bind that is not loopback or tailnet"},
	{name: optAllowDefaultSession, env: []string{"CROWSNEST_ALLOW_DEFAULT_SESSION"}, kind: kindBool, def: "false", usage: "allow --session default (your main herdr session)"},
	{name: optDataDir, env: []string{"CROWSNEST_DATA_DIR"}, usage: "data directory for the database (default ~/.local/share/crowsnest)"},
	{name: optConfig, env: []string{"CROWSNEST_CONFIG"}, usage: "config file (default ~/.config/crowsnest/config.yaml)"},
	{name: optWebDev, env: []string{"CROWSNEST_WEB_DEV"}, kind: kindBool, def: "false", usage: "development: the UI is served by Vite, not embedded", hidden: true},
}

// RegisterFlags adds every option to fs. Call it once on the command that loads the config.
func RegisterFlags(fs *pflag.FlagSet) {
	for _, opt := range options {
		registerFlag(fs, opt)
	}
}

// registerFlag defines one flag. Defaults are shown in --help but the loader never reads a flag
// that was not Changed, so env and file values are not shadowed by flag defaults.
func registerFlag(fs *pflag.FlagSet, opt option) {
	usage := opt.usage
	if len(opt.env) > 0 {
		usage = fmt.Sprintf("%s [$%s]", usage, opt.env[0])
	}
	switch opt.kind {
	case kindBool:
		fs.Bool(opt.name, opt.def == "true", usage)
	case kindInt:
		value, _ := strconv.Atoi(opt.def)
		fs.Int(opt.name, value, usage)
	case kindList:
		var def []string
		if opt.def != "" {
			def = []string{opt.def}
		}
		fs.StringSlice(opt.name, def, usage)
	default: // kindString and kindDuration are both parsed by the loader from text
		fs.String(opt.name, opt.def, usage)
	}
	if opt.hidden {
		_ = fs.MarkHidden(opt.name)
	}
}

// lookupOption finds a row by name. It panics on an unknown name because that is a programming
// error in this package, never a user input.
func lookupOption(name string) option {
	for _, opt := range options {
		if opt.name == name {
			return opt
		}
	}
	panic("config: unknown option " + name)
}
