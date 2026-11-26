// Package transcript turns Claude Code's JSONL transcripts into the conversation the web app
// shows (DESIGN.md section 7.2): it finds an agent's transcript file, tails it as Claude appends
// to it, and normalises the records into a small message model.
//
// The transcript format is not a public contract, so everything here is tolerant: a record type
// or block shape it does not know is skipped, a malformed line is skipped, and nothing in a
// transcript can make the parser fail. The golden tests under testdata/ pin real transcripts
// (generated with Claude Code 2.1.332, scrubbed) so a format change shows up as a diff.
//
// The pieces, from the bottom: Normaliser (one record in, message upserts out), Conversation
// (the ordered messages with revision cursors for paging), Tailer (append-only reads of one
// file, surviving truncation and rotation), and Manager (one tailer per agent, following the
// session id herdr reports, with a pane-text fallback when no transcript exists).
package transcript

import "time"

// Kind is what a message is, which decides how the UI draws it.
type Kind string

// Message kinds.
const (
	KindUser      Kind = "user"      // something typed or sent to the agent
	KindAssistant Kind = "assistant" // the agent's markdown reply
	KindTool      Kind = "tool"      // one tool call and, once it arrives, its result
	KindThinking  Kind = "thinking"  // extended thinking; the UI hides it by default
	KindPane      Kind = "pane"      // fallback: the pane's recent text, preformatted
)

// User-message tags, taken from the header crowsnest puts on what it types (DESIGN.md 7.1).
const (
	TagPoke    = "poke"    // "[crowsnest poke …]"
	TagVoice   = "voice"   // "[crowsnest voice: …]"
	TagAnswer  = "answer"  // "[crowsnest answer q_7] …"
	TagCommand = "command" // a slash command or its local output
)

// Message is one entry of a conversation.
//
// ID is stable for the life of a conversation, and Index is its position, also stable. Seq is a
// revision: it is assigned when the message first appears and assigned again (larger) whenever
// the message changes, which happens to a tool card when its result arrives. A client that has
// seen everything up to some seq asks for "after" it and merges what comes back by ID.
type Message struct {
	ID    string    `json:"id"`
	Index int       `json:"index"`
	Seq   uint64    `json:"seq"`
	Kind  Kind      `json:"kind"`
	Tag   string    `json:"tag,omitempty"`  // user messages only: poke, voice, answer, command
	Text  string    `json:"text,omitempty"` // markdown for assistant, plain for the rest
	Tool  *ToolCall `json:"tool,omitempty"` // KindTool only
	At    time.Time `json:"at"`
}

// ToolCall is a tool card: the call, and its result once Claude records it.
type ToolCall struct {
	ID      string `json:"id"`      // Claude's tool_use id
	Name    string `json:"name"`    // Bash, Read, Edit, an MCP tool name, …
	Summary string `json:"summary"` // one short line drawn from the input (the command, the file)
	Input   string `json:"input"`   // the input as indented JSON, truncated
	// Result is the tool's output as text, ANSI removed and truncated. Empty until Done.
	Result          string `json:"result,omitempty"`
	ResultTruncated bool   `json:"result_truncated,omitempty"`
	IsError         bool   `json:"is_error,omitempty"`
	Done            bool   `json:"done"`
}

// Truncation limits. A transcript can hold megabytes of tool output; the browser needs enough to
// recognise what happened, and the terminal is there for the rest.
const (
	maxSummaryRunes = 120
	maxInputBytes   = 2 << 10
	maxResultBytes  = 4 << 10
	maxTextBytes    = 64 << 10 // a single assistant or user message
)

// clone returns a deep copy, so a message handed to a caller never shares a ToolCall with the
// conversation's own copy.
func (m Message) clone() Message {
	if m.Tool != nil {
		tool := *m.Tool
		m.Tool = &tool
	}
	return m
}
