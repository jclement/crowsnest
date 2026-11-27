// Every check against a fake machine: which binaries exist, what they print, which directories
// are writable, whether the port is free. Nothing here runs a real command or opens a socket.

package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/tailscale"
)

// fakeMachine describes the machine a test wants.
type fakeMachine struct {
	binaries   map[string]string // name -> path
	outputs    map[string]string // "name arg arg" -> output
	failing    map[string]bool   // "name arg arg" -> exits non-zero
	env        []string
	unwritable map[string]bool
	files      map[string]string
	portTaken  bool
	ours       bool // the taken port answers as crowsnest
	herdrUp    bool
	protocol   int
	tailscale  tailscale.Info
	serves     []tailscale.Serve
}

func healthyMachine() *fakeMachine {
	return &fakeMachine{
		binaries: map[string]string{"herdr": "/bin/herdr", "claude": "/bin/claude", "git": "/bin/git"},
		outputs: map[string]string{
			"/bin/herdr --version":           "herdr 0.9.5\n",
			"/bin/claude --version":          "2.1.332 (Claude Code)\n",
			"/bin/claude auth status --json": `{"loggedIn": true, "authMethod": "claude.ai", "email": "someone@example.com"}`,
			"/bin/claude --help":             "  --settings <f>\n  --mcp-config <c>\n --append-system-prompt[-file]\n  --session-id <u>\n  --remote-control [n]\n  --permission-mode <m>\n",
			"/bin/codex --version":           "codex-cli 0.167.0\n",
			"/bin/codex login status":        "Logged in using ChatGPT\n",
		},
		failing:    map[string]bool{},
		unwritable: map[string]bool{},
		files:      map[string]string{"/home/u/.config/herdr/config.toml": "onboarding = false\n"},
		protocol:   herdr.ProtocolVersion,
	}
}

func (m *fakeMachine) withCodex() *fakeMachine { m.binaries["codex"] = "/bin/codex"; return m }

func (m *fakeMachine) without(name string) *fakeMachine { delete(m.binaries, name); return m }

func (m *fakeMachine) envFor() Env {
	return Env{
		GOOS: "darwin", Home: "/home/u",
		LookPath: func(name string) (string, error) {
			if path, ok := m.binaries[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			if m.failing[key] {
				return m.outputs[key], errors.New("exit status 1")
			}
			return m.outputs[key], nil
		},
		Getenv: func(name string) (string, bool) {
			for _, entry := range m.env {
				if key, value, _ := strings.Cut(entry, "="); key == name {
					return value, true
				}
			}
			return "", false
		},
		Environ: func() []string { return m.env },
		Now:     func() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) },
		Listen: func(string) error {
			if m.portTaken {
				return errors.New("bind: address already in use")
			}
			return nil
		},
		HTTPGet: func(context.Context, string, map[string]string) (int, []byte, error) {
			if m.ours {
				return 200, []byte(`{"data":{"status":"ok","version":"v9.9.9","setup_required":true}}`), nil
			}
			return 0, nil, errors.New("refused")
		},
		HerdrSocket: func(context.Context, string) (string, error) { return "/tmp/herdr.sock", nil },
		HerdrPing: func(context.Context, string) (herdr.Pong, error) {
			if !m.herdrUp {
				return herdr.Pong{}, errors.New("no server")
			}
			return herdr.Pong{Version: "0.9.5", Protocol: m.protocol}, nil
		},
		DiskFree: func(string) (uint64, error) { return 50 << 30, nil },
		Writable: func(dir string) bool { return !m.unwritable[dir] },
		Readable: func(string) bool { return true },
		Stat: func(path string) (os.FileInfo, error) {
			if path == "/" || path == "/home" || path == "/home/u" {
				return fakeDir{name: filepath.Base(path)}, nil
			}
			return nil, fs.ErrNotExist
		},
		ReadFile: func(path string) ([]byte, error) {
			if text, ok := m.files[path]; ok {
				return []byte(text), nil
			}
			return nil, fs.ErrNotExist
		},
		InContainer: func() bool { return false },
		Tailscale:   func(context.Context) tailscale.Info { return m.tailscale },
		TailscaleServes: func(context.Context, int) ([]tailscale.Serve, error) {
			return m.serves, nil
		},
	}
}

