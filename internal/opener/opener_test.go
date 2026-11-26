// The opening rule in every situation the brief names, and the handoff's one-use, expiry and
// target.

package opener

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

type fakeInfo struct{ mode fs.FileMode }

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

func env(goos string, vars map[string]string, onPath map[string]string, sockets ...string) Env {
	return Env{
		GOOS: goos, Home: "/home/u", Interactive: true, UID: 501,
		Getenv: func(name string) string { return vars[name] },
		LookPath: func(name string) (string, error) {
			if path, ok := onPath[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		},
		Stat: func(path string) (os.FileInfo, error) {
			for _, socket := range sockets {
				if socket == path {
					return fakeInfo{mode: fs.ModeSocket}, nil
				}
			}
			return nil, fs.ErrNotExist
		},
	}
}

func TestDecide(t *testing.T) {
	devtunPath := map[string]string{"xdg-open": "/home/u/.devtun/bin/xdg-open"}
	cases := []struct {
		name string
		mode string
		env  Env
		via  string // "" means do not open
	}{
		{"macOS desktop", ModeAuto, env("darwin", nil, map[string]string{"open": "/usr/bin/open"}), "open"},
		{"Linux desktop", ModeAuto, env("linux", map[string]string{"DISPLAY": ":0"}, map[string]string{"xdg-open": "/usr/bin/xdg-open"}), "xdg-open"},
		{"Wayland", ModeAuto, env("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, map[string]string{"xdg-open": "/usr/bin/xdg-open"}), "xdg-open"},
		{"Linux without a display", ModeAuto, env("linux", nil, map[string]string{"xdg-open": "/usr/bin/xdg-open"}), ""},
		{"over SSH, nothing", ModeAuto, env("linux", map[string]string{"SSH_CONNECTION": "1 2 3 4", "DISPLAY": ":0"}, map[string]string{"xdg-open": "/usr/bin/xdg-open"}), ""},
		{"over SSH on a Mac", ModeAuto, env("darwin", map[string]string{"SSH_TTY": "/dev/ttys001"}, map[string]string{"open": "/usr/bin/open"}), ""},
		{"over SSH with $BROWSER", ModeAuto, env("linux", map[string]string{"SSH_CONNECTION": "x", "BROWSER": "w3m %s"}, nil), "BROWSER"},
		{"over SSH with devtun", ModeAuto, env("linux", map[string]string{"SSH_CONNECTION": "x", "XDG_RUNTIME_DIR": "/run/user/1"}, devtunPath, "/run/user/1/devtun.sock"), "devtun"},
		{"devtun shim but no session", ModeAuto, env("linux", map[string]string{"SSH_CONNECTION": "x"}, devtunPath), ""},
		{"devtun via DEVTUN_SOCK", ModeAuto, env("linux", map[string]string{"SSH_TTY": "x", "DEVTUN_SOCK": "/tmp/d.sock"}, devtunPath, "/tmp/d.sock"), "devtun"},
		{"never", ModeNever, env("darwin", nil, map[string]string{"open": "/usr/bin/open"}), ""},
		{"no opener on PATH", ModeAuto, env("darwin", nil, nil), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := Decide(tc.mode, tc.env)
			if decision.Via != tc.via {
				t.Fatalf("via %q (%s), want %q", decision.Via, decision.Reason, tc.via)
			}
			if tc.via == "" && decision.Reason == "" {
				t.Error("a decision not to open must say why")
			}
		})
	}
}

func TestDecideRefusals(t *testing.T) {
	root := env("darwin", nil, map[string]string{"open": "/usr/bin/open"})
	root.UID = 0
	if Decide(ModeAuto, root).Command != nil {
		t.Error("opened as root")
	}
	service := env("darwin", nil, map[string]string{"open": "/usr/bin/open"})
	service.Interactive = false
	if Decide(ModeAuto, service).Command != nil {
		t.Error("opened from a non-interactive start")
	}
	if Decide(ModeAlways, service).Command == nil {
		t.Error("--open always should open even without a terminal")
	}
	container := env("linux", map[string]string{"DISPLAY": ":0"}, map[string]string{"xdg-open": "/usr/bin/xdg-open"})
	container.InContainer = true
	if Decide(ModeAuto, container).Command != nil {
		t.Error("opened in a container")
	}
	withBrowser := env("darwin", map[string]string{"BROWSER": "firefox:chromium"}, nil)
	if got := Decide(ModeAuto, withBrowser).Command; len(got) != 1 || got[0] != "firefox" {
		t.Errorf("$BROWSER list: %v", got)
	}
}

func TestHandoffIsOneUseAndExpires(t *testing.T) {
	now := time.Now()
	handoffs := NewHandoffs(func() string { return "/setup#token=abc" }, func() time.Time { return now })
	id := handoffs.New()
	if target, ok := handoffs.Redeem(id); !ok || target != "/setup#token=abc" {
		t.Fatalf("first redemption: %q %v", target, ok)
	}
	if _, ok := handoffs.Redeem(id); ok {
		t.Fatal("redeemed twice")
	}
	late := handoffs.New()
	now = now.Add(HandoffTTL + time.Second)
	if _, ok := handoffs.Redeem(late); ok {
		t.Fatal("an expired id worked")
	}
	if _, ok := handoffs.Redeem("made-up"); ok {
		t.Fatal("an unknown id worked")
	}
}
