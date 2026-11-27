// Questions asked in prose (DESIGN.md 6.18). An agent that ends its turn by asking the human
// something in its reply, instead of calling ask, has stopped: nobody reads the reply. The Stop
// hook's last_assistant_message is checked, and such a stop is refused with a reason that says
// exactly that, so the agent turns it into a real question.
//
// Precision comes first: a false positive costs a confusing nudge, a false negative only the
// generic one. So a message counts only when its last paragraph ends with a question mark and
// that last sentence is addressed to the human ("should I…", "do you want…", "want me to…",
// "which would you prefer…"). Rhetorical or self-directed questions ("Why does this fail?"
// followed by the answer), questions inside code, and messages that already name a question id
// do not count.

package jobs

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// maxLastMessageRunes caps the agent's last message kept on the job.
const maxLastMessageRunes = 2000

var (
	fencedCode     = regexp.MustCompile("(?s)```.*?```")
	inlineCode     = regexp.MustCompile("`[^`\n]*`")
	questionIDRef  = regexp.MustCompile(`\bq_\d+\b`)
	markdownMarks  = regexp.MustCompile(`[*_>#]+`)
	sentenceEnding = regexp.MustCompile(`[.!?]\s+`)
	// addressed are the openings and phrases that make a question the human's to answer.
	addressed = regexp.MustCompile(`(?i)\b(` +
		`(should|shall|can|could|may|would|do|must) (i|we)\b` +
		`|(do|would|will|did) you\b` +
		`|(are|were) you\b` +
		`|want me to\b|like me to\b|prefer (i|me|we|that)\b` +
		`|which (one|option|approach|of these|would you|do you|should)\b` +
		`|what would you\b|what do you (think|want|prefer)\b|how would you like\b` +
		`|(is|would) (that|this|it) (ok|okay|alright|fine|acceptable)\b` +
		`|(ok|okay|alright|fine) (with you|to proceed|if i)\b` +
		`|let me know\b|please (confirm|advise|let me know|decide|choose)\b` +
		`|your (call|preference|decision|go-ahead|approval)\b` +
		`|any (preference|thoughts|objections)\b` +
		`|(go ahead|proceed)( with (it|this|that))?\s*\?` +
		`)`)
)

// LooksLikeProseQuestion reports whether an agent's final message ends its turn by asking the
// human something in prose.
func LooksLikeProseQuestion(message string) bool {
	text := strings.TrimSpace(message)
	if text == "" || questionIDRef.MatchString(text) {
		return false
	}
	text = fencedCode.ReplaceAllString(text, "\n\n")
	text = inlineCode.ReplaceAllString(text, "code")
	last := lastParagraph(text)
	if last == "" {
		return false
	}
	last = strings.TrimSpace(markdownMarks.ReplaceAllString(last, ""))
	last = strings.TrimRight(last, " )]\"'")
	if !strings.HasSuffix(last, "?") {
		return false
	}
	return addressed.MatchString(lastSentence(last))
}

// lastParagraph is the last block of text separated by a blank line (list items count as one
// paragraph with their lead-in when they are the end of the message).
func lastParagraph(text string) string {
	blocks := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	for i := len(blocks) - 1; i >= 0; i-- {
		if block := strings.TrimSpace(blocks[i]); block != "" {
			return block
		}
	}
	return ""
}

// lastSentence is the paragraph's final sentence (its last line when it is a list).
func lastSentence(paragraph string) string {
	lines := strings.Split(paragraph, "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	parts := sentenceEnding.Split(line, -1)
	sentence := strings.TrimSpace(parts[len(parts)-1])
	if sentence == "" || sentence == "?" {
		sentence = line
	}
	// A short last sentence ("Shall I?") keeps its lead-in for the match.
	if utf8.RuneCountInString(sentence) < 12 && len(parts) > 1 {
		sentence = strings.TrimSpace(parts[len(parts)-2]) + " " + sentence
	}
	return sentence
}

// capRunes cuts text at limit runes (keeping lines), marking the cut.
func capRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit-1]) + "…"
}
