// The loader: resolves each option through flag > env > file > default, parses it into its
// type, and assembles Config. Viper is used only to read the YAML file; precedence is done here
// explicitly so that it is obvious, testable with an injected environment, and immune to
// Viper's habit of splitting env lists on spaces.

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Options carries the outside world into Load, so tests can supply their own.
type Options struct {
	// Getenv looks up an environment variable; nil means os.LookupEnv.
	Getenv func(string) (string, bool)
	// Interfaces lists network interfaces for bind resolution; nil means the real ones.
	Interfaces InterfaceLister
	// Hostname returns this machine's name for the host check; nil means os.Hostname.
	Hostname func() (string, error)
	// HomeDir returns the user's home directory; nil means os.UserHomeDir.
	HomeDir func() (string, error)
	// Stored are the settings saved in the app (kv rows settings.<option>), by option name, in
	// the text form a flag would take. They sit between the environment and the config file:
	// whoever starts the process with a flag or a variable wins, then the app, then the file
	// (DESIGN.md 3). Only options in EditableOptions are read from here; anything else is ignored.
	Stored map[string]string
	// TailnetName returns this machine's Tailscale MagicDNS name ("" for none), for the host
	// allowlist: requests through `tailscale serve` carry it as Host. Nil: no detection (tests,
	// and every caller but serve and doctor). --no-tailscale-detect skips it.
	TailnetName func() string
}

func (o *Options) fillDefaults() {
	if o.Getenv == nil {
		o.Getenv = os.LookupEnv
	}
	if o.Interfaces == nil {
		o.Interfaces = SystemInterfaces
	}
	if o.Hostname == nil {
		o.Hostname = os.Hostname
	}
	if o.HomeDir == nil {
		o.HomeDir = os.UserHomeDir
	}
}

// loader holds the three value sources while one Load runs.
type loader struct {
	flags *pflag.FlagSet
	file  *viper.Viper // nil when no config file was read
	opts  Options
	home  string
	// warnings are things worth telling the user that do not stop the load (Config.Warnings).
	warnings []string
	// sources records where each option looked up came from (Config.Sources).
	sources map[string]string
}

// source is where a value came from, for error messages that tell the user what to fix.
type source struct {
	raw   any
	where string // "--port", "CROWSNEST_PORT", "config.yaml", "default"
}

// Load resolves, parses and validates every option. flags must have been set up with
// RegisterFlags and parsed. The returned Config is complete; CheckStartup adds the refusals
// that apply only to `serve`.
func Load(flags *pflag.FlagSet, opts Options) (Config, error) {
	opts.fillDefaults()
	home, err := opts.HomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("finding home directory: %w", err)
	}
	l := &loader{flags: flags, opts: opts, home: home, sources: map[string]string{}}
	cfg := Config{}
	if cfg.ConfigFile, err = l.readFile(); err != nil {
		return Config{}, err
	}
	steps := []func(*Config) error{l.loadAuth, l.loadNetwork, l.loadOrchestrator, l.loadExtras, l.loadUpkeep, l.loadAttachments, l.loadGuard, l.loadEngine, l.loadLanding, l.loadAccept}
	for _, step := range steps {
		if err := step(&cfg); err != nil {
			return Config{}, err
		}
	}
	cfg.Warnings = l.warnings
	cfg.Sources = l.allSources()
	return cfg, nil
}

// ---- Value sources -----------------------------------------------------------

// lookup applies the precedence rule for one option and records where the value came from.
func (l *loader) lookup(name string) source {
	src := l.resolve(name)
	l.sources[name] = src.where
	return src
}

// allSources is where every option's value came from, looked up or not.
func (l *loader) allSources() map[string]string {
	for _, opt := range options {
		if _, seen := l.sources[opt.name]; !seen {
			l.lookup(opt.name)
		}
	}
	return l.sources
}

// resolve is the precedence rule: flag, environment, the app's stored settings, file, default.
func (l *loader) resolve(name string) source {
	opt := lookupOption(name)
	if flag := l.flags.Lookup(name); flag != nil && flag.Changed {
		return l.flagValue(flag, opt)
	}
	for _, env := range opt.env {
		if value, ok := l.opts.Getenv(env); ok && (value != "" || opt.emptyIsValue) {
			return source{raw: value, where: env}
		}
	}
	if value, ok := l.opts.Stored[name]; ok && Editable(name) {
		return source{raw: value, where: SourceDatabase}
	}
	if l.file != nil && l.file.IsSet(name) {
		return source{raw: l.file.Get(name), where: filepath.Base(l.file.ConfigFileUsed())}
	}
	if opt.kind == kindList {
		if opt.def == "" {
			return source{raw: []string(nil), where: "default"}
		}
		return source{raw: []string{opt.def}, where: "default"}
	}
	return source{raw: opt.def, where: "default"}
}

