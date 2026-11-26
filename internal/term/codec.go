// The NDJSON codec for `herdr terminal session control` (verified against herdr 0.9.5 in a
// throwaway session; DESIGN.md section 10). stdout carries one JSON object per line:
//
//	{"type":"terminal.frame","encoding":"ansi","full":true,"width":80,"height":24,"seq":1,"bytes":"<base64>"}
//	{"type":"terminal.closed","reason":"detached"}
//
// A full frame is a complete redraw (it starts by clearing); later frames are incremental ANSI.
// Writing every frame's bytes to a terminal emulator in order renders the pane. stdin takes:
//
//	{"type":"terminal.input","bytes":"<base64>"}
//	{"type":"terminal.resize","cols":100,"rows":30}
//	{"type":"terminal.release"}
//
// herdr also accepts terminal.scroll and terminal.mouse, which crowsnest does not send. An
// unknown or malformed stdin line is ignored by herdr with a note on its stderr.

package term

import (
	"encoding/json"
	"fmt"
)

// Message types on the control stream.
const (
	TypeFrame   = "terminal.frame"
	TypeClosed  = "terminal.closed"
	TypeInput   = "terminal.input"
	TypeResize  = "terminal.resize"
	TypeRelease = "terminal.release"
)

// Message is one decoded line of the control stream's stdout.
type Message struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding,omitempty"` // "ansi" for frames
	Full     bool   `json:"full,omitempty"`     // a complete redraw rather than an increment
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Seq      uint64 `json:"seq,omitempty"`
	// Bytes is the frame's terminal output. encoding/json decodes the base64 for us.
	Bytes  []byte `json:"bytes,omitempty"`
	Reason string `json:"reason,omitempty"` // why a terminal.closed happened
}

// DecodeMessage parses one stdout line. Frames in an encoding other than ANSI are refused,
// because their bytes would not mean anything to xterm.js.
func DecodeMessage(line []byte) (Message, error) {
	var message Message
	if err := json.Unmarshal(line, &message); err != nil {
		return Message{}, fmt.Errorf("decoding control stream line: %w", err)
	}
	switch message.Type {
	case TypeFrame:
		if message.Encoding != "" && message.Encoding != "ansi" {
			return Message{}, fmt.Errorf("frame in unsupported encoding %q", message.Encoding)
		}
	case TypeClosed:
	case "":
		return Message{}, fmt.Errorf("control stream line has no type")
	}
	return message, nil
}

// inputCommand, resizeCommand and releaseCommand are the stdin lines.
type inputCommand struct {
	Type  string `json:"type"`
	Bytes []byte `json:"bytes"` // base64 on the wire
}

type resizeCommand struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type releaseCommand struct {
	Type string `json:"type"`
}

// EncodeInput returns the stdin line (with its newline) that types p into the pane.
func EncodeInput(p []byte) []byte {
	return encodeLine(inputCommand{Type: TypeInput, Bytes: p})
}

// EncodeResize returns the stdin line that resizes the pane's controller view.
func EncodeResize(cols, rows int) []byte {
	return encodeLine(resizeCommand{Type: TypeResize, Cols: cols, Rows: rows})
}

// EncodeRelease returns the stdin line that lets go of the pane (herdr answers with
// terminal.closed "detached" and exits).
func EncodeRelease() []byte {
	return encodeLine(releaseCommand{Type: TypeRelease})
}

func encodeLine(command any) []byte {
	line, err := json.Marshal(command)
	if err != nil {
		panic(fmt.Sprintf("term: encoding %T cannot fail: %v", command, err))
	}
	return append(line, '\n')
}
