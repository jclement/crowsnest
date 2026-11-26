// Startup refusals (DESIGN.md section 3). These fail closed: a wrong default here is a shell on
// the network, so `serve` refuses to start and says exactly what to pass instead.

package config

import (
	"errors"
	"fmt"

	"github.com/spf13/pflag"
)

// ErrRefused marks every startup refusal, so callers can tell a deliberate refusal from a
// broken config file.
var ErrRefused = errors.New("refusing to start")

// CheckStartup applies the rules that apply to `serve` only:
//
//   - neither credentials nor --no-auth;
//   - --no-auth on a bind that is not loopback or tailnet, without --i-know-this-is-open;
//   - --session default without --allow-default-session.
//
// The fourth rule, `tailnet` with no Tailscale address, is enforced during bind resolution in
// Load because there is nothing to listen on without it.
func (c Config) CheckStartup() error {
	if !c.NoAuth && (c.Username == "" || c.Password == "") {
		return fmt.Errorf("%w: no login is configured. Pass --username with CROWSNEST_PASSWORD (or --password-file), "+
			"or pass --no-auth for a trusted localhost or tailnet", ErrRefused)
	}
	if c.NoAuth && !c.IKnowThisIsOpen {
		for _, listener := range c.Listeners {
			if isOpenAddr(listener.Addr.Addr()) {
				return fmt.Errorf("%w: --no-auth with --bind %s (%s) would put an unauthenticated shell on the network. "+
					"Add a login (--username and CROWSNEST_PASSWORD), bind localhost or tailnet, "+
					"or pass --i-know-this-is-open if this network really is trusted",
					ErrRefused, listener.Source, listener.Addr.Addr())
			}
		}
	}
	if c.Session == "default" && !c.AllowDefaultSession {
		return fmt.Errorf("%w: --session default is your main herdr session. Pass another --session name, "+
			"or --allow-default-session if you really mean it", ErrRefused)
	}
	return nil
}

// LoadForServe is Load followed by CheckStartup: the one call `serve` makes.
func LoadForServe(flags *pflag.FlagSet, opts Options) (Config, error) {
	cfg, err := Load(flags, opts)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.CheckStartup(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
