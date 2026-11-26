// Names and the typed line. A file's name comes from a browser or an agent, so it is only ever a
// label: cleaned for display, and turned into a conservative file name (letters, digits, dot,
// dash, underscore) for the copy an agent reads, prefixed with the attachment's id so two
// "image.png" pastes never collide and the web app can find the attachment from the path.

package attach

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jclement/crowsnest/internal/termtext"
)

const (
	maxNameRunes     = 120 // a display name
	maxCopyNameRunes = 60  // the name part of an agent's copy
	defaultName      = "file"
)

// CleanName makes a display name: the last path element only (a browser may send C:\x\y.png),
// no control or format characters, at most maxNameRunes.
func CleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(termtext.Clean(name))
	if !utf8.ValidString(name) || name == "" || name == "." || name == ".." {
		return defaultName
	}
	if runes := []rune(name); len(runes) > maxNameRunes {
		ext := filepath.Ext(name)
		if utf8.RuneCountInString(ext) > 12 {
			ext = ""
		}
		name = string(runes[:maxNameRunes-utf8.RuneCountInString(ext)]) + ext
	}
	return name
}

// CopyName is the file name of an agent's copy: <id>-<safe name>.
func CopyName(id, name string) string {
	return id + "-" + safeFileName(name)
}

// safeFileName keeps letters, digits, '.', '-' and '_' (ASCII), turns runs of anything else into
// one '-', drops leading dots and dashes (no hidden files, nothing that reads as a flag) and keeps
// the extension when it has to shorten. The stem and the extension are cleaned apart, so a name
// with no ASCII in its stem ("日本語.txt") becomes "file.txt" rather than losing its extension.
func safeFileName(name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" { // ".hidden": all extension
		stem, ext = ext, ""
	}
	ext = strings.Trim(safeRun(strings.TrimPrefix(ext, ".")), "-.")
	if len(ext) > 12 {
		ext = ""
	}
	if ext != "" {
		ext = "." + ext
	}
	stem = strings.Trim(safeRun(stem), "-.")
	if stem == "" {
		stem = defaultName
	}
	if len(stem)+len(ext) > maxCopyNameRunes {
		stem = strings.TrimRight(stem[:maxCopyNameRunes-len(ext)], ".-")
	}
	return stem + ext
}

// safeRun maps text to the safe alphabet, one '-' for each run of anything else.
func safeRun(text string) string {
	var b strings.Builder
	dash := false
	for _, r := range text {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return b.String()
}

// FormatSize renders a byte count the way the typed line and the UI do: "812 B", "240 KB",
// "3.4 MB" (binary multiples, as file managers show them).
func FormatSize(size int64) string {
	const unit = 1024
	switch {
	case size < unit:
		return fmt.Sprintf("%d B", size)
	case size < unit*unit:
		return fmt.Sprintf("%d KB", (size+unit/2)/unit)
	case size < unit*unit*unit:
		return trimZero(fmt.Sprintf("%.1f", float64(size)/(unit*unit))) + " MB"
	}
	return trimZero(fmt.Sprintf("%.1f", float64(size)/(unit*unit*unit))) + " GB"
}

func trimZero(text string) string { return strings.TrimSuffix(text, ".0") }

// LinePrefix starts every typed attachment line.
const LinePrefix = "Attached: "

// Line is the one line typed for a delivered file: "Attached: <path> (image/png, 240 KB)". It
// goes through termtext.Clean, as everything typed into a terminal must.
func Line(attachment Attachment) string {
	return termtext.Clean(fmt.Sprintf("%s%s (%s, %s)", LinePrefix, attachment.Path, attachment.Type, FormatSize(attachment.Size)))
}

// Lines are Line for each file, in order.
func Lines(attachments []Attachment) []string {
	out := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, Line(attachment))
	}
	return out
}

// WithLines appends the attachment lines to a message, one per line after the text (or alone when
// the text is empty: a pasted screenshot with nothing to say).
func WithLines(text string, attachments []Attachment) string {
	lines := Lines(attachments)
	text = strings.TrimRight(text, " \t\r\n")
	if len(lines) == 0 {
		return text
	}
	if text == "" {
		return strings.Join(lines, "\n")
	}
	return text + "\n" + strings.Join(lines, "\n")
}
