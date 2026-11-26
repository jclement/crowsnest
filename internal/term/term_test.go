// Unit tests for the bridges and the control-stream codec. The pane and TUI bridges run against
// this test binary re-executed as a fake herdr (TestMain), so they need no herdr; the real thing
// is covered by integration_test.go (`mise run test:integration`).

package term

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	xterm "golang.org/x/term"
)

// fakeEnv selects what the re-executed test binary pretends to be.
// It is deliberately not a CROWSNEST_* name: the bridges scrub those from children (R1 H1).
const fakeEnv = "CN_TEST_TERM_FAKE"

func TestMain(m *testing.M) {
	switch os.Getenv(fakeEnv) {
	case "":
		os.Exit(m.Run())
	case "tui":
		fakeTUI()
	case "tui-deaf":
		fakeDeafTUI()
	default:
		fakeControl(os.Getenv(fakeEnv))
	}
}

// ---- Codec ---------------------------------------------------------------------------

func TestDecodeGoldenControlStream(t *testing.T) {
	stream, err := os.ReadFile("testdata/control_stream.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(stream), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("golden stream has %d lines, want 3", len(lines))
	}
	wantFrames := []string{"testdata/frame1.ansi", "testdata/frame2.ansi"}
	for index, path := range wantFrames {
		message, err := DecodeMessage(lines[index])
		if err != nil {
			t.Fatalf("line %d: %v", index+1, err)
		}
		want, _ := os.ReadFile(path)
		if message.Type != TypeFrame || !bytes.Equal(message.Bytes, want) {
			t.Errorf("line %d: type %q bytes %q, want frame %q", index+1, message.Type, message.Bytes, want)
		}
		if message.Full != (index == 0) || message.Seq != uint64(index+1) || message.Width != 80 || message.Height != 24 {
			t.Errorf("line %d: full=%v seq=%d size=%dx%d", index+1, message.Full, message.Seq, message.Width, message.Height)
		}
	}
	closed, err := DecodeMessage(lines[2])
	if err != nil || closed.Type != TypeClosed || closed.Reason != "detached" {
		t.Errorf("closed line = %+v, %v", closed, err)
	}
}

func TestDecodeRejects(t *testing.T) {
	for name, line := range map[string]string{
		"not json":       `{"type":`,
		"no type":        `{"bytes":"AA=="}`,
		"bad base64":     `{"type":"terminal.frame","bytes":"!!!"}`,
		"other encoding": `{"type":"terminal.frame","encoding":"cells","bytes":"AA=="}`,
	} {
		if _, err := DecodeMessage([]byte(line)); err == nil {
			t.Errorf("%s: decoded %s without error", name, line)
		}
	}
	// An unknown type is passed through for the caller to ignore, not an error.
	if message, err := DecodeMessage([]byte(`{"type":"terminal.future"}`)); err != nil || message.Type != "terminal.future" {
		t.Errorf("unknown type = %+v, %v", message, err)
	}
}

func TestEncodeGolden(t *testing.T) {
	for name, tc := range map[string]struct{ got, want string }{
		"input":   {string(EncodeInput([]byte("echo via-control\r"))), `{"type":"terminal.input","bytes":"ZWNobyB2aWEtY29udHJvbA0="}` + "\n"},
		"resize":  {string(EncodeResize(100, 30)), `{"type":"terminal.resize","cols":100,"rows":30}` + "\n"},
		"release": {string(EncodeRelease()), `{"type":"terminal.release"}` + "\n"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", name, tc.got, tc.want)
		}
	}
}

func TestValidSize(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {80, 24}, {MaxCols, MaxRows}} {
		if err := ValidSize(size[0], size[1]); err != nil {
			t.Errorf("ValidSize%v = %v", size, err)
		}
	}
	for _, size := range [][2]int{{0, 24}, {80, 0}, {-1, 5}, {MaxCols + 1, 24}, {80, MaxRows + 1}} {
		if ValidSize(size[0], size[1]) == nil {
			t.Errorf("ValidSize%v accepted", size)
		}
	}
}

// ---- Helpers -------------------------------------------------------------------------