func (l *loader) flagValue(flag *pflag.Flag, opt option) source {
	where := "--" + opt.name
	if opt.kind == kindList {
		values, _ := l.flags.GetStringSlice(opt.name)
		return source{raw: values, where: where}
	}
	return source{raw: flag.Value.String(), where: where}
}

// readFile reads the YAML config file. A missing default file is fine; a missing file the user
// named explicitly is an error, and so is a key no option knows (a typo would otherwise be
// silently ignored, and a silently ignored `no-auth: false` is the wrong kind of surprise).
func (l *loader) readFile() (string, error) {
	path, explicit := l.configPath()
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) && !explicit {
			return "", nil
		}
		return "", fmt.Errorf("reading config file %s: %w", path, err)
	}
	file := viper.New()
	file.SetConfigFile(path)
	file.SetConfigType("yaml")
	if err := file.ReadInConfig(); err != nil {
		return "", fmt.Errorf("reading config file %s: %w", path, err)
	}
	for _, key := range file.AllKeys() {
		if key == optConfig || !knownOption(key) {
			return "", fmt.Errorf("config file %s: unknown key %q (keys are the flag names, e.g. %q)", path, key, optUsername)
		}
	}
	l.file = file
	if file.IsSet(optPassword) {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o044 != 0 {
			l.warnings = append(l.warnings, fmt.Sprintf(
				"config file %s holds a password and other users can read it (mode %s); run `chmod 600 %s`, or move the password to CROWSNEST_PASSWORD or --password-file",
				path, info.Mode().Perm(), path))
		}
	}
	return path, nil
}

// configPath is --config / CROWSNEST_CONFIG, else $XDG_CONFIG_HOME/crowsnest/config.yaml.
func (l *loader) configPath() (path string, explicit bool) {
	if flag := l.flags.Lookup(optConfig); flag != nil && flag.Changed {
		return l.expandHome(flag.Value.String()), true
	}
	if value, ok := l.opts.Getenv("CROWSNEST_CONFIG"); ok && value != "" {
		return l.expandHome(value), true
	}
	return filepath.Join(l.xdgDir("XDG_CONFIG_HOME", ".config"), "crowsnest", "config.yaml"), false
}

func knownOption(name string) bool {
	return slices.ContainsFunc(options, func(opt option) bool { return opt.name == name })
}

// xdgDir returns $env if set to an absolute path, else ~/fallback (the XDG base-dir rule).
func (l *loader) xdgDir(env, fallback string) string {
	if value, ok := l.opts.Getenv(env); ok && filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(l.home, fallback)
}

func (l *loader) expandHome(path string) string {
	if path == "~" {
		return l.home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(l.home, path[2:])
	}
	return path
}

// ---- Typed getters -----------------------------------------------------------

func (l *loader) str(name string) string {
	return strings.TrimSpace(rawText(l.lookup(name).raw))
}

// rawText renders a raw value; a YAML key with no value arrives as nil and means "empty".
func rawText(raw any) string {
	if raw == nil {
		return ""
	}
	return fmt.Sprint(raw)
}

func (l *loader) path(name string) string {
	return l.expandHome(l.str(name))
}

func (l *loader) boolean(name string) (bool, error) {
	src := l.lookup(name)
	if value, ok := src.raw.(bool); ok {
		return value, nil
	}
	text := strings.ToLower(strings.TrimSpace(rawText(src.raw)))
	switch text {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off", "":
		return false, nil
	}
	return false, fmt.Errorf("--%s from %s: %q is not true or false", name, src.where, text)
}

func (l *loader) integer(name string) (int, error) {
	src := l.lookup(name)
	text := strings.TrimSpace(rawText(src.raw))
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("--%s from %s: %q is not a whole number", name, src.where, text)
	}
	return value, nil
}

// duration accepts Go duration text ("15m", "90s") and a bare 0. A bare non-zero number is
// refused because "15" could mean seconds or minutes and guessing wrong is silent.
func (l *loader) duration(name string) (time.Duration, error) {
	src := l.lookup(name)
	text := strings.TrimSpace(rawText(src.raw))
	if text == "0" {
		return 0, nil
	}
	value, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("--%s from %s: %q is not a duration; pass something like 15m, 1h or 0", name, src.where, text)
	}
	if value < 0 {
		return 0, fmt.Errorf("--%s from %s: %q is negative; pass 0 to disable", name, src.where, text)
	}
	return value, nil
}

