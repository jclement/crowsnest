// Tailscale (DESIGN.md 3.5, 3.5.1): installed and logged in, which CLI and whether it is new
// enough for crowsnest to publish itself, the MagicDNS name and whether start added it to the
// allowed hosts, MagicDNS and HTTPS certificates, and whether `tailscale serve` already proxies
// to crowsnest's port. Read-only like every check: publishing itself is internal/tsserve's,
// turned on from the System page or with --tailscale-serve; the manual command stays as the
// alternative.

package doctor

import (
	"context"
	"net/netip"
	"slices"

	"github.com/jclement/crowsnest/internal/tailscale"
	"github.com/jclement/crowsnest/internal/tsserve"
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
			Fix:    "See README → Publish to your tailnet"})
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
	r.tailscaleVersion(ctx)
	name := Check{ID: "tailscale.name", Group: GroupTailscale, Title: "Tailnet name allowed"}
	switch {
	case info.DNSName == "" || (info.MagicDNSKnown && !info.MagicDNS):
		name.Status, name.Detail = StatusInfo, "MagicDNS is off: no name to allow or publish under"
		name.Fix = "Tailscale admin console → DNS → MagicDNS → Enable MagicDNS"
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
		https.Fix = "Tailscale admin console → DNS → HTTPS Certificates → Enable HTTPS"
	}
	r.add(https)
	r.tailscaleServe(ctx, info)
}

// tailscaleVersion says which CLI crowsnest would run and whether it can publish with it. The
// macOS app's CLI is not on PATH; crowsnest finds it in the app bundle.
func (r *runner) tailscaleVersion(ctx context.Context) {
	path, output := r.env.TailscaleVersion(ctx)
	check := Check{ID: "tailscale.version", Group: GroupTailscale, Title: "Tailscale CLI"}
	if path == "" {
		check.Status, check.Detail = StatusInfo, "not found on PATH or in /Applications/Tailscale.app: crowsnest cannot publish itself"
		check.Fix = "Install the tailscale CLI, or the Tailscale app on macOS"
		r.add(check)
		return
	}
	where := path
	if path == tailscale.MacAppBinary {
		where = "the Tailscale app's CLI (" + path + ")"
	}
	version, supported := tsserve.SupportedVersion(output)
	switch {
	case version == "":
		check.Status, check.Detail = StatusWarn, "could not read the version of "+where
	case !supported:
		check.Status, check.Detail = StatusWarn, version+" at "+where+": older than "+tsserve.MinVersion+", which publishing to the tailnet needs"
		check.Fix = "Update Tailscale"
	default:
		check.Status, check.Detail = StatusOK, version+" at "+where
	}
	r.add(check)
}

// tailscaleServe says whether something publishes crowsnest on the tailnet, and how to.
func (r *runner) tailscaleServe(ctx context.Context, info tailscale.Info) {
	cfg := r.in.Config
	check := Check{ID: "tailscale.serve", Group: GroupTailscale, Title: "Published on the tailnet over HTTPS"}
	serves, err := r.env.TailscaleServes(ctx, cfg.Port)
	manual := "to do it by hand instead: `" + tailscale.ServeCommand(cfg.Port) + "`"
	switch {
	case err != nil:
		check.Status, check.Detail = StatusSkip, "could not read `tailscale serve status`"
	case len(serves) > 0 && cfg.NoAuth:
		check.Status, check.Detail = StatusFail, serves[0].URL+" proxies to this crowsnest, which runs with --no-auth: every device on your tailnet that can reach this machine has a shell"
		check.Fix = "Start crowsnest with a login, or remove that serve config: `tailscale serve status` shows it"
	case len(serves) > 0:
		check.Status, check.Detail = StatusOK, "open "+serves[0].URL+" on your phone"
		if serves[0].Foreground && cfg.TailscaleServe {
			check.Detail += " (published by crowsnest while it runs)"
		}
		if !cfg.NoAuth && !trustsLoopback(cfg.TrustedProxies) {
			check.Status = StatusWarn
			check.Fix = "Set CROWSNEST_TRUSTED_PROXY=loopback (or --trusted-proxy loopback): behind `tailscale serve` every client arrives from 127.0.0.1, so the login limiter counts them all as one"
		}
	case cfg.TailscaleServe && cfg.NoAuth:
		check.Status, check.Detail = StatusWarn, "--tailscale-serve is on, but crowsnest refuses to publish itself with --no-auth"
		check.Fix = "Start crowsnest with a login, or turn Publish to your tailnet off in Settings"
	case cfg.TailscaleServe:
		check.Status, check.Detail = StatusInfo, "crowsnest publishes https://"+orDash(info.DNSName)+" while it runs (--tailscale-serve); the System page shows its state"
	default:
		check.Status = StatusInfo
		check.Detail = "not published. With it, https://" + orDash(info.DNSName) + " reaches crowsnest from any device on your tailnet, with the secure context the microphone, Web Push and the installable app need"
		check.Fix = "Publish it from the System page (Publish to your tailnet), or start crowsnest with --tailscale-serve; " + manual
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
