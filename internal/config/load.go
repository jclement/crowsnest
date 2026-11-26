// The loader: resolves each option through flag > env > file > default, parses it into its
// type, and assembles Config. Viper is used only to read the YAML file; precedence is done here
// explicitly so that it is obvious, testable with an injected environment, and immune to
// Viper's habit of splitting env lists on spaces.

package config

import (
	"errors"
	"fmt"
	"io/fs"
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
	l := &loader{flags: flags, opts: opts, home: home}
	cfg := Config{}
	if cfg.ConfigFile, err = l.readFile(); err != nil {
		return Config{}, err
	}
	steps := []func(*Config) error{l.loadAuth, l.loadNetwork, l.loadOrchestrator, l.loadExtras}
	for _, step := range steps {
		if err := step(&cfg); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// ---- Value sources -----------------------------------------------------------

// lookup applies the precedence rule for one option.
func (l *loader) lookup(name string) source {
	opt := lookupOption(name)
	if flag := l.flags.Lookup(name); flag != nil && flag.Changed {
		return l.flagValue(flag, opt)
	}
	for _, env := range opt.env {
		if value, ok := l.opts.Getenv(env); ok && value != "" {
			return source{raw: value, where: env}
		}
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

// readPasswordFile returns the first line of path without its line ending.
func readPasswordFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading --password-file: %w", err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		return "", fmt.Errorf("--password-file %s: the first line is empty; put the password on it", path)
	}
	return line, nil
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
	if err := l.loadTLS(cfg); err != nil {
		return err
	}
	cfg.HostNames = hostNames(cfg.Listeners, interfaces, l.hostname(), cfg.AllowedHosts)
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
	cfg.ElevenLabsAPIKey = l.str(optElevenLabsAPIKey)
	cfg.ElevenLabsVoice = l.str(optElevenLabsVoice)
	cfg.ElevenLabsURL = strings.TrimRight(l.str(optElevenLabsURL), "/")
	cfg.NotifyURL = l.str(optNotifyURL)
	if err := checkHTTPURL(optElevenLabsURL, cfg.ElevenLabsURL); err != nil {
		return err
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
// process, and nothing that inspects this process later, can read them (R1 H1).
var secretEnv = []string{
	"CROWSNEST_PASSWORD", "CROWSNEST_PASSWORD_FILE", "ELEVENLABS_API_KEY",
	"CROWSNEST_NO_AUTH", "SUPPRESS_AUTH", "CROWSNEST_I_KNOW_THIS_IS_OPEN",
}

// UnsetSecretEnv removes the secret and auth-switch variables from this process's environment.
// Call it once the config has been loaded.
func UnsetSecretEnv() {
	for _, name := range secretEnv {
		_ = os.Unsetenv(name)
	}
}
