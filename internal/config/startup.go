// Startup refusals (DESIGN.md section 3). These fail closed: a wrong default here is a shell on
// the network, so `serve` refuses to start and says exactly what to pass instead.

package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/pflag"
)

// ErrRefused marks every startup refusal, so callers can tell a deliberate refusal from a
// broken config file.
var ErrRefused = errors.New("refusing to start")

// CheckStartup applies the rules that apply to `serve` only:
//
//   - --no-auth on a bind that is not loopback or tailnet, without --i-know-this-is-open;
//   - --session default without --allow-default-session;
//   - SUPPRESS_AUTH (rather than --no-auth or CROWSNEST_NO_AUTH) on a bind that is not
//     loopback-only (R1 L4).
//
// The rule `tailnet` with no Tailscale address is enforced during bind resolution in Load
// because there is nothing to listen on without it.
//
// Neither credentials nor --no-auth used to be a refusal too. It is now the locked first-run
// start (DESIGN.md 3 and 4.14): serve looks in the database for a stored owner, and without one
// serves nothing but the token-gated setup page until an owner exists. That decision needs the
// database, so it is serve's, not this function's.
func (c Config) CheckStartup() error {
	if c.NoAuthFromSuppressAuth && !c.LoopbackOnly() {
		return fmt.Errorf("%w: SUPPRESS_AUTH turns the login off only on a localhost bind, and this bind (%s) is not. "+
			"Use CROWSNEST_NO_AUTH=true (or --no-auth) if you really mean to serve without a login here, "+
			"or add a login (--username and CROWSNEST_PASSWORD)", ErrRefused, c.bindSources())
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

// LoopbackOnly reports whether every listen address is a loopback address.
func (c Config) LoopbackOnly() bool { return loopbackOnly(c.Listeners) }

// bindSources lists the distinct --bind values, for messages.
func (c Config) bindSources() string {
	var sources []string
	for _, listener := range c.Listeners {
		if !slices.Contains(sources, listener.Source) {
			sources = append(sources, listener.Source)
		}
	}
	return strings.Join(sources, ", ")
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
