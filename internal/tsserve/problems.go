// Why publishing cannot happen, in sentences a person can act on: each problem names what is
// wrong, the exact switch or command that fixes it, and (for the admin console) where it is.
//
// Problems come from two places. Most are found before anything runs, from `tailscale status
// --json` and `tailscale serve status --json` (logged out, MagicDNS or HTTPS certificates off,
// no free port). The rest are read from what `tailscale serve` printed when it stopped or while
// it waits (serve not enabled for the tailnet, no permission, an old CLI). Tailscale's own text
// is matched, never shown: it goes to the server log, and the browser gets crowsnest's sentence
// (DESIGN.md 4.9, R1 I2). The one exception is the enable-Serve link tailscale prints, which is
// passed on only when it is a login.tailscale.com address.

package tsserve

import (
	"regexp"
	"strings"
)

// Problem is a reason publishing is refused or failed.
type Problem struct {
	Reason string `json:"reason"`
	Fix    string `json:"fix,omitempty"`
	FixURL string `json:"fix_url,omitempty"`
	// Waiting is true when tailscale serve is still running and will carry on by itself once the
	// fix is made (Serve not yet enabled for the tailnet).
	Waiting bool `json:"-"`
}

// adminDNS is the admin console page with the MagicDNS and HTTPS certificate switches.
const adminDNS = "https://login.tailscale.com/admin/dns"

var (
	problemNotInstalled = Problem{Reason: "Tailscale is not installed on this machine.",
		Fix: "Install it from tailscale.com/download and sign in; this card then offers to publish crowsnest."}
	problemNotRunning = Problem{Reason: "Tailscale is not running on this machine (tailscaled did not answer).",
		Fix: "Start Tailscale: open the Tailscale app, or `sudo systemctl start tailscaled` on Linux. crowsnest tries again by itself."}
	problemLoggedOut = Problem{Reason: "Tailscale is logged out on this machine.",
		Fix: "Sign in: `tailscale up`, or Log in from the Tailscale app's menu. crowsnest tries again by itself."}
	problemStopped = Problem{Reason: "Tailscale is installed but not connected.",
		Fix: "Connect it: `tailscale up`, or Connect in the Tailscale app. crowsnest tries again by itself."}
	problemMagicDNS = Problem{Reason: "MagicDNS is off for your tailnet, so this machine has no ts.net name to publish under.",
		Fix: "Tailscale admin console → DNS → MagicDNS → Enable MagicDNS.", FixURL: adminDNS}
	problemHTTPS = Problem{Reason: "HTTPS certificates are off for your tailnet, so tailscale serve cannot give crowsnest an https:// address.",
		Fix: "Tailscale admin console → DNS → HTTPS Certificates → Enable HTTPS.", FixURL: adminDNS}
	problemServeDisabled = Problem{Reason: "Serve is not turned on for your tailnet yet.",
		Fix: "A tailnet admin turns it on once, at the link Tailscale gave. crowsnest is waiting and carries on by itself once it is on.", Waiting: true}
	problemOperator = Problem{Reason: "Tailscale refused to let this user change its serve config.",
		Fix: "On Linux, allow your user once: `sudo tailscale set --operator=$USER`. crowsnest tries again by itself."}
	problemPortTaken = Problem{Reason: "Another serve config took the port while crowsnest was starting.",
		Fix: "crowsnest chooses again on its next try and never touches the other config."}
)

// problemTooOld is the CLI older than MinVersion (or one whose version could not be read).
func problemTooOld(have string) Problem {
	if have == "" {
		have = "an unknown version"
	}
	return Problem{Reason: "This machine's Tailscale (" + have + ") is older than " + MinVersion + ", the first whose `tailscale serve --https=<port> <target>` crowsnest uses.",
		Fix: "Update Tailscale (tailscale.com/download, or your package manager), then try again."}
}

// loginLink is the one URL taken from tailscale's output: the enable-Serve page.
var loginLink = regexp.MustCompile(`https://login\.tailscale\.com/[A-Za-z0-9/?=&._~%-]+`)

// classify reads what tailscale serve printed and returns the matching problem, if any.
func classify(output string) (Problem, bool) {
	text := strings.ToLower(output)
	has := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(text, word) {
				return true
			}
		}
		return false
	}
	switch {
	case has("serve is not enabled"):
		problem := problemServeDisabled
		problem.FixURL = loginLink.FindString(output)
		return problem, true
	case has("magicdns"):
		return problemMagicDNS, true
	case strings.Contains(text, "https") && has("not enabled", "disabled", "certificate"):
		return problemHTTPS, true
	case has("failed to connect to local tailscale", "is tailscale running", "tailscaled not running", "cannot connect to tailscaled"):
		return problemNotRunning, true
	case has("needslogin", "logged out", "not logged in", "log in first"):
		return problemLoggedOut, true
	case has("access denied", "permission denied", "--operator"):
		return problemOperator, true
	case has("flag provided but not defined", "unknown flag", "unknown subcommand"):
		return problemTooOld(""), true
	case has("already in use", "already exists", "already serving", "listener already"):
		return problemPortTaken, true
	}
	return Problem{}, false
}
