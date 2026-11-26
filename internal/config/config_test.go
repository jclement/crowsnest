// Tests for precedence, parsing, bind resolution and every startup refusal. The environment,
// interfaces, hostname and home directory are all injected, so nothing here reads the machine.

package config

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// fakeInterfaces is a machine with loopback, a LAN address with an IPv6 link-local, and a
// Tailscale interface.
func fakeInterfaces() ([]Interface, error) {
	return []Interface{
		{Name: "lo0", Addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}},
		{Name: "en0", Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.20"), netip.MustParseAddr("fe80::1")}},
		{Name: "utun4", Addrs: []netip.Addr{netip.MustParseAddr("100.101.102.103"), netip.MustParseAddr("fd7a:115c:a1e0::1")}},
		{Name: "empty0"},
	}, nil
}

func noTailnetInterfaces() ([]Interface, error) {
	all, _ := fakeInterfaces()
	return all[:2], nil
}

// harness runs Load with injected flags, environment and machine.
type harness struct {
	t          *testing.T
	args       []string
	env        map[string]string
	interfaces InterfaceLister
	home       string
}

func newHarness(t *testing.T, args ...string) *harness {
	t.Helper()
	return &harness{t: t, args: args, env: map[string]string{}, interfaces: fakeInterfaces, home: t.TempDir()}
}

func (h *harness) withEnv(pairs ...string) *harness {
	for i := 0; i+1 < len(pairs); i += 2 {
		h.env[pairs[i]] = pairs[i+1]
	}
	return h
}

// writeConfig writes ~/.config/crowsnest/config.yaml in the fake home.
func (h *harness) writeConfig(yaml string) *harness {
	h.t.Helper()
	dir := filepath.Join(h.home, ".config", "crowsnest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o600); err != nil {
		h.t.Fatal(err)
	}
	return h
}

func (h *harness) options() Options {
	return Options{
		Getenv: func(key string) (string, bool) {
			value, ok := h.env[key]
			return value, ok
		},
		Interfaces: h.interfaces,
		Hostname:   func() (string, error) { return "quark.example.lan", nil },
		HomeDir:    func() (string, error) { return h.home, nil },
	}
}

func (h *harness) flags() *pflag.FlagSet {
	h.t.Helper()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	if err := flags.Parse(h.args); err != nil {
		h.t.Fatalf("parsing %v: %v", h.args, err)
	}
	return flags
}

func (h *harness) load() (Config, error) { return Load(h.flags(), h.options()) }

func (h *harness) serve() (Config, error) { return LoadForServe(h.flags(), h.options()) }

func (h *harness) mustLoad() Config {
	h.t.Helper()
	cfg, err := h.load()
	if err != nil {
		h.t.Fatalf("Load: %v", err)
	}
	return cfg
}

// ---- Defaults and precedence -------------------------------------------------

func TestDefaults(t *testing.T) {
	h := newHarness(t)
	cfg := h.mustLoad()
	if cfg.Port != DefaultPort || cfg.Session != DefaultSession || cfg.PermissionMode != "auto" {
		t.Errorf("defaults: port=%d session=%q mode=%q", cfg.Port, cfg.Session, cfg.PermissionMode)
	}
	if cfg.PokeInterval != 15*time.Minute {
		t.Errorf("poke interval = %v", cfg.PokeInterval)
	}
	wantData := filepath.Join(h.home, ".local/share/crowsnest")
	if cfg.DataDir != wantData || cfg.Dir != filepath.Join(wantData, "orchestrator") {
		t.Errorf("data dir %q, dir %q", cfg.DataDir, cfg.Dir)
	}
	if want := filepath.Join(h.home, ".local/state/crowsnest/crowsnest"); cfg.StateDir != want {
		t.Errorf("state dir %q, want %q", cfg.StateDir, want)
	}
	if cfg.DatabasePath() != filepath.Join(wantData, "crowsnest.db") {
		t.Errorf("database path %q", cfg.DatabasePath())
	}
	want := []string{"127.0.0.1:2769", "[::1]:2769"}
	if got := listenerStrings(cfg); !slices.Equal(got, want) {
		t.Errorf("listeners %v, want %v", got, want)
	}
	if !cfg.Listeners[1].Optional || cfg.Listeners[0].Optional {
		t.Errorf("only ::1 from localhost should be optional: %+v", cfg.Listeners)
	}
	if cfg.ElevenLabsVoice != DefaultElevenLabsVoice || cfg.ElevenLabsURL != DefaultElevenLabsURL {
		t.Errorf("elevenlabs defaults: %q %q", cfg.ElevenLabsVoice, cfg.ElevenLabsURL)
	}
	if cfg.ConfigFile != "" {
		t.Errorf("no config file should be read, got %q", cfg.ConfigFile)
	}
}

