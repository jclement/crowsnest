// The SAY contract (DESIGN.md 7.1, the same as Earshot's): when talk mode is on, the orchestrator
// begins each final reply with `SAY: <one sentence for the ear>`, a blank line, then the detail.
// ParseSay reads that line back out of a reply.

package voice

import (
	"strings"
	"unicode/utf8"
)

// MaxSpokenRunes caps one spoken sentence, the same limit `crowsnest say` has: one breath.
const MaxSpokenRunes = 300

// SayKind is what a reply asks to be done aloud.
type SayKind string

// Outcomes of ParseSay.
const (
	SaySpeak  SayKind = "speak"  // a sentence to speak
	SaySilent SayKind = "silent" // `SAY: -` or a bare `noop`: say nothing, play nothing
	SayNone   SayKind = "none"   // no SAY line: the web app plays a short chime instead
)

// SayResult is a parsed reply.
type SayResult struct {
	Kind     SayKind
	Sentence string // SaySpeak only, cleaned for the ear
}

// ParseSay reads the SAY line from an assistant reply. Only the first non-blank line counts, so
// a `SAY:` quoted later in a reply (in a code block, say) is never spoken.
func ParseSay(reply string) SayResult {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "noop" || trimmed == "`noop`" {
		return SayResult{Kind: SaySilent}
	}
	first, _, _ := strings.Cut(trimmed, "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(first), "SAY:")
	if !ok {
		return SayResult{Kind: SayNone}
	}
	sentence := cleanForSpeech(rest)
	switch sentence {
	case "-", "—", "–":
		return SayResult{Kind: SaySilent}
	case "":
		return SayResult{Kind: SayNone}
	}
	return SayResult{Kind: SaySpeak, Sentence: sentence}
}

// cleanForSpeech collapses whitespace, drops Markdown emphasis and code marks a voice would read
// out or stumble on, and caps the length at a word boundary.
func cleanForSpeech(text string) string {
	text = strings.NewReplacer("**", "", "__", "", "`", "").Replace(text)
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= MaxSpokenRunes {
		return text
	}
	runes := []rune(text)[:MaxSpokenRunes]
	cut := string(runes)
	if space := strings.LastIndex(cut, " "); space > MaxSpokenRunes/2 {
		cut = cut[:space]
	}
	return cut + "…"
}
