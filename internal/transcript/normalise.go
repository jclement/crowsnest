// The normaliser: one transcript line in, zero or more message upserts out (DESIGN.md 7.2).
//
//	user, string content         → a user message, tagged poke/voice/answer by its header
//	user, tool_result blocks     → completes the tool card with that tool_use id
//	assistant, text blocks       → assistant markdown (consecutive blocks of one reply merge)
//	assistant, tool_use blocks   → a tool card (name, short input), not yet done
//	assistant, thinking blocks   → a thinking message, when it has text (often it is redacted)
//	anything else                → ignored
//
// Records marked isMeta (context Claude injects, not something anyone typed) are skipped.
// Sidechain (subagent) records are skipped too: Claude 2.1.x writes subagent conversations to
// their own files, and the parent's Task/Agent tool card already carries the subagent's result.

package transcript

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// record is the part of a transcript line the normaliser reads. Every other field is ignored.
type record struct {
	Type             string    `json:"type"`
	Subtype          string    `json:"subtype"` // system records: compact_boundary
	UUID             string    `json:"uuid"`
	IsSidechain      bool      `json:"isSidechain"`
	IsMeta           bool      `json:"isMeta"`
	IsCompactSummary bool      `json:"isCompactSummary"` // the summary a compaction starts the conversation with
	Timestamp        time.Time `json:"timestamp"`
	CompactMetadata  *struct {
		PostTokens int `json:"postTokens"`
	} `json:"compactMetadata"`
	Message *struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
		Usage   *usageRecord    `json:"usage"`
	} `json:"message"`
}

// usageRecord is an assistant message's token usage: what the call read is the context size.
type usageRecord struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// block is one content block of a user or assistant message.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Normaliser holds what it needs across lines: the tool cards awaiting results, and the reply
// that text blocks are being merged into. Not safe for concurrent use; one per transcript.
type Normaliser struct {
	tools    map[string]Message // tool_use id → its card, kept so a result can complete it
	lastText *Message           // the assistant text message the next text block may extend
	lastMsg  string             // message.id lastText belongs to
	usage    Usage              // context size and the human's last word, as read so far
}

// Usage is what the transcript says about the conversation's size and the human's presence
// (DESIGN.md 7.9): read by the orchestrator's upkeep to decide on compaction and the daily new
// conversation.
type Usage struct {
	// Tokens is the context size now: the latest call's input (fresh, cache-created and
	// cache-read tokens), or what a compaction left (compactMetadata.postTokens). Known is false
	// before the first reading and right after a compaction that did not record its size.
	Tokens int       `json:"tokens"`
	Known  bool      `json:"known"`
	At     time.Time `json:"at,omitzero"` // when Tokens was read
	// CompactedAt is the last compaction boundary (automatic or /compact).
	CompactedAt time.Time `json:"compacted_at,omitzero"`
	// HumanAt is when someone last typed to the agent: a Chat prompt or a voice message, not a
	// poke, an answer, a command or anything else crowsnest typed.
	HumanAt time.Time `json:"human_at,omitzero"`
}

// Usage returns what has been read so far.
func (n *Normaliser) Usage() Usage { return n.usage }

// NewNormaliser returns a normaliser at the start of a transcript.
func NewNormaliser() *Normaliser {
	return &Normaliser{tools: map[string]Message{}}
}

// Line normalises one transcript line. It returns the messages that are new or changed; their
// Index and Seq are left for the Conversation to assign. A line it cannot use yields nothing.
func (n *Normaliser) Line(line []byte) []Message {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil || rec.IsSidechain {
		return nil
	}
	n.track(rec)
	if rec.Message == nil || rec.IsMeta {
		return nil
	}
	switch rec.Type {
	case "user":
		messages := n.user(rec)
		n.trackHuman(rec, messages)
		return messages
	case "assistant":
		return n.assistant(rec)
	}
	return nil
}

