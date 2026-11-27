// --tailscale-serve and its port: precedence like every editable option, validation, and the
// startup refusal with --no-auth (but not for a value saved in the app).

package config

import (
	"errors"
	"testing"
)

func TestTailscaleServeDefaultsAndPrecedence(t *testing.T) {
	cfg := newHarness(t, "--username", "jeff", "--password", "a-long-password").mustLoad()
	if cfg.TailscaleServe || cfg.TailscaleServePort != 0 {
		t.Fatalf("defaults: %v %d", cfg.TailscaleServe, cfg.TailscaleServePort)
	}
	if !Editable(optTailscaleServe) || Editable(optTailscaleServePort) {
		t.Fatal("the switch is an app setting, the port is not")
	}
	harness := newHarness(t, "--username", "jeff", "--password", "a-long-password")
	opts := harness.options()
	opts.Stored = map[string]string{optTailscaleServe: "true", optTailscaleServePort: "8443"}
	cfg, err := Load(harness.flags(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TailscaleServe || cfg.Sources[optTailscaleServe] != SourceDatabase || cfg.TailscaleServePort != 0 {
		t.Fatalf("stored: %v %q %d (the port is never read from the database)", cfg.TailscaleServe, cfg.Sources[optTailscaleServe], cfg.TailscaleServePort)
	}
	if cfg.SettingValue(optTailscaleServe) != "true" {
		t.Fatal(cfg.SettingValue(optTailscaleServe))
	}
	cfg = newHarness(t, "--username", "jeff", "--password", "a-long-password").withEnv("CROWSNEST_TAILSCALE_SERVE", "true", "CROWSNEST_TAILSCALE_SERVE_PORT", "10000").mustLoad()
	if !cfg.TailscaleServe || cfg.TailscaleServePort != 10000 || cfg.Sources[optTailscaleServe] != "CROWSNEST_TAILSCALE_SERVE" {
		t.Fatalf("env: %+v", cfg.Sources[optTailscaleServe])
	}
	if next := (Config{}).WithSettings(cfg); !next.TailscaleServe {
		t.Fatal("WithSettings dropped tailscale-serve")
	}
}

func TestTailscaleServePortValidation(t *testing.T) {
	for _, port := range []string{"-1", "65536"} {
		_, err := newHarness(t, "--username", "jeff", "--password", "a-long-password", "--tailscale-serve-port", port).load()
		assertErrorContains(t, err, "--tailscale-serve-port")
	}
}

func TestTailscaleServeRefusedWithoutALogin(t *testing.T) {
	_, err := newHarness(t, "--no-auth", "--tailscale-serve").serve()
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("flag: %v", err)
	}
	assertErrorContains(t, err, "every device on the tailnet would get a shell")
	_, err = newHarness(t, "--no-auth").withEnv("CROWSNEST_TAILSCALE_SERVE", "1").serve()
	assertErrorContains(t, err, "CROWSNEST_TAILSCALE_SERVE")

	// Saved in the app: not a refusal (the service refuses to publish and says why).
	harness := newHarness(t, "--no-auth")
	opts := harness.options()
	opts.Stored = map[string]string{optTailscaleServe: "true"}
	if _, err := LoadForServe(harness.flags(), opts); err != nil {
		t.Fatalf("stored: %v", err)
	}
}
