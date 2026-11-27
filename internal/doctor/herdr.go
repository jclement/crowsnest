// herdr: installed, new enough, its session reachable (never started here), and whether its TUI
// will show the first-run screen in the browser's Terminal view.

package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jclement/crowsnest/internal/herdr"
)

// MinHerdrVersion is the oldest herdr crowsnest supports: the first release speaking socket
// protocol 22, which crowsnest is built against (worktree.create with a path, agent.start
// answering launch_pending, the per-pane agent status events). Older servers refuse or misread
// calls crowsnest makes on every start.
const MinHerdrVersion = "0.9.5"

// herdrFacts is what the herdr checks learned, for the summary and the System page.
type herdrFacts struct {
	path    string
	version string
	running bool
}

var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// HerdrInstall is the install advice for goos, from herdr.dev's install page (checked
// 2026-11-26): Homebrew on macOS, the official script elsewhere, mise for either.
func HerdrInstall(goos string) (fix, command string) {
	if goos == "darwin" {
		return "Install herdr with Homebrew (`brew install herdr`), or `curl -fsSL https://herdr.dev/install.sh | sh`, or `mise use -g herdr`. crowsnest picks it up without a restart", "brew install herdr"
	}
	return "Install herdr: `curl -fsSL https://herdr.dev/install.sh | sh` (or `brew install herdr`, or `mise use -g herdr`). crowsnest picks it up without a restart", "curl -fsSL https://herdr.dev/install.sh | sh"
}

func (r *runner) herdrChecks(ctx context.Context) {
	path, err := r.env.LookPath("herdr")
	if err != nil {
		fix, command := HerdrInstall(r.env.GOOS)
		r.add(Check{ID: "herdr.installed", Group: GroupHerdr, Title: "herdr installed", Status: StatusFail,
			Detail: "no herdr on PATH: crowsnest runs every agent inside a herdr session", Fix: fix, Command: command})
		return
	}
	r.herdr.path = path
	r.report.Paths.Herdr = path
	output, err := r.env.Run(ctx, commandTimeout, path, "--version")
	version := versionPattern.FindString(output)
	r.herdr.version = version
	r.report.Versions.Herdr = version
	switch {
	case err != nil || version == "":
		r.add(Check{ID: "herdr.installed", Group: GroupHerdr, Title: "herdr installed", Status: StatusWarn,
			Detail: fmt.Sprintf("found %s, but `herdr --version` gave no version", path), Fix: "Reinstall herdr, or check it runs: `herdr --version`"})
	case CompareVersions(version, MinHerdrVersion) < 0:
		r.add(Check{ID: "herdr.installed", Group: GroupHerdr, Title: "herdr installed", Status: StatusFail,
			Detail: fmt.Sprintf("herdr %s at %s is older than %s, the first release with socket protocol %d that crowsnest speaks", version, path, MinHerdrVersion, herdr.ProtocolVersion),
			Fix:    "Update herdr: `herdr update` (or `brew upgrade herdr`)", Command: "herdr update"})
	default:
		r.add(Check{ID: "herdr.installed", Group: GroupHerdr, Title: "herdr installed", Status: StatusOK,
			Detail: fmt.Sprintf("herdr %s at %s (needs %s or newer)", version, path, MinHerdrVersion)})
	}
	if r.configOK() {
		r.herdrSession(ctx)
	}
	r.herdrOnboarding()
}

// herdrSession pings the configured session's socket. It never starts a server: doctor is
// read-only, and serve does the starting.
func (r *runner) herdrSession(ctx context.Context) {
	session := r.in.Config.Session
	title := fmt.Sprintf("herdr session %q", session)
	socket, err := r.env.HerdrSocket(ctx, session)
	if err != nil {
		r.add(Check{ID: "herdr.session", Group: GroupHerdr, Title: title, Status: StatusWarn,
			Detail: "could not ask herdr where the session lives", Fix: "Check `herdr session list --json` runs"})
		return
	}
	pong, err := r.env.HerdrPing(ctx, socket)
	if err != nil {
		r.add(Check{ID: "herdr.session", Group: GroupHerdr, Title: title, Status: StatusOK,
			Detail: "not running; crowsnest will start it (and never stops it)"})
		return
	}
	r.herdr.running = true
	if pong.Protocol != herdr.ProtocolVersion {
		r.add(Check{ID: "herdr.session", Group: GroupHerdr, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("running herdr %s speaking protocol %d; crowsnest was built for %d", pong.Version, pong.Protocol, herdr.ProtocolVersion),
			Fix:    "Restart that herdr server on the installed version, or update herdr and crowsnest together"})
		return
	}
	r.add(Check{ID: "herdr.session", Group: GroupHerdr, Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("running: herdr %s, protocol %d", pong.Version, pong.Protocol)})
}

// herdrOnboarding reads herdr's config for `onboarding = false`. Without it the first TUI
// opened (the browser's Terminal view) shows herdr's welcome screen, which takes every key
// until Enter and then Esc. It is harmless, and only once, so it is information.
func (r *runner) herdrOnboarding() {
	path := HerdrConfigPath(r.env.Home, r.env.Getenv)
	data, err := r.env.ReadFile(path)
	if err == nil && OnboardingDone(string(data)) {
		r.add(Check{ID: "herdr.onboarding", Group: GroupHerdr, Title: "herdr first-run screen", Status: StatusOK, Detail: "already dismissed"})
		return
	}
	r.add(Check{ID: "herdr.onboarding", Group: GroupHerdr, Title: "herdr first-run screen", Status: StatusInfo,
		Detail: "herdr's TUI has not been opened here yet: the Terminal view will show its welcome screen once",
		Fix:    fmt.Sprintf("Press Enter, then Esc, the first time; or add `onboarding = false` to %s", path)})
}

// HerdrConfigPath is herdr's config.toml: HERDR_CONFIG_PATH's file when set, else
// ~/.config/herdr/config.toml.
func HerdrConfigPath(home string, getenv func(string) (string, bool)) string {
	if value, ok := getenv("HERDR_CONFIG_PATH"); ok && value != "" {
		if strings.HasSuffix(value, ".toml") {
			return value
		}
		return filepath.Join(value, "config.toml")
	}
	return filepath.Join(home, ".config", "herdr", "config.toml")
}

var onboardingPattern = regexp.MustCompile(`(?m)^\s*onboarding\s*=\s*false\b`)

// OnboardingDone reports whether a herdr config.toml records the first-run screen as done.
func OnboardingDone(configText string) bool { return onboardingPattern.MatchString(configText) }

// CompareVersions compares dotted numeric versions: -1, 0 or 1. Missing parts count as 0 and
// anything after the numbers ("-rc1") is ignored.
func CompareVersions(a, b string) int {
	partsA, partsB := versionParts(a), versionParts(b)
	for i := range max(len(partsA), len(partsB)) {
		var x, y int
		if i < len(partsA) {
			x = partsA[i]
		}
		if i < len(partsB) {
			y = partsB[i]
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

func versionParts(version string) []int {
	version = versionPattern.FindString(version)
	var parts []int
	for part := range strings.SplitSeq(version, ".") {
		var value int
		_, _ = fmt.Sscanf(part, "%d", &value)
		parts = append(parts, value)
	}
	return parts
}