func TestPrecedence(t *testing.T) {
	const file = "port: 1111\nsession: from-file\nmodel: file-model\n"
	tests := []struct {
		name      string
		args      []string
		env       []string
		file      string
		wantPort  int
		wantModel string
	}{
		{name: "default", wantPort: DefaultPort},
		{name: "file beats default", file: file, wantPort: 1111, wantModel: "file-model"},
		{name: "env beats file", file: file, env: []string{"CROWSNEST_PORT", "2222"}, wantPort: 2222, wantModel: "file-model"},
		{name: "flag beats env", file: file, env: []string{"CROWSNEST_PORT", "2222"}, args: []string{"--port", "3333"}, wantPort: 3333, wantModel: "file-model"},
		{name: "empty env falls through", file: file, env: []string{"CROWSNEST_PORT", ""}, wantPort: 1111, wantModel: "file-model"},
		{name: "flag at its default value still wins", env: []string{"CROWSNEST_PORT", "2222"}, args: []string{"--port", "2769"}, wantPort: 2769},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.args...).withEnv(tc.env...)
			if tc.file != "" {
				h.writeConfig(tc.file)
			}
			cfg := h.mustLoad()
			if cfg.Port != tc.wantPort || cfg.Model != tc.wantModel {
				t.Errorf("port=%d model=%q, want %d %q", cfg.Port, cfg.Model, tc.wantPort, tc.wantModel)
			}
		})
	}
}

func TestEveryOptionReadsItsEnvVar(t *testing.T) {
	h := newHarness(t).withEnv(
		"CROWSNEST_USERNAME", "jeff", "CROWSNEST_PASSWORD", " s3cret with spaces ",
		"CROWSNEST_BIND", "127.0.0.1:9000, tailnet", "CROWSNEST_PORT", "4000",
		"CROWSNEST_SESSION", "lab", "CROWSNEST_DIR", "~/work", "CROWSNEST_MODEL", "haiku",
		"CROWSNEST_PERMISSION_MODE", "plan", "CROWSNEST_POKE_INTERVAL", "0",
		"CROWSNEST_NO_ORCHESTRATOR", "true", "CROWSNEST_ALLOWED_HOSTS", "Crow.Example.ts.net,proxy.lan",
		"ELEVENLABS_API_KEY", "el-key", "CROWSNEST_ELEVENLABS_VOICE", "voice1",
		"CROWSNEST_ELEVENLABS_URL", "http://127.0.0.1:9999/", "CROWSNEST_NOTIFY_URL", "https://ntfy.sh/x",
		"CROWSNEST_DEMO", "1", "CROWSNEST_DATA_DIR", "/var/lib/crow", "CROWSNEST_WEB_DEV", "yes",
		"CROWSNEST_ALLOW_DEFAULT_SESSION", "on", "CROWSNEST_I_KNOW_THIS_IS_OPEN", "true",
	)
	cfg := h.mustLoad()
	checks := []struct {
		name string
		ok   bool
	}{
		{"username", cfg.Username == "jeff"},
		{"password keeps spaces", cfg.Password == " s3cret with spaces "},
		{"binds split on commas", slices.Equal(cfg.Binds, []string{"127.0.0.1:9000", "tailnet"})},
		{"listeners", slices.Equal(listenerStrings(cfg), []string{"127.0.0.1:9000", "100.101.102.103:4000", "[fd7a:115c:a1e0::1]:4000"})},
		{"session", cfg.Session == "lab"},
		{"dir expands ~", cfg.Dir == filepath.Join(h.home, "work")},
		{"model", cfg.Model == "haiku"},
		{"permission mode", cfg.PermissionMode == "plan"},
		{"poke 0 disables", cfg.PokeInterval == 0},
		{"no orchestrator", cfg.NoOrchestrator},
		{"allowed hosts lowercased", slices.Equal(cfg.AllowedHosts, []string{"crow.example.ts.net", "proxy.lan"})},
		{"elevenlabs key", cfg.ElevenLabsAPIKey == "el-key" && cfg.VoiceEnabled()},
		{"voice", cfg.ElevenLabsVoice == "voice1"},
		{"elevenlabs url trimmed", cfg.ElevenLabsURL == "http://127.0.0.1:9999"},
		{"notify url", cfg.NotifyURL == "https://ntfy.sh/x"},
		{"demo", cfg.Demo},
		{"data dir", cfg.DataDir == "/var/lib/crow"},
		{"web dev", cfg.WebDev},
		{"allow default session", cfg.AllowDefaultSession},
		{"i know this is open", cfg.IKnowThisIsOpen},
	}
	for _, check := range checks {
		if !check.ok {
			t.Errorf("%s: not as expected (cfg=%+v)", check.name, cfg)
		}
	}
}

