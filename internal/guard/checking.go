// The check hold (DESIGN.md 6.15, R7-C01). While crowsnest runs a job's check in the job's own
// worktree, it writes a marker file beside the job's guard rules, holding the time the check
// would have ended by; the guard hook, which runs locally on the agent's tool calls, then answers
// "wait" to every tool call instead of letting the agent change the files being checked. A marker
// past its deadline (left by a crowsnest that stopped mid-check) holds nothing.

package guard

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CheckingMarkerFile is the marker's name in the job's directory, beside guard.json.
const CheckingMarkerFile = "checking"

// CheckHoldReason is what the agent is told while the hold is on.
const CheckHoldReason = "Wait: crowsnest is running the check on your commit in this worktree, and tool calls are held until it ends " +
	"(a few minutes at most). Do nothing to the files meanwhile; call again in a minute."

// HeldByCheck reports whether the marker beside rulesFile holds the agent's tools now.
func HeldByCheck(rulesFile string, now time.Time) bool {
	if rulesFile == "" {
		return false
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(rulesFile), CheckingMarkerFile)) //nolint:gosec // crowsnest's own job directory
	if err != nil {
		return false
	}
	deadline, err := time.Parse(time.RFC3339, strings.TrimSpace(string(body)))
	return err == nil && now.Before(deadline)
}