// waitFor reads bridge output until it contains want, failing after timeout.
func waitFor(t *testing.T, bridge Bridge, seen *bytes.Buffer, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !strings.Contains(seen.String(), want) {
		select {
		case chunk, ok := <-bridge.Output():
			if !ok {
				t.Fatalf("output ended (%s) before %q; saw %q", bridge.Reason(), want, seen.String())
			}
			seen.Write(chunk)
		case <-deadline:
			t.Fatalf("timed out waiting for %q; saw %q", want, seen.String())
		}
	}
}

// waitClosed drains output until it closes and returns the reason.
func waitClosed(t *testing.T, bridge Bridge) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-bridge.Output():
			if !ok {
				return bridge.Reason()
			}
		case <-deadline:
			t.Fatal("output did not close")
		}
	}
}

func fakeLauncher(t *testing.T, mode string) HerdrLauncher {
	t.Helper()
	t.Setenv(fakeEnv, mode)
	t.Setenv("HERDR_SOCKET_PATH", "/must/not/leak") // the bridge must scrub this
	t.Setenv("CROWSNEST_PASSWORD", "s3cret")        // and these (R1 H1)
	t.Setenv("ELEVENLABS_API_KEY", "sk-test")
	return HerdrLauncher{Binary: os.Args[0], Session: "crowsnest-test-unit"}
}

// ---- Pane bridge ---------------------------------------------------------------------

func TestPaneBridgeFramesInputResizeAndRelease(t *testing.T) {
	bridge := fakeLauncher(t, "control").Pane("w1:p2")
	if _, err := bridge.Write([]byte("x")); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Write before Start = %v, want ErrNotStarted", err)
	}
	if err := bridge.Start(90, 25); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Start(90, 25); !errors.Is(err, ErrStarted) {
		t.Fatalf("second Start = %v", err)
	}
	var seen bytes.Buffer
	waitFor(t, bridge, &seen, "pane w1:p2 at 90x25 session crowsnest-test-unit takeover=true herdr_env=0")
	if _, err := bridge.Write([]byte("echo crow-1\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, bridge, &seen, `typed "echo crow-1\r"`)
	if err := bridge.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	waitFor(t, bridge, &seen, "resized 120x40")
	if err := bridge.Resize(0, 40); err == nil {
		t.Error("Resize(0, 40) accepted")
	}
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	if reason := waitClosed(t, bridge); reason != "detached" && reason != ReasonClosed {
		t.Errorf("reason after Close = %q", reason)
	}
	_ = bridge.Close() // idempotent
}

func TestPaneBridgeTakenOver(t *testing.T) {
	bridge := fakeLauncher(t, "takeover").Pane("w1:p1")
	if err := bridge.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	if reason := waitClosed(t, bridge); reason != "terminal attach taken over" {
		t.Errorf("reason = %q, want the takeover message", reason)
	}
	_ = bridge.Close()
}

func TestPaneBridgeProcessDies(t *testing.T) {
	bridge := fakeLauncher(t, "die").Pane("w9:p9")
	if err := bridge.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	reason := waitClosed(t, bridge)
	if !strings.Contains(reason, "pane not found") || !strings.Contains(reason, "exit status 3") {
		t.Errorf("reason = %q, want the stderr line and the exit status", reason)
	}
}

func TestPaneBridgeCloseBeforeStart(t *testing.T) {
	bridge := fakeLauncher(t, "control").Pane("w1:p1")
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	if reason := waitClosed(t, bridge); reason != ReasonClosed {
		t.Errorf("reason = %q", reason)
	}
	if err := bridge.Start(80, 24); !errors.Is(err, ErrClosed) {
		t.Errorf("Start after Close = %v, want ErrClosed", err)
	}
}

// fillUntilBlocked writes to bridge from a goroutine until one write has been stuck for a
// while, which means the child's input buffer is full. It returns once that is so, with a
// channel that is closed when the writing goroutine has given up (its write failed).
func fillUntilBlocked(t *testing.T, bridge Bridge) <-chan struct{} {
	t.Helper()
	progress := make(chan struct{}, 1)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		chunk := bytes.Repeat([]byte("x"), 16<<10)
		for {
			if _, err := bridge.Write(chunk); err != nil {
				return
			}
			select {
			case progress <- struct{}{}:
			default:
			}
		}
	}()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-progress:
		case <-time.After(300 * time.Millisecond):
			return writerDone // no write finished for 300 ms: the writer is blocked
		case <-deadline:
			t.Fatal("writes never blocked; the fake is reading after all")
		}
	}
}

