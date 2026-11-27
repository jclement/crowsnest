// Composing the one message a send carries (DESIGN.md 6.21): every pending note, in file and line
// order, each as where it is, the quoted code and the note, with the human's overall note first.
//
// The quotes are code the agent wrote, coming back through the human to the agent's terminal, and
// the notes are the human's: both are text crowsnest types. The terminal path (orch.Prompt,
// termtext.Block) drops invisible characters, which would silently change a quote, so here every
// invisible or direction-changing character is written out as ⟨U+XXXX⟩ instead (R7-S3: never
// silent), words that mix Latin with look-alike letters are named, and a line that starts with a
// bracket cannot pass for a `[crowsnest …]` header (termtext.DisarmHeaders).

package changes

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jclement/crowsnest/internal/termtext"
)

// Composed is a message body and what it had to reveal.
type Composed struct {
	Text   string
	Hidden int
	Mixed  []string
}

// Compose writes notes into one message body. Outdated notes say they were written on an earlier
// version of the file.
func Compose(comments []Comment, overall string) Composed {
	ordered := append([]Comment(nil), comments...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.LineStart < b.LineStart
	})
	var out strings.Builder
	report := &hiddenTally{}
	noun := "comments"
	if len(ordered) == 1 {
		noun = "comment"
	}
	fmt.Fprintf(&out, "Review comments on your changes: %d %s from the diff view. Address each one; say what you changed for each.\n", len(ordered), noun)
	if note := strings.TrimSpace(overall); note != "" {
		out.WriteString("\nOverall: " + indentRest(report.reveal(note), "  ") + "\n")
	}
	for i, comment := range ordered {
		out.WriteString("\n" + strconv.Itoa(i+1) + ". " + report.reveal(comment.Path) + ", " + where(comment) + ":\n")
		if comment.Status == StatusOutdated {
			out.WriteString("   (written on an earlier version of this file, which has changed since)\n")
		}
		if comment.Quote != "" {
			for _, line := range strings.Split(comment.Quote, "\n") {
				out.WriteString("   > " + report.reveal(line) + "\n")
			}
		}
		out.WriteString("   Comment: " + indentRest(report.reveal(comment.Body), "   ") + "\n")
	}
	notes := []string{}
	if report.count > 0 {
		notes = append(notes, fmt.Sprintf("%d invisible character(s) in the text above are written as ⟨U+XXXX⟩.", report.count))
	}
	if len(report.mixed) > 0 {
		notes = append(notes, "These words mix Latin with look-alike letters from another alphabet: "+strings.Join(report.mixed, ", ")+".")
	}
	if len(notes) > 0 {
		out.WriteString("\nNote: " + strings.Join(notes, " ") + "\n")
	}
	return Composed{Text: termtext.DisarmHeaders(strings.TrimRight(out.String(), "\n")), Hidden: report.count, Mixed: report.mixed}
}

// where says which lines a note is on, in words.
func where(comment Comment) string {
	switch {
	case comment.Side == SideFile:
		return "the whole file"
	case comment.LineStart == comment.LineEnd:
		return fmt.Sprintf("line %d (%s)", comment.LineStart, sideWord(comment.Side))
	default:
		return fmt.Sprintf("lines %d–%d (%s)", comment.LineStart, comment.LineEnd, sideWord(comment.Side))
	}
}

func sideWord(side string) string {
	if side == SideOld {
		return "removed side, before your change"
	}
	return "your version"
}

// indentRest indents every line after the first, so a multi-line note stays under its number.
func indentRest(text, indent string) string {
	return strings.ReplaceAll(text, "\n", "\n"+indent)
}

// hiddenTally writes out what a text hides and counts it.
type hiddenTally struct {
	count int
	mixed []string
}

// reveal returns text with every invisible character written as ⟨U+XXXX⟩, and notes words that
// mix scripts. Line breaks stay (the caller lays the lines out); tabs and spaces stay.
func (t *hiddenTally) reveal(text string) string {
	var out strings.Builder
	for _, r := range text {
		if r == '\r' {
			continue // a CRLF file's line ends: not hidden content, and the terminal path drops them anyway
		}
		if r != '\n' && r != '\t' && invisibleRune(r) {
			t.count++
			fmt.Fprintf(&out, "⟨U+%04X⟩", r)
			continue
		}
		out.WriteRune(r)
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || r == '_') }) {
		if mixesScripts(word) && !contains(t.mixed, word) && len(t.mixed) < 20 {
			t.mixed = append(t.mixed, word)
		}
	}
	return out.String()
}

// invisibleRune mirrors web/src/lib/hidden.ts: Unicode's format characters (bidi controls,
// zero-width characters, BOM, soft hyphen, tags), the line and paragraph separators, the fillers
// that render as nothing, the combining grapheme joiner; plus the controls a terminal would act
// on. Emoji variation selectors are not format characters and stay.
func invisibleRune(r rune) bool {
	switch r {
	case 0x2028, 0x2029, 0x115F, 0x1160, 0x3164, 0xFFA0, 0x180E, 0x034F:
		return true
	}
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Cc, r)
}

func mixesScripts(word string) bool {
	latin, other := false, false
	for _, r := range word {
		switch {
		case unicode.Is(unicode.Latin, r):
			latin = true
		case unicode.Is(unicode.Cyrillic, r) || unicode.Is(unicode.Greek, r) || unicode.Is(unicode.Armenian, r):
			other = true
		}
	}
	return latin && other
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
