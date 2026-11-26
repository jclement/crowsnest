package config

import (
	"strings"
	"testing"
)

func TestNameFromHostname(t *testing.T) {
	cases := map[string]string{
		"quark.example.lan":       "quark",
		"Quark":                   "quark",
		"Jeffs-MacBook-Pro.local": "jeffs-macbook-pro",
		"bad name!":               "bad-name",
		"":                        FallbackName,
		"...":                     FallbackName,
		"-x-":                     "x",
		strings.Repeat("a", 40):   strings.Repeat("a", 32),
		"über":                    "ber",
		"host_1.tail1234.ts.net":  "host_1",
		"<script>.evil":           "script",
	}
	for host, want := range cases {
		if got := NameFromHostname(host); got != want {
			t.Errorf("NameFromHostname(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestUpkeepOptions(t *testing.T) {
	cfg := newHarness(t).mustLoad()
	if cfg.Name != "quark" || cfg.NoRemoteControl || cfg.JobRemoteControl || cfg.CompactAt != DefaultCompactAt || cfg.FreshAt != -1 {
		t.Fatalf("defaults: %+v", cfg)
	}
	if got := cfg.RemoteControlName(); got != "quark crowsnest" {
		t.Errorf("remote control name %q", got)
	}
	cfg = newHarness(t, "--name", "Lab-2", "--session", "work", "--no-remote-control", "--job-remote-control", "--compact-at", "0", "--fresh-at", "4:05").mustLoad()
	if cfg.Name != "Lab-2" || !cfg.NoRemoteControl || !cfg.JobRemoteControl || cfg.CompactAt != 0 || cfg.FreshAt != 4*60+5 {
		t.Fatalf("flags: %+v", cfg)
	}
	if got := cfg.RemoteControlName(); got != "Lab-2 crowsnest work" {
		t.Errorf("remote control name %q", got)
	}
	cfg = newHarness(t).withEnv("CROWSNEST_NAME", "envname", "CROWSNEST_FRESH_AT", "23:59").mustLoad()
	if cfg.Name != "envname" || cfg.FreshAt != 23*60+59 {
		t.Fatalf("env: %+v", cfg)
	}
	bad := [][]string{
		{"--name", "has space"}, {"--name", "-dash"}, {"--name", strings.Repeat("x", 33)}, {"--name", "a\"b"},
		{"--compact-at", "-1"}, {"--compact-at", "500"},
		{"--fresh-at", "24:00"}, {"--fresh-at", "4"}, {"--fresh-at", "04:5"}, {"--fresh-at", "noon"},
	}
	for _, args := range bad {
		if _, err := newHarness(t, args...).load(); err == nil {
			t.Errorf("%v: accepted", args)
		}
	}
}
