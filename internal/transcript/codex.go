// Codex session logs ("rollouts", ~/.codex/sessions/YYYY/MM/DD/rollout-<time>-<thread id>.jsonl)
// as a source of usage (DESIGN.md 7.11). Read against codex-cli 0.167's files: each model call
// ends with an `event_msg` whose payload is `token_count`, carrying the call's own counts
// (`info.last_token_usage`) and the session's running total (`info.total_token_usage`); a
// `turn_context` line before each turn names the model. OpenAI counts cached input inside
// input_tokens, so fresh input is input minus cached, and cached is a cache read. Codex writes no
// cache-write count and no message id: the running total's token count stands in for the id, so
// the same report seen twice is counted once. Codex documents none of this as a stable format;
// testdata/codex/rollout.jsonl pins the shape it was written against.

package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// FormatCodex marks a FileCursor over a Codex rollout ("" is a Claude transcript).
const FormatCodex = "codex"

// codexIDPattern is a Codex thread id (a UUID).
var codexIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// LocateCodex finds the rollout of a Codex thread under codexHome (~/.codex).
func LocateCodex(codexHome, threadID string) (string, error) {
	if !codexIDPattern.MatchString(threadID) {
		return "", fmt.Errorf("codex thread id %q: %w", threadID, ErrNotFound)
	}
	matches, err := filepath.Glob(filepath.Join(codexHome, "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("searching for codex thread %s: %w", threadID, err)
	}
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() {
			return match, nil
		}
	}
	return "", ErrNotFound
}

// codexLine is the part of a rollout line the usage reader looks at.
type codexLine struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type  string `json:"type"`
		Model string `json:"model"` // turn_context
		Info  *struct {
			Total codexTokens `json:"total_token_usage"`
			Last  codexTokens `json:"last_token_usage"`
		} `json:"info"` // token_count
	} `json:"payload"`
}

type codexTokens struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
	Total  int64 `json:"total_tokens"`
}

// codexParser returns ParseUsage for a rollout: it remembers the model the last turn_context
// named, in the cursor, so a read that resumes mid-file still knows it.
func codexParser(cursor *FileCursor) func([]byte) (UsageEntry, bool) {
	return func(line []byte) (UsageEntry, bool) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' || !(bytes.Contains(line, []byte(`"token_count"`)) || bytes.Contains(line, []byte(`"turn_context"`))) {
			return UsageEntry{}, false
		}
		var rec codexLine
		if json.Unmarshal(line, &rec) != nil {
			return UsageEntry{}, false
		}
		if rec.Type == "turn_context" {
			if rec.Payload.Model != "" {
				cursor.Model = rec.Payload.Model
			}
			return UsageEntry{}, false
		}
		info := rec.Payload.Info
		if rec.Type != "event_msg" || rec.Payload.Type != "token_count" || info == nil || info.Last.Total == 0 {
			return UsageEntry{}, false
		}
		fresh := max(0, info.Last.Input-info.Last.Cached)
		return UsageEntry{
			MessageID: fmt.Sprintf("codex:%d", info.Total.Total),
			Model:     cursor.Model,
			At:        rec.Timestamp,
			Input:     fresh,
			CacheRead: info.Last.Cached,
			Output:    info.Last.Output,
		}, true
	}
}