func TestSuppressAuthIsHonoured(t *testing.T) {
	cfg := newHarness(t).withEnv("SUPPRESS_AUTH", "true").mustLoad()
	if !cfg.NoAuth {
		t.Fatal("SUPPRESS_AUTH=true should mean --no-auth")
	}
	cfg = newHarness(t).withEnv("CROWSNEST_NO_AUTH", "false", "SUPPRESS_AUTH", "true").mustLoad()
	if cfg.NoAuth {
		t.Fatal("CROWSNEST_NO_AUTH is listed first and should win over SUPPRESS_AUTH")
	}
}

func TestConfigFile(t *testing.T) {
	t.Run("lists and bools from yaml", func(t *testing.T) {
		cfg := newHarness(t).writeConfig("bind: [localhost, 10.0.0.5]\nallowed-host: crow.lan\nno-orchestrator: true\npoke-interval: 1h\nusername:\n").mustLoad()
		if !slices.Equal(cfg.Binds, []string{"localhost", "10.0.0.5"}) || !cfg.NoOrchestrator || cfg.PokeInterval != time.Hour {
			t.Errorf("got binds=%v noorch=%v poke=%v", cfg.Binds, cfg.NoOrchestrator, cfg.PokeInterval)
		}
		if !slices.Equal(cfg.AllowedHosts, []string{"crow.lan"}) || cfg.Username != "" {
			t.Errorf("allowed=%v username=%q", cfg.AllowedHosts, cfg.Username)
		}
	})
	t.Run("unknown key is an error", func(t *testing.T) {
		_, err := newHarness(t).writeConfig("pasword: x\n").load()
		assertErrorContains(t, err, `unknown key "pasword"`)
	})
	t.Run("explicit missing file is an error", func(t *testing.T) {
		_, err := newHarness(t, "--config", "/nonexistent/crow.yaml").load()
		assertErrorContains(t, err, "/nonexistent/crow.yaml")
	})
	t.Run("explicit file via env", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.yaml")
		if err := os.WriteFile(path, []byte("session: via-env-file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := newHarness(t).withEnv("CROWSNEST_CONFIG", path).mustLoad()
		if cfg.Session != "via-env-file" || cfg.ConfigFile != path {
			t.Errorf("session %q file %q", cfg.Session, cfg.ConfigFile)
		}
	})
	t.Run("XDG_CONFIG_HOME is respected", func(t *testing.T) {
		xdg := t.TempDir()
		if err := os.MkdirAll(filepath.Join(xdg, "crowsnest"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(xdg, "crowsnest", "config.yaml"), []byte("session: xdg\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := newHarness(t).withEnv("XDG_CONFIG_HOME", xdg).mustLoad()
		if cfg.Session != "xdg" {
			t.Errorf("session %q", cfg.Session)
		}
	})
	t.Run("malformed yaml is an error", func(t *testing.T) {
		_, err := newHarness(t).writeConfig("bind: [unclosed\n").load()
		assertErrorContains(t, err, "config file")
	})
}

func TestPasswordFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := write("good", "hunter2 two\r\nsecond line ignored\n")
	empty := write("empty", "\nsecond\n")
	tests := []struct {
		name    string
		args    []string
		env     []string
		want    string
		wantErr string
	}{
		{name: "first line, CRLF stripped", args: []string{"--username", "u", "--password-file", good}, want: "hunter2 two"},
		{name: "via env", env: []string{"CROWSNEST_USERNAME", "u", "CROWSNEST_PASSWORD_FILE", good}, want: "hunter2 two"},
		{name: "empty first line", args: []string{"--username", "u", "--password-file", empty}, wantErr: "first line is empty"},
		{name: "missing file", args: []string{"--username", "u", "--password-file", filepath.Join(dir, "nope")}, wantErr: "reading --password-file"},
		{name: "both password and file", args: []string{"--username", "u", "--password-file", good}, env: []string{"CROWSNEST_PASSWORD", "x"}, wantErr: "pass only one"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := newHarness(t, tc.args...).withEnv(tc.env...).load()
			if tc.wantErr != "" {
				assertErrorContains(t, err, tc.wantErr)
				return
			}
			if err != nil || cfg.Password != tc.want {
				t.Fatalf("password %q err %v", cfg.Password, err)
			}
		})
	}
}

// ---- Validation errors -------------------------------------------------------

func TestInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     []string
		wantErr string
	}{
		{name: "port not a number (env)", env: []string{"CROWSNEST_PORT", "abc"}, wantErr: "CROWSNEST_PORT"},
		{name: "port out of range", args: []string{"--port", "70000"}, wantErr: "1 to 65535"},
		{name: "port zero", args: []string{"--port", "0"}, wantErr: "1 to 65535"},
		{name: "bool garbage", env: []string{"CROWSNEST_DEMO", "maybe"}, wantErr: "not true or false"},
		{name: "duration bare number", args: []string{"--poke-interval", "15"}, wantErr: "not a duration"},
		{name: "duration negative", args: []string{"--poke-interval", "-1m"}, wantErr: "negative"},
		{name: "permission mode", args: []string{"--permission-mode", "yolo"}, wantErr: "not one of"},
		{name: "session name with slash", args: []string{"--session", "../etc"}, wantErr: "not a valid herdr session name"},
		{name: "username without password", args: []string{"--username", "jeff"}, wantErr: "together"},
		{name: "password without username", env: []string{"CROWSNEST_PASSWORD", "x"}, wantErr: "together"},
		{name: "no-auth with credentials", args: []string{"--no-auth", "--username", "u"}, env: []string{"CROWSNEST_PASSWORD", "p"}, wantErr: "not both"},
		{name: "tls cert without key", args: []string{"--tls-cert", "/tmp/c.pem"}, wantErr: "together"},
		{name: "tls files missing", args: []string{"--tls-cert", "/nonexistent/c.pem", "--tls-key", "/nonexistent/k.pem"}, wantErr: "TLS file"},
		{name: "notify url not http", args: []string{"--notify-url", "ftp://x"}, wantErr: "not an http(s) URL"},
		{name: "notify url no host", args: []string{"--notify-url", "https:///path"}, wantErr: "not an http(s) URL"},
		{name: "elevenlabs url garbage", env: []string{"CROWSNEST_ELEVENLABS_URL", "elevenlabs"}, wantErr: "not an http(s) URL"},
		{name: "empty bind", args: []string{"--bind", ""}, wantErr: "--bind is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newHarness(t, tc.args...).withEnv(tc.env...).load()
			assertErrorContains(t, err, tc.wantErr)
		})
	}
}

