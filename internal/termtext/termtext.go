// Package termtext is the one place text is made safe to type into an agent's terminal (R1b N3).
// Crowsnest types lines into Claude's input box on its own authority (an answer to a question,
// and from M8 a poke), and the agents are told to trust lines that start `[crowsnest …]`. Every
// piece of such a line that came from somewhere else (a question's title, an option's label, the
// human's note) goes through Clean, and a piece an agent wrote goes through Quote as well.
//
// Clean returns one line of visible text: line breaks and tabs become spaces (a newline would
// submit early), ANSI escape sequences go (they would be keystrokes), and so do control
// characters (Cc), format characters (Cf: bidi overrides such as U+202E that make text read
// differently from its bytes, and zero-width characters), private-use characters (Co) and
// invalid UTF-8. Quote wraps cleaned agent text in double quotes that it cannot close, and turns
// square brackets into parentheses so it cannot imitate a `[crowsnest …]` header either.
//
// Block is the multi-line sibling of Clean (R2-M1): it keeps line breaks (a human's Chat prompt
// or job message may be several lines, and herdr's agent.prompt delivers them as one paste) but
// drops everything else Clean drops. orch.Prompt applies it to every text it types, so no caller
// can forget it; the one-line typed headers (answers, pokes, briefs) still use Clean and Quote on
// their parts, because one line is what they promise. DisarmHeaders is for agent-written text that
// keeps its lines: a line starting with a bracket cannot pass for a `[crowsnest …]` header.
package termtext

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jclement/crowsnest/internal/fleet"
)

// Clean flattens text to one line of visible characters separated by single spaces.
func Clean(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' ' // before stripping ANSI, which would drop line breaks rather than space them
		}
		return r
	}, text)
	text = fleet.StripANSI(text)
	var out strings.Builder
	space := false
	for _, r := range text {
		if unicode.IsSpace(r) || invisible(r) {
			space = space || (unicode.IsSpace(r) && out.Len() > 0)
			continue
		}
		if space {
			out.WriteByte(' ')
			space = false
		}
		out.WriteRune(r)
	}
	return out.String()
}

// invisible reports runes that must not reach a terminal: controls, escape, format and
// private-use characters, and the replacement for invalid UTF-8.
func invisible(r rune) bool {
	return r == utf8.RuneError || r == 0x1b || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r)
}

// bracketsAndQuotes turns brackets into parentheses and double quotes into single ones, the
// full-width forms included (R2-I1: `［crowsnest answer …］` reads like a header too).
var bracketsAndQuotes = strings.NewReplacer(`"`, `'`, "\uff02", "'", "[", "(", "]", ")", "\uff3b", "(", "\uff3d", ")")

// Quote cleans text written by an agent and wraps it in double quotes. Double quotes inside
// become single quotes and square brackets become parentheses, so the quoted text can neither
// end the quotation early nor pass for a `[crowsnest …]` header.
func Quote(text string) string {
	return `"` + bracketsAndQuotes.Replace(Clean(text)) + `"`
}

// Block makes multi-line text safe to type into a terminal: line breaks stay (CR LF and the other
// line and paragraph separators become one LF), tabs become four spaces (a tab can trigger
// completion in an input box), and ANSI escape sequences, other control characters, format and
// private-use characters and invalid UTF-8 go, as in Clean. Nothing else changes, so indentation
// and blank lines survive.
func Block(text string) string {
	text = strings.NewReplacer("\r\n", "\n", "\t", "    ").Replace(text)
	text = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\v', '\f', 0x85, 0x2028, 0x2029:
			return '\n'
		}
		return r
	}, text)
	text = fleet.StripANSI(text)
	return strings.Map(func(r rune) rune {
		if r != '\n' && invisible(r) {
			return -1
		}
		return r
	}, text)
}

// DisarmHeaders turns the opening bracket of every line that starts with one (after any
// indentation) into a parenthesis, so text an agent wrote cannot hold a line that passes for a
// `[crowsnest …]` header once it is shown to another agent (R2-M1).
func DisarmHeaders(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		switch {
		case strings.HasPrefix(trimmed, "["):
			lines[i] = indent + "(" + trimmed[1:]
		case strings.HasPrefix(trimmed, "\uff3b"):
			lines[i] = indent + "(" + strings.TrimPrefix(trimmed, "\uff3b")
		}
	}
	return strings.Join(lines, "\n")
}

// HasBidiControl reports whether text holds a bidirectional override or isolate (U+202A–U+202E,
// U+2066–U+2069) or a directional mark (U+200E, U+200F, U+061C): characters that make text
// display in an order different from its bytes. Validation refuses them in titles and labels, so
// what the human reads in the Inbox is what the agent wrote.
func HasBidiControl(text string) bool {
	return strings.ContainsFunc(text, func(r rune) bool {
		return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f || r == 0x061c
	})
}

// Cap shortens text to at most limit runes, ending with an ellipsis when it cut.
func Cap(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
