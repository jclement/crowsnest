// The Settings reload sees the login start saw: serve removes CROWSNEST_PASSWORD from its
// environment once loaded (R1 H1), and the reload used to read the scrubbed environment, so an
// instance whose login came from the environment refused every setting saved in the app.

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/jclement/crowsnest/internal/config"
)

func TestReloadKeepsTheEnvironmentLogin(t *testing.T) {
	t.Setenv("CROWSNEST_USERNAME", "crow")
	t.Setenv("CROWSNEST_PASSWORD", "")
	_ = os.Unsetenv("CROWSNEST_PASSWORD") // as UnsetSecretEnv left it
	t.Setenv("CROWSNEST_DATA_DIR", t.TempDir())
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.RegisterFlags(flags)
	if _, err := reloader(flags, nil)(map[string]string{"poke-interval": "5m"}); err == nil || !strings.Contains(err.Error(), "--username and --password") {
		t.Fatalf("without the snapshot the reload should fail as it used to: %v", err)
	}
	cfg, err := reloader(flags, map[string]string{"CROWSNEST_PASSWORD": "a-long-password"})(map[string]string{"poke-interval": "5m", "tailscale-serve": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PokeInterval.String() != "5m0s" || !cfg.TailscaleServe {
		t.Fatalf("%s %v", cfg.PokeInterval, cfg.TailscaleServe)
	}
	if _, set := os.LookupEnv("CROWSNEST_PASSWORD"); set {
		t.Fatal("the reload put the password back into the environment")
	}
}
