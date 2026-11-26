// A small ANSI parser: just enough of ECMA-48 to recover the characters on a screen and, for
// each, whether it was drawn faint, grey or reverse video. Draft detection needs those three
// attributes (ghost text is dim, the cursor is reverse video); everything else (colours, cursor
// movement, OSC titles and hyperlinks) is consumed and ignored.

package fleet

import (
	"strconv"
	"strings"
)

// styledCell is one printed character and the attributes it was drawn with.
type styledCell struct {
	r       rune
	faint   bool // SGR 2
	grey    bool // foreground bright black (SGR 90) or a 256-colour grey
	inverse bool // SGR 7
}

// ghost reports whether the cell is placeholder styling rather than typed text.
func (c styledCell) ghost() bool { return c.faint || c.grey }

// styledLine is one screen line.
type styledLine struct {
	cells []styledCell
}

func (l styledLine) plain() string {
	var b strings.Builder
	for _, cell := range l.cells {
		b.WriteRune(cell.r)
	}
	return b.String()
}

// StripANSI returns text with every escape sequence removed.
func StripANSI(text string) string {
	lines := parseANSI(text)
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = line.plain()
	}
	return strings.Join(plain, "\n")
}

// sgrState is the subset of graphic rendition that matters here.
type sgrState struct {
	faint, grey, inverse bool
}

// parseANSI splits text into lines of styled cells. Attributes carry across lines, as they do in
// a terminal.
func parseANSI(text string) []styledLine {
	var (
		lines = []styledLine{{}}
		state sgrState
		runes = []rune(text)
	)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\x1b':
			i = consumeEscape(runes, i, &state)
		case r == '\n':
			lines = append(lines, styledLine{})
		case r == '\r' || r < 0x20 || r == 0x7f:
			// carriage returns and other controls draw nothing
		default:
			current := &lines[len(lines)-1]
			current.cells = append(current.cells, styledCell{r: r, faint: state.faint, grey: state.grey, inverse: state.inverse})
		}
	}
	return lines
}

// consumeEscape skips the escape sequence starting at runes[start] (an ESC), applying it to
// state when it is SGR, and returns the index of its last rune.
func consumeEscape(runes []rune, start int, state *sgrState) int {
	if start+1 >= len(runes) {
		return start
	}
	switch runes[start+1] {
	case '[': // CSI: parameters and intermediates, then a final byte in @..~
		end := start + 2
		for end < len(runes) && (runes[end] < 0x40 || runes[end] > 0x7e) {
			end++
		}
		if end >= len(runes) {
			return len(runes) - 1
		}
		if runes[end] == 'm' {
			applySGR(string(runes[start+2:end]), state)
		}
		return end
	case ']', 'P', '_', '^': // OSC, DCS, APC, PM: until BEL or ST (ESC \)
		for end := start + 2; end < len(runes); end++ {
			if runes[end] == '\a' {
				return end
			}
			if runes[end] == '\x1b' && end+1 < len(runes) && runes[end+1] == '\\' {
				return end + 1
			}
		}
		return len(runes) - 1
	default: // a two-character escape (ESC 7, ESC =, …)
		return start + 1
	}
}

// applySGR updates state from an SGR parameter string such as "0;2;38;5;245".
func applySGR(params string, state *sgrState) {
	if params == "" {
		*state = sgrState{}
		return
	}
	fields := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	for i := 0; i < len(fields); i++ {
		code, err := strconv.Atoi(fields[i])
		if err != nil {
			continue
		}
		switch {
		case code == 0:
			*state = sgrState{}
		case code == 2:
			state.faint = true
		case code == 22:
			state.faint = false
		case code == 7:
			state.inverse = true
		case code == 27:
			state.inverse = false
		case code == 90:
			state.grey = true
		case code == 39 || (code >= 30 && code <= 37) || (code >= 91 && code <= 97):
			state.grey = false
		case code == 38:
			i = applyExtendedColour(fields, i, state)
		case code == 48:
			i = skipExtendedColour(fields, i)
		}
	}
}

// applyExtendedColour handles 38;5;n and 38;2;r;g;b, marking greys, and returns the index of the
// last field consumed.
func applyExtendedColour(fields []string, i int, state *sgrState) int {
	if i+2 < len(fields) && fields[i+1] == "5" {
		n, _ := strconv.Atoi(fields[i+2])
		state.grey = n == 8 || (n >= 238 && n <= 250)
		return i + 2
	}
	if i+4 < len(fields) && fields[i+1] == "2" {
		r, _ := strconv.Atoi(fields[i+2])
		g, _ := strconv.Atoi(fields[i+3])
		b, _ := strconv.Atoi(fields[i+4])
		state.grey = isGrey(r, g, b)
		return i + 4
	}
	return i
}

func skipExtendedColour(fields []string, i int) int {
	if i+1 < len(fields) && fields[i+1] == "5" {
		return i + 2
	}
	if i+1 < len(fields) && fields[i+1] == "2" {
		return i + 4
	}
	return i
}

// isGrey: a truecolour mid grey, the kind used for placeholder text (channels within 12 of each
// other and between 80 and 180).
func isGrey(r, g, b int) bool {
	low, high := min(r, g, b), max(r, g, b)
	return high-low <= 12 && low >= 80 && high <= 180
}
