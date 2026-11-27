// Tests for the settings layer: flag > env > database > file > default, the source each value
// reports, and that a stored value can never change a setting the app may not edit.

package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStoredSettingPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		env       []string
		file      string
		stored    map[string]string
		want      time.Duration
		wantKind  string
		wantWhere string
	}{
		{name: "default", want: 15 * time.Minute, wantKind: KindDefault, wantWhere: "default"},
		{name: "file", file: "poke-interval: 20m\n", want: 20 * time.Minute, wantKind: KindFile, wantWhere: "config.yaml"},
		{name: "database beats file", file: "poke-interval: 20m\n", stored: map[string]string{"poke-interval": "30m"}, want: 30 * time.Minute, wantKind: KindDatabase, wantWhere: SourceDatabase},
		{name: "env beats database", env: []string{"CROWSNEST_POKE_INTERVAL", "5m"}, stored: map[string]string{"poke-interval": "30m"}, want: 5 * time.Minute, wantKind: KindEnv, wantWhere: "CROWSNEST_POKE_INTERVAL"},
		{name: "flag beats all", args: []string{"--poke-interval=1m"}, env: []string{"CROWSNEST_POKE_INTERVAL", "5m"}, stored: map[string]string{"poke-interval": "30m"}, want: time.Minute, wantKind: KindFlag, wantWhere: "--poke-interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.args...).withEnv(tc.env...)
			if tc.file != "" {
				h.writeConfig(tc.file)
			}
			opts := h.options()
			opts.Stored = tc.stored
			cfg, err := Load(h.flags(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PokeInterval != tc.want {
				t.Errorf("poke interval %v, want %v", cfg.PokeInterval, tc.want)
			}
			where := cfg.Sources["poke-interval"]
			if where != tc.wantWhere || SourceKind(where) != tc.wantKind {
				t.Errorf("source %q (%s), want %q (%s)", where, SourceKind(where), tc.wantWhere, tc.wantKind)
			}
		})
	}
}

func TestStoredValuesNeverReachSensitiveOptions(t *testing.T) {
	h := newHarness(t)
	opts := h.options()
	opts.Stored = map[string]string{"no-auth": "true", "bind": "0.0.0.0", "session": "default", "data-dir": "/tmp/x", "username": "eve"}
	cfg, err := Load(h.flags(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NoAuth || cfg.Session != DefaultSession || cfg.Username != "" || strings.HasPrefix(cfg.DataDir, "/tmp/x") || cfg.Binds[0] != "localhost" {
		t.Errorf("a stored value changed a sensitive option: %+v", cfg)
	}
	for _, name := range []string{"no-auth", "bind", "session", "data-dir", "username", "password", "dir", "port", "tls-cert", "i-know-this-is-open", "allowed-host", "trusted-proxy"} {
		if Editable(name) {
			t.Errorf("%s must not be editable in the app", name)
		}
	}
}

func TestStoredValueIsValidated(t *testing.T) {
	h := newHarness(t)
	opts := h.options()
	opts.Stored = map[string]string{"permission-mode": "yolo"}
	if _, err := Load(h.flags(), opts); err == nil {
		t.Fatal("an invalid stored permission mode was accepted")
	}
}

func TestSettingValueRoundTrips(t *testing.T) {
	h := newHarness(t)
	opts := h.options()
	opts.Stored = map[string]string{"poke-interval": "1h30m", "fresh-at": "04:05", "protected-branch": "main,develop", "engine": "codex", "no-remote-control": "true"}
	cfg, err := Load(h.flags(), opts)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"poke-interval": "1h30m", "fresh-at": "04:05", "protected-branch": "main,develop", "engine": "codex", "no-remote-control": "true", "notify-url": "false"}
	for name, value := range want {
		if got := cfg.SettingValue(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestSourceKind(t *testing.T) {
	for where, want := range map[string]string{"--port": KindFlag, "CROWSNEST_PORT": KindEnv, "config.yaml": KindFile, "database": KindDatabase, "default": KindDefault, "": KindDefault} {
		if got := SourceKind(where); got != want {
			t.Errorf("SourceKind(%q) = %s, want %s", where, got, want)
		}
	}
}

func TestTailnetNameJoinsTheAllowedHosts(t *testing.T) {
	h := newHarness(t)
	opts := h.options()
	opts.TailnetName = func() string { return "Quark-2.tail1234.ts.net." }
	cfg, err := Load(h.flags(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TailnetName != "quark-2.tail1234.ts.net" || !slices.Contains(cfg.HostNames, "quark-2.tail1234.ts.net") {
		t.Fatalf("name %q hosts %v", cfg.TailnetName, cfg.HostNames)
	}
	off := newHarness(t, "--no-tailscale-detect")
	opts = off.options()
	opts.TailnetName = func() string { t.Fatal("detected despite --no-tailscale-detect"); return "" }
	if cfg, err := Load(off.flags(), opts); err != nil || cfg.TailnetName != "" {
		t.Fatalf("%v %q", err, cfg.TailnetName)
	}
}