type fakeDir struct{ name string }

func (d fakeDir) Name() string       { return d.name }
func (d fakeDir) Size() int64        { return 0 }
func (d fakeDir) Mode() fs.FileMode  { return fs.ModeDir | 0o700 }
func (d fakeDir) ModTime() time.Time { return time.Time{} }
func (d fakeDir) IsDir() bool        { return true }
func (d fakeDir) Sys() any           { return nil }

func testConfig() config.Config {
	return config.Config{
		Session: "crowsnest", DataDir: "/home/u/.local/share/crowsnest", StateDir: "/home/u/.local/state/crowsnest/crowsnest",
		Dir: "/home/u/.local/share/crowsnest/orchestrator", Port: 2769, Binds: []string{"localhost"}, Engine: "auto",
		Listeners:    []config.Listener{{Addr: netip.MustParseAddrPort("127.0.0.1:2769"), Source: "localhost"}},
		Sources:      map[string]string{"port": "default", "poke-interval": "CROWSNEST_POKE_INTERVAL", "notify-url": "database"},
		PokeInterval: 5 * time.Minute, NotifyURL: "https://ntfy.sh/secret-topic",
		FreshAt: -1,
	}
}

func run(t *testing.T, machine *fakeMachine, mutate ...func(*Inputs)) Report {
	t.Helper()
	inputs := Inputs{Config: testConfig(), Version: "v1.0.0", AgentSocket: "/home/u/.local/state/crowsnest/crowsnest/agent.sock", Env: machine.envFor()}
	for _, fn := range mutate {
		fn(&inputs)
	}
	return Run(context.Background(), inputs)
}

func requireCheck(t *testing.T, report Report, id string, want Status) Check {
	t.Helper()
	check, ok := report.Find(id)
	if !ok {
		t.Fatalf("no check %s in %+v", id, report.Checks)
	}
	if check.Status != want {
		t.Fatalf("%s: status %s, want %s (detail %q)", id, check.Status, want, check.Detail)
	}
	return check
}

func TestHealthyMachine(t *testing.T) {
	report := run(t, healthyMachine())
	if report.Verdict == StatusFail {
		for _, check := range report.Checks {
			if check.Status == StatusFail {
				t.Errorf("unexpected failure %s: %s", check.ID, check.Detail)
			}
		}
	}
	requireCheck(t, report, "herdr.installed", StatusOK)
	requireCheck(t, report, "herdr.session", StatusOK)
	requireCheck(t, report, "agent.claude", StatusOK)
	requireCheck(t, report, "claude.flags", StatusOK)
	requireCheck(t, report, "machine.port", StatusOK)
	if report.Selection.Orchestrator != "claude" {
		t.Errorf("selection %+v", report.Selection)
	}
	if !strings.Contains(report.Summary, "herdr 0.9.5 ✓") || !strings.Contains(report.Summary, "claude 2.1.332 ✓ signed in") || !strings.Contains(report.Summary, "codex not found") {
		t.Errorf("summary %q", report.Summary)
	}
}

func TestHerdrMissing(t *testing.T) {
	report := run(t, healthyMachine().without("herdr"))
	check := requireCheck(t, report, "herdr.installed", StatusFail)
	if check.Command != "brew install herdr" || !strings.Contains(check.Fix, "herdr.dev/install.sh") {
		t.Errorf("install advice %+v", check)
	}
	if report.Verdict != StatusFail {
		t.Error("verdict should fail")
	}
	machine := healthyMachine().without("herdr")
	linux := machine.envFor()
	linux.GOOS = "linux"
	report = Run(context.Background(), Inputs{Config: testConfig(), Env: linux})
	if check, _ := report.Find("herdr.installed"); !strings.HasPrefix(check.Command, "curl -fsSL https://herdr.dev/install.sh") {
		t.Errorf("linux install command %q", check.Command)
	}
}

