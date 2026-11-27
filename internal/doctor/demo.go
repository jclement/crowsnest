// Demo machines: a scripted Env for --demo, so the System page and the first-run checklist can
// be shown and tested (Playwright, screenshots) in every state without touching the real
// machine. CROWSNEST_DEMO_DOCTOR picks the state at start; in --demo the app can switch it
// (POST /system/demo-state), which is how "herdr missing, then installed" is tested.

package doctor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/tailscale"
)

// Demo states.
const (
	DemoAllGreen        = "ok"
	DemoHerdrMissing    = "herdr-missing"
	DemoClaudeSignedOut = "claude-signed-out"
	DemoCodexOnly       = "codex-only"
	DemoNoAgent         = "no-agent"
	DemoBoth            = "both"
)

// DemoStates lists the states DemoEnv knows.
var DemoStates = []string{DemoAllGreen, DemoHerdrMissing, DemoClaudeSignedOut, DemoCodexOnly, DemoNoAgent, DemoBoth}

// ValidDemoState reports whether state is one of DemoStates.
func ValidDemoState(state string) bool {
	for _, known := range DemoStates {
		if known == state {
			return true
		}
	}
	return false
}

// DemoEnv is a fake machine in the given state. Unknown states are all green. The machine is
// "/home/demo" with herdr 0.9.5, Claude Code 2.1.332 and, in some states, Codex 0.167.0.
func DemoEnv(state string) Env {
	binaries := map[string]string{"herdr": "/opt/homebrew/bin/herdr", "claude": "/usr/local/bin/claude", "git": "/usr/bin/git"}
	signedIn := `{"loggedIn": true, "authMethod": "claude.ai"}`
	switch state {
	case DemoHerdrMissing:
		delete(binaries, "herdr")
	case DemoClaudeSignedOut:
		signedIn = `{"loggedIn": false}`
	case DemoCodexOnly:
		delete(binaries, "claude")
		binaries["codex"] = "/opt/homebrew/bin/codex"
	case DemoNoAgent:
		delete(binaries, "claude")
	case DemoBoth:
		binaries["codex"] = "/opt/homebrew/bin/codex"
	}
	outputs := map[string]string{
		"herdr --version":           "herdr 0.9.5",
		"claude --version":          "2.1.332 (Claude Code)",
		"claude auth status --json": signedIn,
		"claude --help":             "--settings x\n--mcp-config x\n--append-system-prompt[-file]\n--session-id x\n--remote-control x\n--permission-mode x\n",
		"codex --version":           "codex-cli 0.167.0",
		"codex login status":        "Logged in using ChatGPT",
	}
	return Env{
		GOOS: "darwin", Home: "/home/demo",
		LookPath: func(name string) (string, error) {
			if path, ok := binaries[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
			base := name[strings.LastIndex(name, "/")+1:]
			output, ok := outputs[strings.Join(append([]string{base}, args...), " ")]
			if !ok {
				return "", errors.New("demo: no such command")
			}
			return output, nil
		},
		Getenv:  func(string) (string, bool) { return "", false },
		Environ: func() []string { return nil },
		Listen:  func(string) error { return nil },
		HTTPGet: func(context.Context, string, map[string]string) (int, []byte, error) {
			return 0, nil, errors.New("demo")
		},
		HerdrSocket: func(context.Context, string) (string, error) {
			return "/home/demo/.config/herdr/sessions/crowsnest/herdr.sock", nil
		},
		HerdrPing: func(context.Context, string) (herdr.Pong, error) {
			if state == DemoHerdrMissing {
				return herdr.Pong{}, errors.New("not running")
			}
			return herdr.Pong{Version: "0.9.5", Protocol: herdr.ProtocolVersion}, nil
		},
		DiskFree: func(string) (uint64, error) { return 312 << 30, nil },
		Writable: func(string) bool { return true },
		Readable: func(string) bool { return true },
		Stat:     func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist },
		ReadFile: func(path string) ([]byte, error) {
			if strings.HasSuffix(path, "config.toml") {
				return []byte("onboarding = false\n"), nil
			}
			return nil, fs.ErrNotExist
		},
		InContainer: func() bool { return false },
		Tailscale:   func(context.Context) tailscale.Info { return tailscale.Info{} },
		TailscaleServes: func(context.Context, int) ([]tailscale.Serve, error) {
			return nil, nil
		},
	}
}