// track follows the context size: each assistant call's usage, and compaction boundaries.
func (n *Normaliser) track(rec record) {
	switch {
	case rec.Type == "system" && rec.Subtype == "compact_boundary":
		n.usage.CompactedAt = rec.Timestamp
		n.usage.Tokens, n.usage.Known, n.usage.At = 0, false, rec.Timestamp
		if rec.CompactMetadata != nil && rec.CompactMetadata.PostTokens > 0 {
			n.usage.Tokens, n.usage.Known = rec.CompactMetadata.PostTokens, true
		}
	case rec.Type == "assistant" && rec.Message != nil && rec.Message.Usage != nil:
		u := rec.Message.Usage
		n.usage.Tokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
		n.usage.Known, n.usage.At = true, rec.Timestamp
	}
}

// trackHuman notes a user message someone typed (untagged or voice, and not one of crowsnest's
// own bracketed lines, which are typed by the server).
func (n *Normaliser) trackHuman(rec record, messages []Message) {
	if rec.IsCompactSummary {
		return
	}
	for _, msg := range messages {
		if msg.Kind != KindUser {
			continue
		}
		human := msg.Tag == TagVoice || (msg.Tag == "" && !strings.HasPrefix(msg.Text, "[crowsnest "))
		if human && rec.Timestamp.After(n.usage.HumanAt) {
			n.usage.HumanAt = rec.Timestamp
		}
	}
}

// ---- User records --------------------------------------------------------------------

func (n *Normaliser) user(rec record) []Message {
	content := rec.Message.Content
	var text string
	if json.Unmarshal(content, &text) == nil {
		n.lastText = nil
		return userText(rec.UUID, text, rec.Timestamp)
	}
	var blocks []block
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []Message
	var typed []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			if card, ok := n.completeTool(b); ok {
				out = append(out, card)
			}
		case "text":
			typed = append(typed, b.Text)
		case "image":
			typed = append(typed, "[image]")
		}
	}
	if len(typed) > 0 {
		n.lastText = nil
		out = append(out, userText(rec.UUID, strings.Join(typed, "\n"), rec.Timestamp)...)
	}
	return out
}

// Headers crowsnest types in front of automatic messages (DESIGN.md 7.1 and 6.3).
var (
	commandName   = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgs   = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	commandOutput = regexp.MustCompile(`(?s)^<local-command-std(?:out|err)>(.*?)</local-command-std(?:out|err)>`)
)

// userText builds a typed user message, recognising crowsnest's own headers and Claude's
// slash-command wrappers. An empty message yields nothing.
func userText(id, text string, at time.Time) []Message {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	tag := ""
	switch {
	case strings.HasPrefix(text, "[crowsnest poke"):
		tag = TagPoke
	case strings.HasPrefix(text, "[crowsnest voice"):
		tag = TagVoice
	case strings.HasPrefix(text, "[crowsnest answer"):
		tag = TagAnswer
	case commandName.MatchString(text):
		tag = TagCommand
		name := strings.TrimSpace(commandName.FindStringSubmatch(text)[1])
		if args := commandArgs.FindStringSubmatch(text); args != nil && strings.TrimSpace(args[1]) != "" {
			name += " " + strings.TrimSpace(args[1])
		}
		text = name
	case commandOutput.MatchString(text):
		tag = TagCommand
		text = strings.TrimSpace(commandOutput.FindStringSubmatch(text)[1])
		if text == "" {
			return nil
		}
	}
	return []Message{{ID: id, Kind: KindUser, Tag: tag, Text: truncateText(text, maxTextBytes), At: at}}
}

// completeTool fills in a tool card's result. A result for a tool_use the normaliser never saw
// (the transcript was picked up mid-way) still becomes a card, named "tool".
func (n *Normaliser) completeTool(b block) (Message, bool) {
	if b.ToolUseID == "" {
		return Message{}, false
	}
	card, ok := n.tools[b.ToolUseID]
	if !ok {
		card = Message{ID: b.ToolUseID, Kind: KindTool, Tool: &ToolCall{ID: b.ToolUseID, Name: "tool"}}
	}
	card = card.clone()
	result, truncated := truncateBytes(stripANSI(resultText(b.Content)), maxResultBytes)
	card.Tool.Result, card.Tool.ResultTruncated, card.Tool.IsError, card.Tool.Done = result, truncated, b.IsError, true
	delete(n.tools, b.ToolUseID)
	return card, true
}

