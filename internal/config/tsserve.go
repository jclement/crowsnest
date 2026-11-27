// Publishing to the tailnet (DESIGN.md 3.5.1): --tailscale-serve and --tailscale-serve-port.
// Kept in their own file and loader step, like accept.go.
//
// --tailscale-serve is an ordinary setting, editable in the app, unlike --tailscale-auth: it does
// not change who may sign in (the login still stands in front of every request) and the owner
// turns it on from the System page by design. The port is a flag, the environment or the file
// only: it is rarely needed, and a typo there should not be one click away.
//
// --no-auth with --tailscale-serve given at start (flag, environment, file) is a startup
// refusal: a loopback bind with no login would become a shell for the whole tailnet. A value
// saved in the app is not a refusal (that would leave an instance that cannot start), so the
// service refuses to publish and the System page says why.

package config

import (
	"fmt"
)

// Option names for this file's settings.
const (
	optTailscaleServe     = "tailscale-serve"
	optTailscaleServePort = "tailscale-serve-port"
)

func init() {
	options = append(options,
		option{name: optTailscaleServe, env: []string{"CROWSNEST_TAILSCALE_SERVE"}, kind: kindBool, def: "false", usage: "publish crowsnest to your tailnet while it runs, with a foreground `tailscale serve` (needs a login; never Funnel)"},
		option{name: optTailscaleServePort, env: []string{"CROWSNEST_TAILSCALE_SERVE_PORT"}, kind: kindInt, def: "0", usage: "the tailnet HTTPS port --tailscale-serve publishes on; 0 picks the first free of 443, 8443 and 10000"},
	)
	editable = append(editable, Setting{Name: optTailscaleServe, Label: "Publish to your tailnet", Section: "Tailnet", Kind: "bool", Apply: ApplyLive,
		Help: "run tailscale serve while crowsnest runs, so https://<machine>.<tailnet>.ts.net reaches it from your devices (the sign-in page first). Needs a login; never public"})
}

func (l *loader) loadTailscaleServe(cfg *Config) error {
	var err error
	if cfg.TailscaleServe, err = l.boolean(optTailscaleServe); err != nil {
		return err
	}
	if cfg.TailscaleServePort, err = l.integer(optTailscaleServePort); err != nil {
		return err
	}
	if cfg.TailscaleServePort < 0 || cfg.TailscaleServePort > 65535 {
		return fmt.Errorf("--tailscale-serve-port %d: give 0 (pick 443, 8443 or 10000) or a port from 1 to 65535", cfg.TailscaleServePort)
	}
	return nil
}

// checkTailscaleServe is CheckStartup's rule for this file: --tailscale-serve given at start
// with no login is refused rather than silently ignored.
func (c Config) checkTailscaleServe() error {
	if !c.TailscaleServe || !c.NoAuth {
		return nil
	}
	where := c.Sources[optTailscaleServe]
	if SourceKind(where) == KindDatabase || SourceKind(where) == KindDefault {
		return nil
	}
	return fmt.Errorf("%w: %s asks to publish crowsnest to your tailnet, but --no-auth turns the login off: "+
		"every device on the tailnet would get a shell. Drop --no-auth (crowsnest then uses the owner from first-run setup, "+
		"or --username with CROWSNEST_PASSWORD), or drop %s", ErrRefused, where, where)
}

// tsserveSettingValue renders this file's editable setting for the Settings page.
func (c Config) tsserveSettingValue(name string) (string, bool) {
	if name == optTailscaleServe {
		return boolText(c.TailscaleServe), true
	}
	return "", false
}

// withTailscaleServeSettings copies this file's editable setting from from.
func (c Config) withTailscaleServeSettings(from Config) Config {
	c.TailscaleServe = from.TailscaleServe
	return c
}
