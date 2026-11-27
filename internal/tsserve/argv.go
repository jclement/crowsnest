// The argv allowlist: every tailscale command line this package may run, and nothing else.
//
// Publishing crowsnest is the one state-changing Tailscale call crowsnest makes, so it is boxed
// in here rather than trusted to every caller. CheckArgs admits exactly four shapes: three
// read-only ones (`version`, `status --json`, `serve status --json`) and the foreground
// `serve --https=<port> http://127.0.0.1:<port>` (or [::1]). There is no `--bg` (it would leave a
// persistent config behind), no `reset` or `off` (crowsnest never removes anyone's handlers, its
// own foreground session ends with its process), and above all no Funnel: anything that
// mentions funnel is refused outright, so no code path can put crowsnest on the public
// internet. Both the service and the watchdog child check every argv against it before exec.

package tsserve

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrForbiddenArgs is a tailscale command line outside the allowlist.
var ErrForbiddenArgs = errors.New("tsserve: refusing a tailscale command outside the allowlist")

// ErrFunnel is any attempt to involve Tailscale Funnel: publishing to the public internet is
// never something crowsnest does.
var ErrFunnel = errors.New("tsserve: Tailscale Funnel would publish crowsnest to the public internet; crowsnest never uses it")

// targetPattern is the only proxy target crowsnest publishes: plain HTTP to a loopback address
// and a port. The service builds it from a listener; anything else is refused.
var targetPattern = regexp.MustCompile(`^http://(127\.0\.0\.1|\[::1\]):([0-9]{1,5})$`)

// Read-only command lines.
var (
	argsVersion     = []string{"version"}
	argsStatus      = []string{"status", "--json"}
	argsServeStatus = []string{"serve", "status", "--json"}
)

// ServeArgs is the foreground publish command line: HTTPS on httpsPort of this node's MagicDNS
// name, proxied to target. It panics on a shape CheckArgs refuses, which would be a bug here.
func ServeArgs(httpsPort int, target string) []string {
	args := []string{"serve", "--https=" + strconv.Itoa(httpsPort), target}
	if err := CheckArgs(args); err != nil {
		panic(err)
	}
	return args
}

// CheckArgs reports whether args (without the binary) is one of the allowed command lines.
func CheckArgs(args []string) error {
	for _, arg := range args {
		if strings.Contains(strings.ToLower(arg), "funnel") {
			return ErrFunnel
		}
	}
	switch {
	case equal(args, argsVersion), equal(args, argsStatus), equal(args, argsServeStatus):
		return nil
	case isServe(args):
		return nil
	}
	return fmt.Errorf("%w: %q", ErrForbiddenArgs, args)
}

// isServe matches `serve --https=<1..65535> <loopback target>` exactly.
func isServe(args []string) bool {
	if len(args) != 3 || args[0] != "serve" || !strings.HasPrefix(args[1], "--https=") {
		return false
	}
	port, err := strconv.Atoi(strings.TrimPrefix(args[1], "--https="))
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	match := targetPattern.FindStringSubmatch(args[2])
	if match == nil {
		return false
	}
	targetPort, err := strconv.Atoi(match[2])
	return err == nil && targetPort >= 1 && targetPort <= 65535
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- Version ----------------------------------------------------------------

// MinVersion is the oldest Tailscale whose CLI has the form used here: `tailscale serve
// --https=<port> <target>`, running in the foreground until interrupted, with the session's
// config removed when the CLI goes away. That form replaced the older `tailscale serve https /
// proxy` syntax in 1.52 (Tailscale's changelog; not checked against a 1.52 binary here).
const MinVersion = "1.52.0"

// version is a parsed major.minor.patch.
type version [3]int

// parseVersion reads the first line of `tailscale version` ("1.104.5" or
// "1.104.5-t788384763-gd9218e278"); ok is false when it is not a version.
func parseVersion(output string) (version, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	line = strings.TrimSpace(line)
	line, _, _ = strings.Cut(line, "-")
	parts := strings.Split(line, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return version{}, false
	}
	var parsed version
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return version{}, false
		}
		parsed[i] = number
	}
	return parsed, true
}

// atLeast reports whether v is min or newer.
func (v version) atLeast(min version) bool {
	for i := range v {
		if v[i] != min[i] {
			return v[i] > min[i]
		}
	}
	return true
}

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// SupportedVersion reads `tailscale version` output: the version it names ("" when unreadable)
// and whether publishing supports it (MinVersion or newer). doctor uses it.
func SupportedVersion(output string) (string, bool) {
	have, ok := parseVersion(output)
	if !ok {
		return "", false
	}
	minimum, _ := parseVersion(MinVersion)
	return have.String(), have.atLeast(minimum)
}