// resultText flattens a tool_result's content, which is a string or an array of blocks.
func resultText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []block
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

// ---- Assistant records ---------------------------------------------------------------

func (n *Normaliser) assistant(rec record) []Message {
	var blocks []block
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return nil
	}
	var out []Message
	for i, b := range blocks {
		id := rec.UUID
		if i > 0 {
			id = rec.UUID + "#" + strconv.Itoa(i)
		}
		switch b.Type {
		case "text":
			if msg, ok := n.text(id, rec.Message.ID, b.Text, rec.Timestamp); ok {
				out = append(out, msg)
			}
		case "tool_use":
			n.lastText = nil
			card := Message{ID: b.ID, Kind: KindTool, At: rec.Timestamp, Tool: &ToolCall{
				ID: b.ID, Name: b.Name, Summary: toolSummary(b.Name, b.Input), Input: prettyInput(b.Input),
			}}
			if card.ID == "" {
				card.ID, card.Tool.ID = id, id
			}
			n.tools[card.ID] = card
			out = append(out, card.clone())
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				n.lastText = nil
				out = append(out, Message{ID: id, Kind: KindThinking, Text: truncateText(b.Thinking, maxTextBytes), At: rec.Timestamp})
			}
		}
	}
	return out
}

// text emits assistant text. Claude writes one record per content block, so a reply split into
// several text blocks with nothing between them is merged back into one message.
func (n *Normaliser) text(id, messageID, text string, at time.Time) (Message, bool) {
	if strings.TrimSpace(text) == "" {
		return Message{}, false
	}
	if n.lastText != nil && messageID != "" && n.lastMsg == messageID {
		n.lastText.Text = truncateText(n.lastText.Text+"\n\n"+text, maxTextBytes)
		return *n.lastText, true
	}
	msg := Message{ID: id, Kind: KindAssistant, Text: truncateText(text, maxTextBytes), At: at}
	n.lastText, n.lastMsg = &msg, messageID
	return msg, true
}

// ---- Tool input ----------------------------------------------------------------------

// summaryKeys is, per tool, the input field that best says what the call does.
var summaryKeys = map[string][]string{
	"Bash":      {"command"},
	"Read":      {"file_path"},
	"Write":     {"file_path"},
	"Edit":      {"file_path"},
	"MultiEdit": {"file_path"},
	"Grep":      {"pattern"},
	"Glob":      {"pattern"},
	"WebFetch":  {"url"},
	"WebSearch": {"query"},
	"Task":      {"description", "prompt"},
	"Agent":     {"description", "prompt"},
	"Skill":     {"skill"},
}

// genericSummaryKeys are tried for any tool, after its own keys.
var genericSummaryKeys = []string{"description", "command", "file_path", "path", "url", "query", "pattern", "prompt", "name"}

// toolSummary picks one short line from a tool's input: the command, the file, the pattern.
func toolSummary(name string, input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, key := range append(summaryKeys[name], genericSummaryKeys...) {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return shortLine(value)
		}
	}
	return ""
}

// shortLine is the first non-blank line of s, cut to maxSummaryRunes.
func shortLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if utf8.RuneCountInString(line) > maxSummaryRunes {
				runes := []rune(line)
				return string(runes[:maxSummaryRunes-1]) + "…"
			}
			return line
		}
	}
	return ""
}

// prettyInput is the input as indented JSON, truncated.
func prettyInput(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var buffer bytes.Buffer
	if json.Indent(&buffer, input, "", "  ") != nil {
		return ""
	}
	out, _ := truncateBytes(buffer.String(), maxInputBytes)
	return out
}

// ---- Text helpers --------------------------------------------------------------------

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func stripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// truncateBytes cuts s to at most limit bytes on a rune boundary and says whether it did.
func truncateBytes(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// truncateText is truncateBytes with a visible marker, for message bodies.
func truncateText(s string, limit int) string {
	out, cut := truncateBytes(s, limit)
	if cut {
		return out + "\n\n… (truncated)"
	}
	return out
}