// assertCloseUnblocks checks Close returns at once and the child is gone soon after, even
// though a write is stuck on a child that never reads (R1-C01), and that the stuck write itself
// returns rather than leaking its goroutine (on Linux nothing but closing a pollable PTY wakes it).
func assertCloseUnblocks(t *testing.T, bridge Bridge) {
	t.Helper()
	writerDone := fillUntilBlocked(t, bridge)
	closed := make(chan struct{})
	go func() { _ = bridge.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a write to a child that does not read")
	}
	waitClosed(t, bridge)
	select {
	case <-writerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the blocked write never returned after Close")
	}
}

func TestPaneBridgeCloseWithBlockedWrite(t *testing.T) {
	bridge := fakeLauncher(t, "deaf").Pane("w1:p1")
	if err := bridge.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	waitFor(t, bridge, &seen, "deaf")
	assertCloseUnblocks(t, bridge)
}

// fakeControl is the re-executed test binary standing in for `herdr terminal session control`.
func fakeControl(mode string) {
	args := strings.Join(os.Args[1:], " ")
	frame := func(text string) {
		line, _ := json.Marshal(Message{Type: TypeFrame, Encoding: "ansi", Bytes: []byte(text)})
		fmt.Println(string(line))
	}
	closed := func(reason string) {
		line, _ := json.Marshal(Message{Type: TypeClosed, Reason: reason})
		fmt.Println(string(line))
	}
	switch mode {
	case "die":
		fmt.Fprintln(os.Stderr, "herdr: pane not found: w9:p9")
		os.Exit(3)
	case "takeover":
		frame("hello")
		closed("terminal attach taken over")
		os.Exit(0)
	case "deaf":
		// Never reads stdin, so the bridge's writes fill the pipe and block.
		frame("deaf")
		time.Sleep(time.Hour)
	}
	var pane, cols, rows, session string
	_, _ = fmt.Sscanf(strings.Replace(args, "--takeover ", "", 1), "--session %s terminal session control %s --cols %s --rows %s", &session, &pane, &cols, &rows)
	frame(fmt.Sprintf("pane %s at %sx%s session %s takeover=%v herdr_env=%d", pane, cols, rows, session, strings.Contains(args, "--takeover"), countHerdrEnv()))
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var command struct {
			Type       string
			Bytes      []byte
			Cols, Rows int
		}
		if json.Unmarshal(scanner.Bytes(), &command) != nil {
			continue
		}
		switch command.Type {
		case TypeInput:
			frame(fmt.Sprintf("typed %q", command.Bytes))
		case TypeResize:
			frame(fmt.Sprintf("resized %dx%d", command.Cols, command.Rows))
		case TypeRelease:
			closed("detached")
			os.Exit(0)
		}
	}
	os.Exit(0)
}

// countHerdrEnv counts what a child of crowsnest must never inherit: HERDR_* (DESIGN.md 4.10)
// and the secrets the tests plant (R1 H1).
func countHerdrEnv() int {
	count := 0
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HERDR_") || strings.HasPrefix(entry, "CROWSNEST_PASSWORD=") || strings.HasPrefix(entry, "ELEVENLABS_") {
			count++
		}
	}
	return count
}

// ---- TUI bridge ----------------------------------------------------------------------

