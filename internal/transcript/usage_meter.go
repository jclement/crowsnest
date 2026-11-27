// Usage records for spend (DESIGN.md 7.11). The conversation tailer (tail.go) follows a
// transcript only while someone reads it, and the normaliser keeps only the context size; spend
// needs every assistant call's token counts for every job, all the time. So this file adds a
// second, lighter reader over the same files and the same record shape: ParseUsage pulls the
// usage out of one line, and ReadUsage reads whatever was appended to one file since a cursor
// the caller persists, so a restart picks up exactly where the last read stopped.
//
// What the transcript records, as seen in Claude Code 2.1.332's files (2026-11-26):
//
//   - One API response is written as one record per content block, every record repeating the
//     whole message's usage under the same message.id. Counting records would count a reply
//     with three blocks three times, so callers deduplicate by message id. Rarely the copies
//     differ (an early copy written while streaming has fewer output tokens); the caller keeps
//     the largest value of each field.
//   - usage.cache_creation splits cache writes into ephemeral_5m and ephemeral_1h (priced
//     differently); older records have only cache_creation_input_tokens, counted as 5-minute.
//   - usage.speed is "fast" for fast mode, usage.inference_geo "us" for US-only inference.
//   - Subagents (the Task/Agent tool) write their own files, <session>/subagents/agent-*.jsonl
//     beside the session's own file, with isSidechain set on every record. SubagentFiles finds
//     them. Older versions wrote sidechain records into the session's file; those are counted
//     too, and the message-id deduplication keeps a record from counting twice.
//   - The model "<synthetic>" marks a message Claude Code made up itself (an error notice); it
//     has no usage and is skipped.

package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// UsageEntry is one assistant record's usage: one API call (or one copy of it; see the file
// comment on deduplication).
type UsageEntry struct {
	MessageID    string    // message.id: the key copies of one call share
	Model        string    // message.model as recorded, e.g. claude-opus-5-5
	At           time.Time // the record's timestamp
	Input        int64     // fresh input tokens
	Output       int64     // output tokens (thinking included)
	CacheWrite5m int64     // tokens written to the 5-minute cache
	CacheWrite1h int64     // tokens written to the 1-hour cache
	CacheRead    int64     // tokens read from the cache
	Speed        string    // "fast" for fast mode, else ""
	Geo          string    // inference_geo, e.g. "us"; "" or "global" is standard pricing
	Sidechain    bool      // a subagent's call
	ToolUseIDs   []string  // the tool_use blocks in this record (one call can hold several)
}

// Prompt is the call's whole prompt size: fresh input plus cache writes and reads. Long-context
// pricing tiers are decided on it.
func (e UsageEntry) Prompt() int64 { return e.Input + e.CacheWrite5m + e.CacheWrite1h + e.CacheRead }

