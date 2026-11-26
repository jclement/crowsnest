// Package redact scrubs token-shaped strings from text that leaves crowsnest's control: notices
// POSTed to --notify-url, and anything echoed from panes into logs (DESIGN.md section 4.9). It is
// a heuristic, not a guarantee; it errs towards hiding a harmless long identifier rather than
// letting a credential through.
package redact

import (
	"regexp"
	"strings"
)

// Placeholder replaces each redacted span.
const Placeholder = "[redacted]"

var patterns = []*regexp.Regexp{
	// Well-known prefixed secrets: GitHub, Slack, OpenAI/Anthropic-style, AWS, Stripe, npm.
	regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abposr]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{16,}|sk_(?:live|test)_[A-Za-z0-9]{16,}|AKIA[0-9A-Z]{16}|npm_[A-Za-z0-9]{30,})\b`),
	// Authorization headers and key=value secrets.
	regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|access[_-]?key)\s*[:=]\s*\S+`),
	// JSON web tokens.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	// URLs with credentials in them.
	regexp.MustCompile(`\b([a-z][a-z0-9+.-]*://)[^\s/:@]+:[^\s/@]+@`),
	// Long unbroken runs of hex or base64-ish characters with both letters and digits.
	regexp.MustCompile(`\b[A-Za-z0-9+/_-]{32,}={0,2}`),
}

// String returns text with every token-shaped span replaced by Placeholder.
func String(text string) string {
	for index, pattern := range patterns {
		switch index {
		case 1, 2:
			// Keep the label ("Bearer", "password=") so the reader knows what was there.
			text = pattern.ReplaceAllStringFunc(text, func(match string) string {
				return keepLabel(match)
			})
		case 4:
			text = pattern.ReplaceAllString(text, "${1}"+Placeholder+"@")
		case 5:
			text = pattern.ReplaceAllStringFunc(text, func(match string) string {
				if hasLetterAndDigit(match) {
					return Placeholder
				}
				return match
			})
		default:
			text = pattern.ReplaceAllString(text, Placeholder)
		}
	}
	return text
}

// keepLabel keeps the word before the separator of "label: secret" or "Bearer secret".
func keepLabel(match string) string {
	if cut := strings.IndexAny(match, ":="); cut >= 0 {
		return match[:cut+1] + " " + Placeholder
	}
	if cut := strings.IndexAny(match, " \t"); cut >= 0 {
		return match[:cut] + " " + Placeholder
	}
	return Placeholder
}

func hasLetterAndDigit(text string) bool {
	var letter, digit bool
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			letter = true
		}
	}
	return letter && digit
}
