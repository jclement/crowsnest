// The tailer: append-only reads of one transcript file into a Conversation.
//
// Claude only ever appends to a transcript, so each poll reads from where the last one stopped
// and hands complete lines to the normaliser; a trailing partial line waits for its newline. Two
// things break the append-only assumption and both restart the conversation from the top of the
// file: the file shrinking (truncated) and the path naming a different file (rotated or
// replaced). Polling rather than file notifications keeps this simple and portable; a stat every
// second is nothing next to what Claude itself does.

package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	// readChunk is how much is read per call; a session-start transcript is already ~700 KB.
	readChunk = 256 << 10
	// maxLineBytes drops absurd lines (a giant attachment) instead of buffering them forever.
	// Nothing the normaliser reads is anywhere near this size.
	maxLineBytes = 16 << 20
)

// Tailer follows one transcript file.
type Tailer struct {
	path      string
	sessionID string
	conv      *Conversation
	norm      *Normaliser
	offset    int64
	info      os.FileInfo // the file as last opened, to notice a replacement
	partial   []byte      // an incomplete last line
	skipping  bool        // inside an over-long line, discarding until its newline
}

// NewTailer returns a tailer for path that feeds conv. Nothing is read until Poll.
func NewTailer(path, sessionID string, conv *Conversation) *Tailer {
	return &Tailer{path: path, sessionID: sessionID, conv: conv, norm: NewNormaliser()}
}

// Path is the file being followed.
func (t *Tailer) Path() string { return t.path }

// Poll reads whatever was appended since the last call and reports whether the conversation
// changed. A file that has vanished is an error wrapping os.ErrNotExist; the conversation keeps
// what it had.
func (t *Tailer) Poll() (bool, error) {
	file, err := os.Open(t.path)
	if err != nil {
		return false, fmt.Errorf("opening transcript: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("reading transcript size: %w", err)
	}
	reset := false
	if t.info != nil && (!os.SameFile(t.info, info) || info.Size() < t.offset) {
		t.restart()
		reset = true
	}
	t.info = info
	if info.Size() == t.offset {
		return reset, nil
	}
	if _, err := file.Seek(t.offset, io.SeekStart); err != nil {
		return reset, fmt.Errorf("seeking in transcript: %w", err)
	}
	changed, err := t.read(file)
	return changed || reset, err
}

// restart forgets everything read so far: the next read starts at the top of the file.
func (t *Tailer) restart() {
	t.offset, t.partial, t.skipping = 0, nil, false
	t.norm = NewNormaliser()
	t.conv.Reset(SourceTranscript, t.sessionID)
}

func (t *Tailer) read(file io.Reader) (bool, error) {
	changed := false
	buffer := make([]byte, readChunk)
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			t.offset += int64(n)
			changed = t.consume(buffer[:n]) || changed
		}
		if errors.Is(err, io.EOF) {
			return changed, nil
		}
		if err != nil {
			return changed, fmt.Errorf("reading transcript: %w", err)
		}
	}
}

// consume splits data into lines, carrying a partial line over to the next call.
func (t *Tailer) consume(data []byte) bool {
	changed := false
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			t.keepPartial(data)
			return changed
		}
		line := data[:newline]
		data = data[newline+1:]
		if t.skipping {
			t.skipping = false
			continue
		}
		if len(t.partial) > 0 {
			line = append(t.partial, line...)
			t.partial = nil
		}
		if upserts := t.norm.Line(line); len(upserts) > 0 {
			changed = t.conv.Apply(upserts) || changed
		}
	}
	return changed
}

func (t *Tailer) keepPartial(data []byte) {
	if t.skipping {
		return
	}
	if len(t.partial)+len(data) > maxLineBytes {
		t.partial, t.skipping = nil, true
		return
	}
	t.partial = append(t.partial, data...)
}
