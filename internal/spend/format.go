// Plain-text spend, for job_get, `crowsnest job get` and logs: the same figures the web app
// shows, worded so an agent reading its own job can tell an estimate from a bill.

package spend

import (
	"fmt"
	"strings"
	"time"
)

// FormatTokens is a token count made short: 950, 12.3k, 4.56M.
func FormatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return trimZero(fmt.Sprintf("%.2f", float64(n)/1e6)) + "M"
	case n >= 10_000:
		return trimZero(fmt.Sprintf("%.0f", float64(n)/1e3)) + "k"
	case n >= 1_000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1e3)) + "k"
	}
	return fmt.Sprintf("%d", n)
}

func trimZero(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// FormatUSD is an amount with sensible precision: $0.004, $0.27, $12.40, $1,204.
func FormatUSD(usd float64) string {
	switch {
	case usd == 0:
		return "$0.00"
	case usd < 0.01:
		return fmt.Sprintf("$%.3f", usd)
	case usd < 1000:
		return fmt.Sprintf("$%.2f", usd)
	}
	whole := fmt.Sprintf("%.0f", usd)
	var groups []string
	for len(whole) > 3 {
		groups = append([]string{whole[len(whole)-3:]}, groups...)
		whole = whole[:len(whole)-3]
	}
	return "$" + strings.Join(append([]string{whole}, groups...), ",")
}

// CostText is the cost as shown everywhere: "$1.20 est.", "≥ $1.20 est." when some tokens are
// from an unknown model, "cost unknown" when none could be priced.
func CostText(s Summary) string {
	if s.CostUSD == nil {
		return "cost unknown"
	}
	text := FormatUSD(*s.CostUSD) + " est."
	if s.CostPartial {
		text = "≥ " + text
	}
	return text
}

// Describe is a summary as a few lines of text.
func Describe(s Summary) string {
	if !s.Measured {
		switch s.Reason {
		case ReasonCodex:
			return "Not measured: crowsnest never learned this Codex job's session, so it cannot find its log."
		case ReasonOrchestrator:
			return "Not measured separately: the orchestrator does this job in its own conversation, so its use is on the orchestrator's line."
		case ReasonLead:
			return "Not measured separately: the project's lead does this job in its own conversation, so its use is on the lead's line."
		}
		return "Not measured."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (API-equivalent estimate; subscription plans are not billed per token)\n", CostText(s))
	fmt.Fprintf(&b, "Tokens: %s in, %s out, %s cache write, %s cache read (%.0f%% of the prompt from cache)\n",
		FormatTokens(s.InputTokens), FormatTokens(s.OutputTokens), FormatTokens(s.CacheWriteTokens), FormatTokens(s.CacheReadTokens), s.CacheHitShare*100)
	fmt.Fprintf(&b, "Model calls: %d, tool calls: %d", s.Turns, s.ToolCalls)
	if s.WallSeconds > 0 {
		fmt.Fprintf(&b, ", wall time: %s", (time.Duration(s.WallSeconds) * time.Second).String())
	}
	if len(s.Models) > 0 {
		fmt.Fprintf(&b, "\nModels: %s", strings.Join(s.Models, ", "))
	}
	if s.UnpricedTokens > 0 {
		fmt.Fprintf(&b, "\n%s tokens are from a model with no known price.", FormatTokens(s.UnpricedTokens))
	}
	return b.String()
}