// usageLine is the part of a transcript line ParseUsage reads.
type usageLine struct {
	Type        string    `json:"type"`
	IsSidechain bool      `json:"isSidechain"`
	Timestamp   time.Time `json:"timestamp"`
	Message     *struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"content"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreation            *struct {
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
			Speed string `json:"speed"`
			Geo   string `json:"inference_geo"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseUsage reads the usage from one transcript line. It reports false for anything that is not
// an assistant record with a message id and usage, and for synthetic messages.
func ParseUsage(line []byte) (UsageEntry, bool) {
	line = bytes.TrimSpace(line)
	// A cheap filter first: most lines are not assistant records, and the big ones (tool
	// results, attachments) are exactly the ones not worth decoding.
	if len(line) == 0 || line[0] != '{' || !bytes.Contains(line, []byte(`"usage"`)) {
		return UsageEntry{}, false
	}
	var rec usageLine
	if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" || rec.Message == nil || rec.Message.Usage == nil {
		return UsageEntry{}, false
	}
	m, u := rec.Message, rec.Message.Usage
	if m.ID == "" || m.Model == "" || m.Model == "<synthetic>" {
		return UsageEntry{}, false
	}
	entry := UsageEntry{
		MessageID: m.ID, Model: m.Model, At: rec.Timestamp, Sidechain: rec.IsSidechain,
		Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadInputTokens,
		CacheWrite5m: u.CacheCreationInputTokens, Geo: u.Geo,
	}
	if u.Speed == "fast" {
		entry.Speed = "fast"
	}
	if c := u.CacheCreation; c != nil && c.Ephemeral5m+c.Ephemeral1h > 0 {
		entry.CacheWrite5m, entry.CacheWrite1h = c.Ephemeral5m, c.Ephemeral1h
	}
	for _, block := range m.Content {
		if block.Type == "tool_use" && block.ID != "" {
			entry.ToolUseIDs = append(entry.ToolUseIDs, block.ID)
		}
	}
	return entry, true
}

// FileCursor is how far one transcript file has been read for usage. The caller persists it.
type FileCursor struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"` // bytes consumed: always at the end of a complete line
	Inode  uint64 `json:"inode"`  // the file read, so a replaced file is noticed across restarts
	// Format is "" for a Claude transcript, FormatCodex for a Codex rollout (codex.go).
	Format string `json:"format,omitempty"`
	// Model is a Codex rollout's model as its last turn_context named it (codex.go).
	Model string `json:"model,omitempty"`
}

// ErrUsageFileGone means the cursor's file no longer exists. What was read stays counted.
var ErrUsageFileGone = errors.New("transcript file gone")

// ReadUsage reads the complete lines appended to cursor's file since cursor.Offset, calls fn for
// each usage entry, and advances the cursor. A trailing line without its newline is left for the
// next call (Claude may be mid-write). A file that shrank or was replaced (a different inode) is
// read again from the top: reset reports that, so the caller can rely on its deduplication. A
// missing file is ErrUsageFileGone.
func ReadUsage(cursor *FileCursor, fn func(UsageEntry)) (reset bool, err error) {
	file, err := os.Open(cursor.Path)
	if errors.Is(err, os.ErrNotExist) {
		return false, ErrUsageFileGone
	}
	if err != nil {
		return false, fmt.Errorf("opening transcript for usage: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("reading transcript size: %w", err)
	}
	inode := inodeOf(info)
	if (cursor.Inode != 0 && inode != 0 && inode != cursor.Inode) || info.Size() < cursor.Offset {
		cursor.Offset, reset = 0, true
	}
	cursor.Inode = inode
	if info.Size() == cursor.Offset {
		return reset, nil
	}
	if _, err := file.Seek(cursor.Offset, io.SeekStart); err != nil {
		return reset, fmt.Errorf("seeking in transcript: %w", err)
	}
	parse := ParseUsage
	if cursor.Format == FormatCodex {
		parse = codexParser(cursor)
	}
	return reset, readUsageLines(file, cursor, parse, fn)
}

// readUsageLines consumes complete lines from r. cursor.Offset moves to just past each newline,
// so an unfinished last line is read again, whole, next time.
func readUsageLines(r io.Reader, cursor *FileCursor, parse func([]byte) (UsageEntry, bool), fn func(UsageEntry)) error {
	buffer := make([]byte, readChunk)
	position := cursor.Offset // absolute offset of buffer[0] on the next read
	var pending []byte
	skipping := false // inside a line longer than maxLineBytes: drop it
	for {
		n, err := r.Read(buffer)
		data := buffer[:n]
		for len(data) > 0 {
			newline := bytes.IndexByte(data, '\n')
			if newline < 0 {
				if !skipping && len(pending)+len(data) > maxLineBytes {
					pending, skipping = nil, true
				}
				if !skipping {
					pending = append(pending, data...)
				}
				position += int64(len(data))
				break
			}
			line := data[:newline]
			if !skipping {
				if len(pending) > 0 {
					line = append(pending, line...)
				}
				if entry, ok := parse(line); ok {
					fn(entry)
				}
			}
			position += int64(newline + 1)
			cursor.Offset = position
			data = data[newline+1:]
			pending, skipping = nil, false
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading transcript: %w", err)
		}
	}
}

// inodeOf is the file's inode on unix (crowsnest builds only for linux and darwin), 0 if unknown.
func inodeOf(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Ino) //nolint:unconvert // Ino is uint32 on some platforms
	}
	return 0
}

// SubagentFiles lists the subagent transcripts of the session whose own transcript is
// sessionFile: <dir>/<session id>/subagents/agent-*.jsonl, sorted. None is an empty list.
func SubagentFiles(sessionFile string) []string {
	dir := strings.TrimSuffix(sessionFile, ".jsonl")
	matches, err := filepath.Glob(filepath.Join(dir, "subagents", "*.jsonl"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}