func TestHerdrTooOld(t *testing.T) {
	machine := healthyMachine()
	machine.outputs["/bin/herdr --version"] = "herdr 0.8.2"
	check := requireCheck(t, run(t, machine), "herdr.installed", StatusFail)
	if !strings.Contains(check.Detail, MinHerdrVersion) || check.Command != "herdr update" {
		t.Errorf("%+v", check)
	}
}

func TestHerdrRunningAndProtocolMismatch(t *testing.T) {
	machine := healthyMachine()
	machine.herdrUp = true
	check := requireCheck(t, run(t, machine), "herdr.session", StatusOK)
	if !strings.Contains(check.Detail, "running") {
		t.Errorf("%+v", check)
	}
	machine.protocol = 21
	requireCheck(t, run(t, machine), "herdr.session", StatusWarn)
}

func TestHerdrNotRunningIsNotStarted(t *testing.T) {
	check := requireCheck(t, run(t, healthyMachine()), "herdr.session", StatusOK)
	if !strings.Contains(check.Detail, "crowsnest will start it") {
		t.Errorf("%q", check.Detail)
	}
}

func TestHerdrOnboarding(t *testing.T) {
	machine := healthyMachine()
	machine.files = map[string]string{}
	requireCheck(t, run(t, machine), "herdr.onboarding", StatusInfo)
}

func TestClaudeAbsentNoAgent(t *testing.T) {
	report := run(t, healthyMachine().without("claude"))
	requireCheck(t, report, "agent.claude", StatusFail)
	requireCheck(t, report, "agent.selection", StatusFail)
	if _, ok := report.Find("claude.flags"); ok {
		t.Error("claude checks ran without claude")
	}
}

func TestClaudeNotSignedIn(t *testing.T) {
	machine := healthyMachine()
	machine.outputs["/bin/claude auth status --json"] = `{"loggedIn": false}`
	machine.failing["/bin/claude auth status --json"] = true
	report := run(t, machine)
	check := requireCheck(t, report, "agent.claude", StatusFail)
	if check.Command != "claude" {
		t.Errorf("sign-in command %q", check.Command)
	}
	if !strings.Contains(report.Summary, "not signed in") {
		t.Errorf("summary %q", report.Summary)
	}
}

func TestCodexOnly(t *testing.T) {
	report := run(t, healthyMachine().without("claude").withCodex())
	if report.Selection.Orchestrator != "codex" || report.Selection.Jobs != "codex" {
		t.Fatalf("selection %+v", report.Selection)
	}
	requireCheck(t, report, "agent.selection", StatusWarn)
	requireCheck(t, report, "agent.codex", StatusOK)
	requireCheck(t, report, "agent.claude", StatusWarn)
	if report.Verdict == StatusFail {
		t.Error("codex only is a working setup, not a failure")
	}
}

func TestEngineSettingRespected(t *testing.T) {
	report := run(t, healthyMachine().withCodex(), func(in *Inputs) { in.Config.Engine = "codex" })
	if report.Selection.Orchestrator != "codex" {
		t.Errorf("selection %+v", report.Selection)
	}
}

func TestClaudeOldOrMissingFlags(t *testing.T) {
	machine := healthyMachine()
	machine.outputs["/bin/claude --version"] = "2.0.10 (Claude Code)"
	machine.outputs["/bin/claude --help"] = "  --settings <f>\n"
	report := run(t, machine)
	requireCheck(t, report, "claude.hooks", StatusFail)
	check := requireCheck(t, report, "claude.flags", StatusFail)
	if !strings.Contains(check.Detail, "--mcp-config") {
		t.Errorf("%q", check.Detail)
	}
}

func TestHelpHasFlag(t *testing.T) {
	help := "  --settings-file x\n  --append-system-prompt[-file]\n  --session-id <u>"
	cases := map[string]bool{"--settings": false, "--session-id": true, "--append-system-prompt-file": true, "--mcp-config": false}
	for flag, want := range cases {
		if got := HelpHasFlag(help, flag); got != want {
			t.Errorf("HelpHasFlag(%s) = %v", flag, got)
		}
	}
}