// list flattens a flag slice, a comma-separated env string or a YAML list (or string) into
// trimmed, non-empty values.
func (l *loader) list(name string) []string {
	src := l.lookup(name)
	var items []string
	switch raw := src.raw.(type) {
	case []string:
		items = raw
	case []any:
		for _, item := range raw {
			items = append(items, fmt.Sprint(item))
		}
	default:
		items = []string{rawText(raw)}
	}
	var out []string
	for _, item := range items {
		for part := range strings.SplitSeq(item, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// ---- Assembly ----------------------------------------------------------------

func (l *loader) loadAuth(cfg *Config) error {
	var err error
	if cfg.NoAuth, err = l.boolean(optNoAuth); err != nil {
		return err
	}
	cfg.NoAuthFromSuppressAuth = cfg.NoAuth && l.lookup(optNoAuth).where == envSuppressAuth
	cfg.Username = l.str(optUsername)
	password := l.lookup(optPassword)
	passwordFile := l.path(optPasswordFile)
	cfg.Password = rawText(password.raw) // passwords keep their spaces
	if passwordFile != "" {
		if cfg.Password != "" {
			return fmt.Errorf("both a password (from %s) and --password-file were given; pass only one", password.where)
		}
		if cfg.Password, err = readPasswordFile(passwordFile); err != nil {
			return err
		}
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return errors.New("--username and --password must be given together (password may come from CROWSNEST_PASSWORD or --password-file)")
	}
	if cfg.NoAuth && cfg.Username != "" {
		return errors.New("--no-auth was given together with a username and password; pass one or the other, not both")
	}
	return nil
}

// othersMayAccess are the permission bits that let someone other than the owner read or write a
// file. A password file with any of them is refused: others could read the password, or replace
// it with one they know (R1 L5).
const othersMayAccess fs.FileMode = 0o066

// ReadPasswordFile is readPasswordFile for other commands (`crowsnest reset-password`): the first
// line of path, refused when its group or others may read or write it.
func ReadPasswordFile(path string) (string, error) { return readPasswordFile(path) }

// readPasswordFile returns the first line of path without its line ending. It refuses a file
// that its group or others may read or write.
func readPasswordFile(path string) (string, error) {
	return readSecretFile(optPasswordFile, "the password", path)
}

// readSecretFile is readPasswordFile for any --<flag>-file option: the first line of path, from
// a file only its owner may read or write. A file form keeps a secret out of the environment of
// whatever started crowsnest (tini in the devbox, R1b N1), where every agent of the same user
// could read it.
func readSecretFile(flag, what, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("reading --%s: %w", flag, err)
	}
	if info.Mode().Perm()&othersMayAccess != 0 {
		return "", fmt.Errorf("--%s %s can be read or written by other users (mode %s); run `chmod 600 %s` and start again",
			flag, path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is configuration
	if err != nil {
		return "", fmt.Errorf("reading --%s: %w", flag, err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		return "", fmt.Errorf("--%s %s: the first line is empty; put %s on it", flag, path, what)
	}
	return line, nil
}

// secretOrFile returns the value of option, or the first line of fileOption's file; both at
// once is refused.
func (l *loader) secretOrFile(option, fileOption, what string) (string, error) {
	value, path := l.str(option), l.path(fileOption)
	if path == "" {
		return value, nil
	}
	if value != "" {
		return "", fmt.Errorf("both --%s and --%s were given; pass only one", option, fileOption)
	}
	return readSecretFile(fileOption, what, path)
}

// parseTrustedProxies reads --trusted-proxy: "loopback" (127.0.0.0/8 and ::1), an IP, or a CIDR.
func parseTrustedProxies(values []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, value := range values {
		switch value = strings.TrimSpace(strings.ToLower(value)); {
		case value == "loopback":
			out = append(out, netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"))
		case strings.Contains(value, "/"):
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, fmt.Errorf("--trusted-proxy %q is not loopback, an IP or a CIDR: %w", value, err)
			}
			out = append(out, prefix.Masked())
		default:
			addr, err := netip.ParseAddr(value)
			if err != nil {
				return nil, fmt.Errorf("--trusted-proxy %q is not loopback, an IP or a CIDR", value)
			}
			out = append(out, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
		}
	}
	return out, nil
}

func (l *loader) loadNetwork(cfg *Config) error {
	var err error
	if cfg.Port, err = l.integer(optPort); err != nil {
		return err
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("--port %d is out of range; pass a port from 1 to 65535", cfg.Port)
	}
	cfg.Binds = l.list(optBind)
	if len(cfg.Binds) == 0 {
		return errors.New("--bind is empty; pass localhost, tailnet, an interface name, an IP or ip:port")
	}
	interfaces, err := l.opts.Interfaces()
	if err != nil {
		return fmt.Errorf("listing network interfaces: %w", err)
	}
	if cfg.Listeners, err = resolveBinds(cfg.Binds, cfg.Port, interfaces); err != nil {
		return err
	}
	for _, host := range l.list(optAllowedHost) {
		cfg.AllowedHosts = append(cfg.AllowedHosts, strings.ToLower(host))
	}
	if cfg.TrustedProxies, err = parseTrustedProxies(l.list(optTrustedProxy)); err != nil {
		return err
	}
	switch header := strings.ToLower(strings.TrimSpace(l.str(optTrustedProxyHeader))); header {
	case HeaderXForwardedFor, HeaderCFConnectingIP:
		cfg.TrustedProxyHeader = header
	default:
		return fmt.Errorf("--trusted-proxy-header %q is not x-forwarded-for or cf-connecting-ip", header)
	}
	if err := l.loadTLS(cfg); err != nil {
		return err
	}
	if cfg.NoTailscaleDetect, err = l.boolean(optNoTailscaleDetect); err != nil {
		return err
	}
	extra := cfg.AllowedHosts
	if !cfg.NoTailscaleDetect && l.opts.TailnetName != nil {
		if name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(l.opts.TailnetName()), ".")); name != "" {
			cfg.TailnetName = name
			extra = append(slices.Clone(extra), name)
		}
	}
	cfg.HostNames = hostNames(cfg.Listeners, interfaces, l.hostname(), extra)
	return nil
}

func (l *loader) hostname() string {
	name, err := l.opts.Hostname()
	if err != nil {
		return ""
	}
	return name
}

func (l *loader) loadTLS(cfg *Config) error {
	cfg.TLSCert = l.path(optTLSCert)
	cfg.TLSKey = l.path(optTLSKey)
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return errors.New("--tls-cert and --tls-key must be given together")
	}
	for _, file := range []string{cfg.TLSCert, cfg.TLSKey} {
		if file == "" {
			continue
		}
		if _, err := os.Stat(file); err != nil {
			return fmt.Errorf("TLS file: %w", err)
		}
	}
	return nil
}

// sessionNamePattern matches herdr's session directory names; anything else could escape the
// sessions directory or confuse the CLI.
var sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// modelNamePattern is what a model name may look like: it becomes one argument of Claude's
// command line. The characters are a job's model's (jobs.modelPattern); the first must be a
// letter or digit, so a value can never read as another flag ("--model --something").
var modelNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\[\]-]{0,63}$`)

// permissionModes are Claude Code's --permission-mode choices (claude --help, 2.1.x).
var permissionModes = []string{"acceptEdits", "auto", "bypassPermissions", "dontAsk", "manual", "plan"}

func (l *loader) loadOrchestrator(cfg *Config) error {
	var err error
	cfg.Session = l.str(optSession)
	if !sessionNamePattern.MatchString(cfg.Session) {
		return fmt.Errorf("--session %q is not a valid herdr session name; use letters, digits, '.', '_' or '-'", cfg.Session)
	}
	cfg.DataDir = l.path(optDataDir)
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(l.xdgDir("XDG_DATA_HOME", ".local/share"), "crowsnest")
	}
	// State (logs, sockets) is per session, so two crowsnests on two sessions never share it.
	cfg.StateDir = filepath.Join(l.xdgDir("XDG_STATE_HOME", ".local/state"), "crowsnest", cfg.Session)
	cfg.Dir = l.path(optDir)
	if cfg.Dir == "" {
		cfg.Dir = filepath.Join(cfg.DataDir, "orchestrator")
	}
	cfg.Model = l.str(optModel)
	cfg.JobModel = l.str(optJobModel)
	for name, model := range map[string]string{optModel: cfg.Model, optJobModel: cfg.JobModel} {
		if model != "" && !modelNamePattern.MatchString(model) {
			return fmt.Errorf("--%s %q is not a model name (letters, digits and . _ : [ ] -, at most 64)", name, model)
		}
	}
	cfg.PermissionMode = l.str(optPermissionMode)
	if !slices.Contains(permissionModes, cfg.PermissionMode) {
		return fmt.Errorf("--permission-mode %q is not one of %s", cfg.PermissionMode, strings.Join(permissionModes, ", "))
	}
	if cfg.PokeInterval, err = l.duration(optPokeInterval); err != nil {
		return err
	}
	cfg.NoOrchestrator, err = l.boolean(optNoOrchestrator)
	return err
}

func (l *loader) loadExtras(cfg *Config) error {
	var err error
	if cfg.ElevenLabsAPIKey, err = l.secretOrFile(optElevenLabsAPIKey, optElevenLabsKeyFile, "the API key"); err != nil {
		return err
	}
	cfg.ElevenLabsVoice = l.str(optElevenLabsVoice)
	cfg.ElevenLabsVoiceSource = l.lookup(optElevenLabsVoice).where
	cfg.ElevenLabsURL = strings.TrimRight(l.str(optElevenLabsURL), "/")
	if cfg.NotifyURL, err = l.secretOrFile(optNotifyURL, optNotifyURLFile, "the URL"); err != nil {
		return err
	}
	if err := checkHTTPURL(optElevenLabsURL, cfg.ElevenLabsURL); err != nil {
		return err
	}
	if !SecureOrLoopbackURL(cfg.ElevenLabsURL) {
		// R2-L4: the API key travels in a header on every request.
		return fmt.Errorf("--%s %q must be https (plain http only to a loopback address, such as a local fake); the API key travels with every request", optElevenLabsURL, cfg.ElevenLabsURL)
	}
	if err := checkHTTPURL(optNotifyURL, cfg.NotifyURL); err != nil {
		return err
	}
	bools := []struct {
		name   string
		target *bool
	}{
		{optDemo, &cfg.Demo},
		{optIKnowThisIsOpen, &cfg.IKnowThisIsOpen},
		{optAllowDefaultSession, &cfg.AllowDefaultSession},
		{optWebDev, &cfg.WebDev},
	}
	for _, item := range bools {
		value, err := l.boolean(item.name)
		if err != nil {
			return err
		}
		*item.target = value
	}
	return nil
}

// SecureOrLoopbackURL reports whether raw is https, or http to a loopback host (localhost,
// 127.0.0.0/8, ::1). An empty value counts as secure (the default is https).
func SecureOrLoopbackURL(raw string) bool {
	if raw == "" {
		return true
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return true
	case "http":
		host := parsed.Hostname()
		if strings.EqualFold(host, "localhost") {
			return true
		}
		addr, err := netip.ParseAddr(host)
		return err == nil && addr.IsLoopback()
	}
	return false
}

// checkHTTPURL accepts an empty value or an absolute http(s) URL with a host.
func checkHTTPURL(name, value string) error {
	if value == "" {
		return nil
	}
	scheme, rest, ok := strings.Cut(value, "://")
	if !ok || (scheme != "http" && scheme != "https") || rest == "" || strings.HasPrefix(rest, "/") {
		return fmt.Errorf("--%s %q is not an http(s) URL; pass something like https://ntfy.sh/your-topic", name, value)
	}
	return nil
}

// FlagTable lists every visible option as (flag, env, default, usage) rows, in --help order. The README's config table is checked against it in tests.
func FlagTable() [][4]string {
	rows := make([][4]string, 0, len(options))
	for _, opt := range options {
		if opt.hidden {
			continue
		}
		rows = append(rows, [4]string{"--" + opt.name, strings.Join(opt.env, ", "), opt.def, opt.usage})
	}
	return rows
}

// secretEnv are the environment variables that hold a secret or switch authentication. After
// the config is loaded they have done their job; UnsetSecretEnv removes them so no child
// process can inherit them (R1 H1). The notify URL is among them because an ntfy topic in it is
// as good as a password for posting to (and on a public server, reading) the human's phone.
var secretEnv = []string{
	"CROWSNEST_PASSWORD", "CROWSNEST_PASSWORD_FILE", "ELEVENLABS_API_KEY", "CROWSNEST_NOTIFY_URL",
	"ELEVENLABS_API_KEY_FILE", "CROWSNEST_NOTIFY_URL_FILE",
	"CROWSNEST_NO_AUTH", "SUPPRESS_AUTH", "CROWSNEST_I_KNOW_THIS_IS_OPEN",
}

// SecretEnvNames returns the names UnsetSecretEnv removes, for serve's self re-exec (which keeps
// them out of the process's own initial environment as well).
func SecretEnvNames() []string { return append([]string(nil), secretEnv...) }

// UnsetSecretEnv removes the secret and auth-switch variables from this process's environment.
// Call it once the config has been loaded.
func UnsetSecretEnv() {
	for _, name := range secretEnv {
		_ = os.Unsetenv(name)
	}
}