func TestTUIBridgePTY(t *testing.T) {
	bridge := fakeLauncher(t, "tui").TUI()
	if err := bridge.Start(100, 30); err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	waitFor(t, bridge, &seen, "tui session crowsnest-test-unit tty=true TERM=xterm-256color herdr_env=0 size=100x30")
	if _, err := bridge.Write([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, bridge, &seen, "got hello")
	if err := bridge.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	waitFor(t, bridge, &seen, "size=120x40")
	_ = bridge.Close()
	if reason := waitClosed(t, bridge); reason == "" {
		t.Error("no reason after Close")
	}
}

func TestTUIBridgeClientExits(t *testing.T) {
	bridge := fakeLauncher(t, "tui").TUI()
	if err := bridge.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	waitFor(t, bridge, &seen, "size=80x24")
	_, _ = bridge.Write([]byte("quit\r"))
	if reason := waitClosed(t, bridge); reason != "detached" {
		t.Errorf("reason = %q, want detached (a clean client exit)", reason)
	}
	_ = bridge.Close()
}

func TestTUIBridgeCloseWithBlockedWrite(t *testing.T) {
	bridge := fakeLauncher(t, "tui-deaf").TUI()
	if err := bridge.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	waitFor(t, bridge, &seen, "deaf")
	go func() { // keep draining output so only the input side is stuck
		for range bridge.Output() {
		}
	}()
	assertCloseUnblocks(t, bridge)
}

// fakeDeafTUI is a client in raw mode that never reads its terminal.
func fakeDeafTUI() {
	_, _ = xterm.MakeRaw(int(os.Stdin.Fd()))
	fmt.Print("deaf\r\n")
	time.Sleep(time.Hour)
}

// fakeTUI stands in for the herdr client: it reports what it was given and echoes lines.
func fakeTUI() {
	fd := int(os.Stdin.Fd())
	_, _ = xterm.MakeRaw(fd)
	printSize := func() {
		cols, rows, _ := xterm.GetSize(fd)
		fmt.Printf("size=%dx%d\r\n", cols, rows)
	}
	fmt.Printf("tui session %s tty=%v TERM=%s herdr_env=%d ", strings.Join(os.Args[2:], " "), xterm.IsTerminal(fd), os.Getenv("TERM"), countHerdrEnv())
	printSize()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			printSize()
		}
	}()
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\r')
		if err != nil {
			os.Exit(1)
		}
		line = strings.TrimSuffix(line, "\r")
		if line == "quit" {
			os.Exit(0)
		}
		fmt.Printf("got %s\r\n", line)
	}
}

// ---- Demo bridge ---------------------------------------------------------------------

func TestDemoBridge(t *testing.T) {
	bridge := DemoLauncher{}.Pane("w1:p1")
	if err := bridge.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	waitFor(t, bridge, &seen, "pane w1:p1 (demo) · 80x24")
	waitFor(t, bridge, &seen, "  "+DemoGlyphs+"\r\n")
	_, _ = bridge.Write([]byte("glyphs\r"))
	waitFor(t, bridge, &seen, "glyphs\r\n"+DemoGlyphs+"\r\n")
	// An arrow key is swallowed, backspace edits, Enter answers.
	_, _ = bridge.Write([]byte("hx\x1b[A\x7fi\r"))
	waitFor(t, bridge, &seen, "you typed: \x1b[1mhi\x1b[0m")
	_ = bridge.Resize(132, 43)
	_, _ = bridge.Write([]byte("size\r"))
	waitFor(t, bridge, &seen, "132 columns x 43 rows")
	_, _ = bridge.Write([]byte("exit\r"))
	if reason := waitClosed(t, bridge); reason != "exited" {
		t.Errorf("reason = %q, want exited", reason)
	}
	_ = bridge.Close()
	if _, err := bridge.Write([]byte("more")); err != nil {
		t.Errorf("Write after exit = %v", err)
	}
}

func TestDemoBridgeCloseWhileTyping(t *testing.T) {
	bridge := DemoLauncher{}.TUI()
	if err := bridge.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	// Nobody reads: the buffer fills, emits queue up, and Close must still finish promptly.
	for range 200 {
		_, _ = bridge.Write([]byte("a lot of typing\r"))
	}
	done := make(chan struct{})
	go func() { _ = bridge.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung with an unread buffer")
	}
	if reason := waitClosed(t, bridge); reason != ReasonClosed {
		t.Errorf("reason = %q", reason)
	}
}