func TestTrust(t *testing.T) {
	machine := healthyMachine()
	requireCheck(t, run(t, machine), "claude.trust", StatusInfo)
	machine.files["/home/u/.claude.json"] = `{"projects":{"/home/u/.local/share/crowsnest":{"hasTrustDialogAccepted":true}}}`
	requireCheck(t, run(t, machine), "claude.trust", StatusOK)
}

func TestUnwritableDirectory(t *testing.T) {
	machine := healthyMachine()
	machine.unwritable["/home/u"] = true
	check := requireCheck(t, run(t, machine), "machine.data_dir", StatusFail)
	if check.Fix == "" {
		t.Error("no fix")
	}
}

func TestLongSocketPath(t *testing.T) {
	long := "/" + strings.Repeat("x", 120) + "/agent.sock"
	check := requireCheck(t, run(t, healthyMachine(), func(in *Inputs) { in.AgentSocket = long }), "machine.socket", StatusFail)
	if !strings.Contains(check.Fix, "--session") {
		t.Errorf("%q", check.Fix)
	}
}

func TestPortInUse(t *testing.T) {
	machine := healthyMachine()
	machine.portTaken = true
	check := requireCheck(t, run(t, machine), "machine.port", StatusFail)
	if !strings.Contains(check.Fix, "--port 2770") {
		t.Errorf("fix %q", check.Fix)
	}
	machine.ours = true
	report := run(t, machine)
	requireCheck(t, report, "machine.port", StatusOK)
	if report.Live == nil || !report.Live.SetupRequired || report.Live.Version != "v9.9.9" {
		t.Errorf("live %+v", report.Live)
	}
}

func TestTypoEnvVars(t *testing.T) {
	machine := healthyMachine()
	machine.env = []string{"CROWSNEST_POKE_INTERVALL=5m", "CROWSNEST_SOCKET=/x", "CROWSNEST_PORT=1", "CROWSNEST_ZZZZZZZZZZZZZZ=1"}
	check := requireCheck(t, run(t, machine), "config.env", StatusWarn)
	if !strings.Contains(check.Detail, "CROWSNEST_POKE_INTERVALL (did you mean CROWSNEST_POKE_INTERVAL?)") || strings.Contains(check.Detail, "CROWSNEST_SOCKET") || strings.Contains(check.Detail, "CROWSNEST_PORT,") {
		t.Errorf("%q", check.Detail)
	}
}

func TestConfigLoadError(t *testing.T) {
	report := run(t, healthyMachine(), func(in *Inputs) {
		in.LoadErr = errors.New("--port 0 is out of range")
	})
	requireCheck(t, report, "config.load", StatusFail)
	if _, ok := report.Find("machine.port"); ok {
		t.Error("config-dependent checks ran without a config")
	}
	requireCheck(t, report, "herdr.installed", StatusOK)
}

func TestStartupRefusalReported(t *testing.T) {
	requireCheck(t, run(t, healthyMachine(), func(in *Inputs) { in.Config.Session = "default" }), "config.startup", StatusFail)
}

func TestSourcesRedactSecrets(t *testing.T) {
	report := run(t, healthyMachine())
	found := map[string]SettingSource{}
	for _, setting := range report.Settings {
		found[setting.Name] = setting
	}
	if found["poke-interval"].Source != "env" || found["poke-interval"].Value != "5m" {
		t.Errorf("poke-interval %+v", found["poke-interval"])
	}
	if found["notify-url"].Value != "(set)" || found["notify-url"].Source != "database" {
		t.Errorf("notify-url %+v", found["notify-url"])
	}
	if _, ok := found["port"]; ok {
		t.Error("a default appeared in the sources")
	}
}

func TestDeepChecksCost(t *testing.T) {
	machine := healthyMachine()
	report := run(t, machine, func(in *Inputs) { in.Deep = true })
	check := requireCheck(t, report, "deep.claude", StatusFail) // the fake prints nothing for -p
	if check.Cost == "" {
		t.Error("a deep check must say it costs tokens")
	}
	if _, ok := run(t, machine).Find("deep.claude"); ok {
		t.Error("deep checks ran without --deep")
	}
}

