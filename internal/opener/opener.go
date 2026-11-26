// Package opener decides whether, and with what, crowsnest opens a browser at start (DESIGN.md
// 3.3): the setup link on a first run, or the app for `crowsnest open`.
//
// The rule (--open auto) is "attempt whenever an opener is plausibly present and working":
//
//   - $BROWSER, when set, is honoured first: whoever set it chose how URLs open here;
//   - devtun's opener: devtun (Jeff's remote-development tunnel) puts an `xdg-open` (and `open`)
//     shim in ~/.devtun/bin on the remote box that opens URLs on the laptop, port rewritten to the
//     tunnel. It counts when it is the one on PATH and its session socket exists, over SSH too;
//   - otherwise the platform opener: macOS `open` (not over SSH: it would open on the remote
//     screen), Linux `xdg-open` with a DISPLAY or WAYLAND_DISPLAY (not over SSH);
//   - never as root, never from a non-interactive start (no terminal: a service, the devbox), and
//     not in a container that has none of the above.
//
// It is best effort: a failure to open is logged and the link is always printed too.
package opener

import (
	"os"
	"path/filepath"
	"strings"
)

// Modes for --open.
const (
	ModeAuto   = "auto"
	ModeAlways = "always"
	ModeNever  = "never"
)

// Env is what the decision looks at. Fields are filled by Detect on the real machine.
type Env struct {
	GOOS        string
	Getenv      func(string) string
	LookPath    func(string) (string, error)
	Stat        func(string) (os.FileInfo, error)
	Home        string
	UID         int
	Interactive bool // stderr is a terminal
	InContainer bool
}

// Decision is what to run, or why not.
type Decision struct {
	Command []string // the opener and its leading arguments; the URL is appended. Nil: do not open.
	Via     string   // "BROWSER", "devtun", "open", "xdg-open"
	Reason  string   // why not, when Command is nil
}

// Decide applies the rule for mode.
func Decide(mode string, env Env) Decision {
	if mode == ModeNever {
		return Decision{Reason: "--open never"}
	}
	if mode != ModeAlways {
		switch {
		case !env.Interactive:
			return Decision{Reason: "not started from a terminal"}
		case env.UID == 0:
			return Decision{Reason: "running as root"}
		}
	}
	if browser := strings.TrimSpace(env.Getenv("BROWSER")); browser != "" {
		first, _, _ := strings.Cut(browser, string(os.PathListSeparator))
		if fields := strings.Fields(strings.ReplaceAll(first, "%s", "")); len(fields) > 0 {
			return Decision{Command: fields, Via: "BROWSER"}
		}
	}
	if command, ok := devtunOpener(env); ok {
		return Decision{Command: command, Via: "devtun"}
	}
	overSSH := env.Getenv("SSH_CONNECTION") != "" || env.Getenv("SSH_TTY") != ""
	if mode != ModeAlways {
		switch {
		case overSSH:
			return Decision{Reason: "over SSH with no $BROWSER and no devtun opener"}
		case env.InContainer:
			return Decision{Reason: "in a container with no opener"}
		}
	}
	switch env.GOOS {
	case "darwin":
		if path, err := env.LookPath("open"); err == nil {
			return Decision{Command: []string{path}, Via: "open"}
		}
	default:
		if mode != ModeAlways && env.Getenv("DISPLAY") == "" && env.Getenv("WAYLAND_DISPLAY") == "" {
			return Decision{Reason: "no graphical session (DISPLAY or WAYLAND_DISPLAY)"}
		}
		if path, err := env.LookPath("xdg-open"); err == nil {
			return Decision{Command: []string{path}, Via: "xdg-open"}
		}
	}
	return Decision{Reason: "no browser opener found"}
}

// devtunOpener finds devtun's xdg-open (or open) shim: the one on PATH lives in ~/.devtun/bin,
// and a devtun session's socket exists (without a session the shim falls through to nothing).
func devtunOpener(env Env) ([]string, bool) {
	dir := filepath.Join(env.Home, ".devtun", "bin")
	for _, name := range []string{"xdg-open", "open"} {
		path, err := env.LookPath(name)
		if err != nil || filepath.Dir(path) != dir {
			continue
		}
		if devtunSession(env) {
			return []string{path}, true
		}
	}
	return nil, false
}

// devtunSession reports whether devtun's control socket exists: DEVTUN_SOCK, else
// $XDG_RUNTIME_DIR/devtun.sock, else ~/.devtun/devtun.sock.
func devtunSession(env Env) bool {
	candidates := []string{env.Getenv("DEVTUN_SOCK")}
	if runtime := env.Getenv("XDG_RUNTIME_DIR"); runtime != "" {
		candidates = append(candidates, filepath.Join(runtime, "devtun.sock"))
	}
	candidates = append(candidates, filepath.Join(env.Home, ".devtun", "devtun.sock"))
	for _, path := range candidates {
		if path == "" {
			continue
		}
		if info, err := env.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return true
		}
	}
	return false
}
