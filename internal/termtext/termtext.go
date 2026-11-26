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

// Quote cleans text written by an agent and wraps it in double quotes. Double quotes inside
// become single quotes and square brackets become parentheses, so the quoted text can neither
// end the quotation early nor pass for a `[crowsnest …]` header.
func Quote(text string) string {
	return `"` + strings.NewReplacer(`"`, `'`, "[", "(", "]", ")").Replace(Clean(text)) + `"`
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