func TestJSONShapeAndDiagnosticsRedaction(t *testing.T) {
	report := run(t, healthyMachine())
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	_ = json.Unmarshal(data, &shape)
	for _, key := range []string{"checked_at", "verdict", "summary", "versions", "agents", "selection", "paths", "checks"} {
		if _, ok := shape[key]; !ok {
			t.Errorf("JSON lacks %q", key)
		}
	}
	if strings.Contains(string(data), "someone@example.com") || strings.Contains(string(data), "secret-topic") {
		t.Error("the report carries an email or a secret")
	}
	text := Diagnostics(report, "/home/u")
	if strings.Contains(text, "/home/u") || !strings.Contains(text, "~/.local/share/crowsnest") {
		t.Errorf("home not shortened:\n%s", text)
	}
	if got := RedactText("mail me at a.b@example.org", ""); strings.Contains(got, "example.org") {
		t.Errorf("email kept: %q", got)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"0.9.5", "0.9.5", 0}, {"0.10.0", "0.9.5", 1}, {"0.9", "0.9.5", -1}, {"2.1.332", "2.1.0", 1}, {"v1.2.3-rc1", "1.2.3", 0}}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%s, %s) = %d", tc.a, tc.b, got)
		}
	}
}

func TestWritePlainHasNoEscapes(t *testing.T) {
	var out strings.Builder
	if err := Write(&out, run(t, healthyMachine().without("herdr")), Style{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") || !strings.Contains(out.String(), "FAIL") || !strings.Contains(out.String(), "→ Install herdr") || !strings.Contains(out.String(), "$ brew install herdr") {
		t.Errorf("plain output:\n%s", out.String())
	}
}

func TestTailscaleAbsentIsInformation(t *testing.T) {
	report := run(t, healthyMachine())
	requireCheck(t, report, "tailscale.installed", StatusInfo)
	if _, ok := report.Find("tailscale.serve"); ok {
		t.Error("serve checked without tailscale")
	}
}

func TestTailscaleLoggedOut(t *testing.T) {
	machine := healthyMachine()
	machine.tailscale = tailscale.Info{Installed: true, State: "NeedsLogin"}
	check := requireCheck(t, run(t, machine), "tailscale.installed", StatusWarn)
	if check.Command != "tailscale up" {
		t.Errorf("%+v", check)
	}
}

func TestTailscaleNotServedPrintsTheCommand(t *testing.T) {
	machine := healthyMachine()
	machine.tailscale = tailscale.Info{Installed: true, Running: true, DNSName: "quark.tail1.ts.net", HTTPS: true}
	report := run(t, machine, func(in *Inputs) { in.Config.TailnetName = "quark.tail1.ts.net" })
	requireCheck(t, report, "tailscale.name", StatusOK)
	requireCheck(t, report, "tailscale.https", StatusOK)
	check := requireCheck(t, report, "tailscale.serve", StatusInfo)
	if check.Command != "tailscale serve --bg 2769" || !strings.Contains(check.Detail, "https://quark.tail1.ts.net") {
		t.Errorf("%+v", check)
	}
}

func TestTailscaleServedWithoutTrustedProxyWarns(t *testing.T) {
	machine := healthyMachine()
	machine.tailscale = tailscale.Info{Installed: true, Running: true, DNSName: "quark.tail1.ts.net", HTTPS: false}
	machine.serves = []tailscale.Serve{{URL: "https://quark.tail1.ts.net", Target: "http://127.0.0.1:2769"}}
	report := run(t, machine)
	requireCheck(t, report, "tailscale.name", StatusWarn) // detection was off: not an allowed host
	requireCheck(t, report, "tailscale.https", StatusWarn)
	check := requireCheck(t, report, "tailscale.serve", StatusWarn)
	if !strings.Contains(check.Fix, "CROWSNEST_TRUSTED_PROXY=loopback") {
		t.Errorf("%+v", check)
	}
	trusted := run(t, machine, func(in *Inputs) {
		in.Config.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	})
	requireCheck(t, trusted, "tailscale.serve", StatusOK)
}