// ---- Bind resolution ---------------------------------------------------------

func TestBindResolution(t *testing.T) {
	tests := []struct {
		name       string
		binds      []string
		interfaces InterfaceLister
		want       []string
		wantErr    string
	}{
		{name: "localhost", binds: []string{"localhost"}, want: []string{"127.0.0.1:2769", "[::1]:2769"}},
		{name: "localhost is case-insensitive", binds: []string{"LocalHost"}, want: []string{"127.0.0.1:2769", "[::1]:2769"}},
		{name: "tailnet v4 and v6", binds: []string{"tailnet"}, want: []string{"100.101.102.103:2769", "[fd7a:115c:a1e0::1]:2769"}},
		{name: "tailnet absent refuses", binds: []string{"tailnet"}, interfaces: noTailnetInterfaces, wantErr: "no Tailscale address"},
		{name: "interface name skips link-local", binds: []string{"en0"}, want: []string{"192.168.1.20:2769"}},
		{name: "interface without address", binds: []string{"empty0"}, wantErr: "no usable address"},
		{name: "ipv4", binds: []string{"10.1.2.3"}, want: []string{"10.1.2.3:2769"}},
		{name: "ipv6", binds: []string{"::1"}, want: []string{"[::1]:2769"}},
		{name: "ip:port keeps its port", binds: []string{"127.0.0.1:8080"}, want: []string{"127.0.0.1:8080"}},
		{name: "[v6]:port", binds: []string{"[::1]:8080"}, want: []string{"[::1]:8080"}},
		{name: "ip:0 refused", binds: []string{"127.0.0.1:0"}, wantErr: "port 0"},
		{name: "unspecified when typed", binds: []string{"0.0.0.0"}, want: []string{"0.0.0.0:2769"}},
		{name: "repeatable and de-duplicated", binds: []string{"localhost", "127.0.0.1", "tailnet"}, want: []string{"127.0.0.1:2769", "[::1]:2769", "100.101.102.103:2769", "[fd7a:115c:a1e0::1]:2769"}},
		{name: "unknown hostname refused", binds: []string{"example.com"}, wantErr: "not localhost, tailnet"},
		{name: "unknown interface refused", binds: []string{"wlan9"}, wantErr: "not localhost, tailnet"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var args []string
			for _, bind := range tc.binds {
				args = append(args, "--bind", bind)
			}
			h := newHarness(t, args...)
			if tc.interfaces != nil {
				h.interfaces = tc.interfaces
			}
			cfg, err := h.load()
			if tc.wantErr != "" {
				assertErrorContains(t, err, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := listenerStrings(cfg); !slices.Equal(got, tc.want) {
				t.Errorf("listeners %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBindFlagAcceptsCommaList(t *testing.T) {
	cfg := newHarness(t, "--bind", "localhost,tailnet").mustLoad()
	if len(cfg.Listeners) != 4 {
		t.Errorf("listeners %v", listenerStrings(cfg))
	}
}

func TestHostNames(t *testing.T) {
	t.Run("specific binds", func(t *testing.T) {
		cfg := newHarness(t, "--bind", "tailnet", "--allowed-host", "Crow.Tail1234.ts.net.").mustLoad()
		for _, want := range []string{"localhost", "127.0.0.1", "::1", "100.101.102.103", "fd7a:115c:a1e0::1",
			"quark.example.lan", "quark", "quark.local", "crow.tail1234.ts.net"} {
			if !slices.Contains(cfg.HostNames, want) {
				t.Errorf("host names %v missing %q", cfg.HostNames, want)
			}
		}
		if slices.Contains(cfg.HostNames, "192.168.1.20") {
			t.Errorf("an unbound LAN IP must not be an allowed host: %v", cfg.HostNames)
		}
	})
	t.Run("loopback-only bind leaves the machine names out", func(t *testing.T) {
		// R1 I4: on loopback the machine's names add only DNS-rebinding surface.
		cfg := newHarness(t).mustLoad()
		for _, name := range []string{"quark", "quark.local", "quark.example.lan"} {
			if slices.Contains(cfg.HostNames, name) {
				t.Errorf("host names %v include %q on a loopback-only bind", cfg.HostNames, name)
			}
		}
		if !slices.Contains(cfg.HostNames, "localhost") {
			t.Errorf("host names %v lost localhost", cfg.HostNames)
		}
	})
	t.Run("unspecified bind allows every interface IP", func(t *testing.T) {
		cfg := newHarness(t, "--bind", "0.0.0.0").mustLoad()
		if !slices.Contains(cfg.HostNames, "192.168.1.20") || slices.Contains(cfg.HostNames, "0.0.0.0") {
			t.Errorf("host names %v", cfg.HostNames)
		}
	})
}

// ---- Startup refusals --------------------------------------------------------

func TestStartupRefusals(t *testing.T) {
	creds := []string{"CROWSNEST_USERNAME", "jeff", "CROWSNEST_PASSWORD", "pw"}
	tests := []struct {
		name       string
		args       []string
		env        []string
		interfaces InterfaceLister
		wantErr    string // empty means it must start
	}{
		{name: "no credentials and no --no-auth", wantErr: "--no-auth"},
		{name: "credentials start", env: creds},
		{name: "--no-auth on localhost starts", args: []string{"--no-auth"}},
		{name: "--no-auth on tailnet starts", args: []string{"--no-auth", "--bind", "tailnet"}},
		{name: "--no-auth on a LAN IP refuses", args: []string{"--no-auth", "--bind", "192.168.1.20"}, wantErr: "--i-know-this-is-open"},
		{name: "--no-auth on an interface refuses", args: []string{"--no-auth", "--bind", "en0"}, wantErr: "--i-know-this-is-open"},
		{name: "--no-auth on 0.0.0.0 refuses", args: []string{"--no-auth", "--bind", "0.0.0.0"}, wantErr: "--i-know-this-is-open"},
		{name: "--no-auth on :: refuses", args: []string{"--no-auth", "--bind", "::"}, wantErr: "--i-know-this-is-open"},
		{name: "--no-auth open with the override starts", args: []string{"--no-auth", "--bind", "0.0.0.0", "--i-know-this-is-open"}},
		{name: "credentials on 0.0.0.0 start", args: []string{"--bind", "0.0.0.0"}, env: creds},
		{name: "default session refuses", args: []string{"--session", "default"}, env: creds, wantErr: "--allow-default-session"},
		{name: "default session with override starts", args: []string{"--session", "default", "--allow-default-session"}, env: creds},
		{name: "tailnet with no Tailscale refuses", args: []string{"--bind", "tailnet"}, env: creds, interfaces: noTailnetInterfaces, wantErr: "--bind localhost"},
		// R1 L4: the generic SUPPRESS_AUTH turns auth off on loopback only.
		{name: "SUPPRESS_AUTH on localhost starts", env: []string{"SUPPRESS_AUTH", "true"}},
		{name: "SUPPRESS_AUTH on tailnet refuses", args: []string{"--bind", "tailnet"}, env: []string{"SUPPRESS_AUTH", "true"}, wantErr: "CROWSNEST_NO_AUTH"},
		{name: "SUPPRESS_AUTH open refuses even with the override", args: []string{"--bind", "0.0.0.0", "--i-know-this-is-open"}, env: []string{"SUPPRESS_AUTH", "true"}, wantErr: "CROWSNEST_NO_AUTH"},
		{name: "CROWSNEST_NO_AUTH on tailnet starts", args: []string{"--bind", "tailnet"}, env: []string{"CROWSNEST_NO_AUTH", "true"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.args...).withEnv(tc.env...)
			if tc.interfaces != nil {
				h.interfaces = tc.interfaces
			}
			_, err := h.serve()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("should start, got %v", err)
				}
				return
			}
			assertErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRefusalsAreMarked(t *testing.T) {
	_, err := newHarness(t).serve()
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("expected ErrRefused, got %v", err)
	}
}

func TestLogValueHidesSecrets(t *testing.T) {
	cfg := newHarness(t).withEnv("CROWSNEST_USERNAME", "jeff", "CROWSNEST_PASSWORD", "topsecret", "ELEVENLABS_API_KEY", "el-secret").mustLoad()
	rendered := cfg.LogValue().String()
	for _, secret := range []string{"topsecret", "el-secret"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("LogValue leaks %q: %s", secret, rendered)
		}
	}
}

func TestFlagTableCoversVisibleOptions(t *testing.T) {
	rows := FlagTable()
	for _, row := range rows {
		if row[0] == "--web-dev" {
			t.Error("hidden options should not be in the table")
		}
	}
	if len(rows) != len(options)-1 {
		t.Errorf("table has %d rows for %d options", len(rows), len(options))
	}
}

// ---- Helpers -----------------------------------------------------------------

func listenerStrings(cfg Config) []string {
	out := make([]string, 0, len(cfg.Listeners))
	for _, listener := range cfg.Listeners {
		out = append(out, listener.Addr.String())
	}
	return out
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

// TestUnsetSecretEnv is R1 H1: once loaded, the secrets and auth switches leave the process
// environment, so nothing started later inherits them.
func TestUnsetSecretEnv(t *testing.T) {
	for _, name := range []string{"CROWSNEST_PASSWORD", "CROWSNEST_PASSWORD_FILE", "ELEVENLABS_API_KEY", "CROWSNEST_NO_AUTH", "SUPPRESS_AUTH", "CROWSNEST_I_KNOW_THIS_IS_OPEN"} {
		t.Setenv(name, "set")
	}
	t.Setenv("CROWSNEST_PORT", "2770")
	UnsetSecretEnv()
	for _, name := range secretEnv {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("%s is still in the environment", name)
		}
	}
	if os.Getenv("CROWSNEST_PORT") != "2770" {
		t.Error("an ordinary setting was removed too")
	}
}
