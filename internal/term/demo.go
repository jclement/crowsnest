// The demo bridge, for --demo: a tiny built-in terminal toy written in Go, so the UI and the
// hermetic E2E can exercise the whole WebSocket path with no herdr and no process at all. It
// never starts a shell (a --demo instance must be harmless to expose), it just draws a coloured
// banner, echoes what you type with simple line editing, and answers a few words. The banner
// includes a glyph test line (DemoGlyphs) so the terminal's font chain can be checked by eye and
// by the hermetic E2E (web/e2e/hermetic/terminal-fonts.spec.ts).

package term

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// demoPrompt is drawn before every input line.
const demoPrompt = "\x1b[1;36mdemo\x1b[0m \x1b[2m$\x1b[0m "

// DemoGlyphs is one character or run from each face of the terminal's font chain (DESIGN.md 12,
// "Terminal font"), ending in a "|" whose column shows whether the cells stayed on the grid:
// powerline separators and the branch symbol, devicons (Node.js, Rust) and Font Awesome's GitHub
// mark from the Nerd Font symbols; box drawing and ⌘ from DejaVu Sans Mono; braille and a
// legacy-computing sextant from Noto Sans Symbols 2; a contour integral and a maths italic x
// from Noto Sans Math; and an emoji, which no face carries (the system's colour emoji font draws
// it) and which takes two cells. The E2E reads it from the screen; change both together.
const DemoGlyphs = "glyphs \ue0a0 \ue0b0\ue0b1\ue0b2\ue0b3 \ue718 \ue7a8 \uf09b ┌─┐│⌘ ⠃⡇🬀 ∮𝑥 😀 |"

// maxDemoLine caps the input line; nobody needs more to try a toy.
const maxDemoLine = 512

// DemoLauncher builds demo bridges for both routes.
type DemoLauncher struct{}

// TUI is the demo standing in for the herdr TUI.
func (DemoLauncher) TUI() Bridge { return newDemoBridge("the herdr session (demo)") }

// Pane is the demo standing in for one pane.
func (DemoLauncher) Pane(paneID string) Bridge { return newDemoBridge("pane " + paneID + " (demo)") }

// demoBridge implements Bridge with an in-process line editor.
type demoBridge struct {
	*stream
	title string

	mu      sync.Mutex
	pending sync.WaitGroup // emits that did not fit the buffer and are still being delivered
	started bool
	ended   bool
	cols    int
	rows    int
	line    []rune
	escape  []byte // an escape sequence being skipped (arrow keys and the like)
}

func newDemoBridge(title string) *demoBridge {
	return &demoBridge{stream: newStream(), title: title}
}

// Start draws the banner and the first prompt.
func (b *demoBridge) Start(cols, rows int) error {
	if err := ValidSize(cols, rows); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return ErrStarted
	}
	if b.stopping() {
		return ErrClosed
	}
	b.started, b.cols, b.rows = true, cols, rows
	b.emitLocked(demoBanner(b.title, cols, rows) + demoPrompt)
	return nil
}

func demoBanner(title string, cols, rows int) string {
	return "\x1b[2J\x1b[H" +
		"\x1b[1;35m  ▲ crowsnest\x1b[0m  demo terminal\r\n" +
		fmt.Sprintf("\x1b[2m  standing in for %s · %dx%d\x1b[0m\r\n", title, cols, rows) +
		"\x1b[2m  No shell runs here. Type something and press Enter; try \x1b[0m\x1b[33mcolors\x1b[0m\x1b[2m, \x1b[0m" +
		"\x1b[33msize\x1b[0m\x1b[2m, \x1b[0m\x1b[33mglyphs\x1b[0m\x1b[2m or \x1b[0m\x1b[33mexit\x1b[0m\x1b[2m.\x1b[0m\r\n" +
		"  " + DemoGlyphs + "\r\n\r\n"
}

