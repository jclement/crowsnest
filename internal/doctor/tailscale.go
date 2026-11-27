// Tailscale (DESIGN.md 3.5): installed and logged in, the MagicDNS name and whether start added
// it to the allowed hosts, HTTPS certificates, and whether `tailscale serve` already proxies to
// crowsnest's port; if not, the exact command. Read-only: crowsnest never runs `tailscale serve`
// itself, it prints the command.

package doctor

import (
	"context"
	"net/netip"
	"slices"

	"github.com/jclement/crowsnest/internal/tailscale"
)

// GroupTailscale is the Tailscale section.
const GroupTailscale = "tailscale"

func (r *runner) tailscaleChecks(ctx context.Context) {
	if !r.configOK() {
		return
	}
	cfg := r.in.Config
	info := r.env.Tailscale(ctx)
	if !info.Installed {
		r.add(Check{ID: "tailscale.installed", Group: GroupTailscale, Title: "Tailscale", Status: StatusInfo,
			Detail: "not installed (optional: it is the easy way to reach crowsnest from your phone, over HTTPS)",
			Fix:    "See README → On your tailnet with Tailscale"})
		return
	}
	if !info.Running {
		state := info.State
		if info.Error != "" {
			state = info.Error
		}
		r.add(Check{ID: "tailscale.installed", Group: GroupTailscale, Title: "Tailscale", Status: StatusWarn,
			Detail: "installed but not connected (" + orDash(state) + ")", Fix: "Connect it: `tailscale up`", Command: "tailscale up"})
		return
	}
	r.add(Check{ID: "tailscale.installed", Group: GroupTailscale, Title: "Tailscale", Status: StatusOK, Detail: "connected as " + orDash(info.DNSName)})
	name := Check{ID: "tailscale.name", Group: GroupTailscale, Title: "Tailnet name allowed"}
	switch {
	case info.DNSName == "":
		name.Status, name.Detail = StatusInfo, "MagicDNS is off: no name to allow"
	case cfg.TailnetName == info.DNSName || slices.Contains(cfg.HostNames, info.DNSName):
		name.Status, name.Detail = StatusOK, info.DNSName+" (detected at start)"
	default:
		name.Status, name.Detail = StatusWarn, info.DNSName+" is not an allowed host, so requests through `tailscale serve` would be refused"
		name.Fix = "Drop --no-tailscale-detect, or pass --allowed-host " + info.DNSName
	}
	r.add(name)
	https := Check{ID: "tailscale.https", Group: GroupTailscale, Title: "Tailscale HTTPS certificates"}
	if info.HTTPS {
		https.Status, https.Detail = StatusOK, "enabled"
	} else {
		https.Status, https.Detail = StatusWarn, "not enabled: `tailscale serve` cannot give crowsnest an https:// address"
		https.Fix = "Turn on HTTPS certificates in the Tailscale admin console (DNS page)"
	}
	r.add(https)
	r.tailscaleServe(ctx, info)
}

// tailscaleServe says whether `tailscale serve` proxies to crowsnest, and how to make it.
func (r *runner) tailscaleServe(ctx context.Context, info tailscale.Info) {
	cfg := r.in.Config
	check := Check{ID: "tailscale.serve", Group: GroupTailscale, Title: "Served on the tailnet over HTTPS"}
	serves, err := r.env.TailscaleServes(ctx, cfg.Port)
	switch {
	case err != nil:
		check.Status, check.Detail = StatusSkip, "could not read `tailscale serve status`"
	case len(serves) > 0:
		check.Status, check.Detail = StatusOK, "open "+serves[0].URL+" on your phone"
		if !cfg.NoAuth && !trustsLoopback(cfg.TrustedProxies) {
			check.Status = StatusWarn
			check.Fix = "Set CROWSNEST_TRUSTED_PROXY=loopback (or --trusted-proxy loopback): behind `tailscale serve` every client arrives from 127.0.0.1, so the login limiter counts them all as one"
		}
	default:
		check.Status = StatusInfo
		check.Detail = "not set up. With it, https://" + orDash(info.DNSName) + " reaches crowsnest from any device on your tailnet, with the secure context the microphone, Web Push and the installable app need"
		check.Fix = "Run this once (crowsnest never changes Tailscale's config itself), then set CROWSNEST_TRUSTED_PROXY=loopback"
		check.Command = tailscale.ServeCommand(cfg.Port)
	}
	r.add(check)
}

func trustsLoopback(prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(netip.MustParseAddr("127.0.0.1")) {
			return true
		}
	}
	return false
}