// Write feeds keystrokes to the line editor.
func (b *demoBridge) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started {
		return 0, ErrNotStarted
	}
	if b.ended {
		return len(p), nil
	}
	var echo strings.Builder
	for _, r := range string(p) {
		b.keyLocked(r, &echo)
		if b.ended {
			break
		}
	}
	if echo.Len() > 0 {
		b.emitLocked(echo.String())
	}
	return len(p), nil
}

// keyLocked handles one rune of input, appending what to draw to echo.
func (b *demoBridge) keyLocked(r rune, echo *strings.Builder) {
	if len(b.escape) > 0 || r == 0x1b {
		b.skipEscape(r)
		return
	}
	switch {
	case r == '\r' || r == '\n':
		echo.WriteString("\r\n" + b.answerLocked(strings.TrimSpace(string(b.line))))
		b.line = b.line[:0]
		if !b.ended {
			echo.WriteString(demoPrompt)
		}
	case r == 0x7f || r == 0x08:
		if len(b.line) > 0 {
			b.line = b.line[:len(b.line)-1]
			echo.WriteString("\b \b")
		}
	case r == 0x03: // Ctrl+C
		b.line = b.line[:0]
		echo.WriteString("^C\r\n" + demoPrompt)
	case r < 0x20:
		// other control keys do nothing in the toy
	case len(b.line) < maxDemoLine:
		b.line = append(b.line, r)
		echo.WriteRune(r)
	}
}

// skipEscape swallows an escape sequence: ESC, then optionally [ or O, then parameters, ending
// at a final byte in @..~ (so arrows, Home and function keys do not print garbage).
func (b *demoBridge) skipEscape(r rune) {
	b.escape = utf8.AppendRune(b.escape, r)
	if len(b.escape) == 1 {
		return
	}
	if len(b.escape) == 2 && (r == '[' || r == 'O') {
		return
	}
	if (r >= '@' && r <= '~') || len(b.escape) > 16 {
		b.escape = b.escape[:0]
	}
}

// answerLocked is the toy's reply to a line.
func (b *demoBridge) answerLocked(line string) string {
	switch strings.ToLower(line) {
	case "":
		return ""
	case "exit", "logout":
		b.ended = true
		b.emitLocked("\x1b[2mbye\x1b[0m\r\n")
		go b.finishDemo("exited")
		return ""
	case "size":
		return fmt.Sprintf("%d columns x %d rows\r\n", b.cols, b.rows)
	case "colors", "colours":
		return demoPalette()
	case "glyphs":
		return DemoGlyphs + "\r\n"
	default:
		return fmt.Sprintf("you typed: \x1b[1m%s\x1b[0m\r\n", line)
	}
}

func demoPalette() string {
	var palette strings.Builder
	for colour := range 16 {
		fmt.Fprintf(&palette, "\x1b[48;5;%dm  \x1b[0m", colour)
		if colour == 7 {
			palette.WriteString("\r\n")
		}
	}
	palette.WriteString("\r\n")
	return palette.String()
}

// Resize records the size for the `size` command; the toy does not redraw.
func (b *demoBridge) Resize(cols, rows int) error {
	if err := ValidSize(cols, rows); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started {
		return ErrNotStarted
	}
	b.cols, b.rows = cols, rows
	return nil
}

// emitLocked sends output without holding up the caller: the stream's buffer absorbs a typing
// burst, and a stuck reader only costs a goroutine until Close.
func (b *demoBridge) emitLocked(text string) {
	select {
	case b.out <- []byte(text):
	default:
		b.pending.Go(func() { b.send([]byte(text)) })
	}
}

// finishDemo ends the stream after `exit`, unless Close got there first.
func (b *demoBridge) finishDemo(reason string) {
	b.setReason(reason) // recorded before stop, so it wins over "closed"
	if b.stop() {
		b.closeOutput()
	}
}

// Close ends the toy.
func (b *demoBridge) Close() error {
	if b.stop() {
		b.closeOutput()
	}
	return nil
}

// closeOutput closes Output once nothing more can be sent to it: the bridge is stopped (so
// pending sends give up), no new emit can start (ended, under mu), and pending ones are done.
func (b *demoBridge) closeOutput() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ended = true
	b.pending.Wait()
	close(b.out)
}
