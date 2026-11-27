// Remote Control's session URL (DESIGN.md 7.10): where a standing agent's conversation opens in
// claude.ai/code and the Claude app. Claude Code prints it in the "/remote-control is active"
// banner and records it in the transcript as a `system` entry with subtype `bridge_status` and a
// `url` field (both verified against Claude Code 2.1.332, recorded in testdata/remote_control).
// Only a URL of the exact shape https://claude.ai/code/session_<id> is ever taken; nothing is
// built from a display name.

package orch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
)

const (
	// remoteControlURLPrefix is the only place a session URL may point.
	remoteControlURLPrefix = "https://claude.ai/code/session_"
	// minSessionIDRunes is the length of the ids seen so far ("01" and 22 more): a URL broken at
	// a line end before this many id characters continues on the next line.
	minSessionIDRunes = 24
	// maxTranscriptScan bounds how much of a transcript is read when looking for the URL.
	maxTranscriptScan = 64 << 20
)

// remoteControlURLPattern is the whole of an acceptable URL.
var remoteControlURLPattern = regexp.MustCompile(`^https://claude\.ai/code/session_[A-Za-z0-9]{20,64}$`)

// ValidRemoteControlURL reports whether url has the exact shape of a Remote Control session URL.
func ValidRemoteControlURL(url string) bool { return remoteControlURLPattern.MatchString(url) }

// ParseRemoteControlURL returns the newest Remote Control session URL in a pane's text, or "".
// Claude wraps the banner to the pane's width, breaking the URL anywhere (at 44 columns it was
// ".../co" then "de/session_…" on the next line, indented), so a URL that runs to the end of a
// line is continued with the first word of the next line while it is still incomplete.
//
// Only a URL that starts on a banner line, or within the few lines after it, is taken (R6-S10):
// Claude's "/remote-control is active" line or the Remote Control panel's "available in the Claude
// mobile app" line. A URL anywhere else in the pane is text an agent could have printed.
func ParseRemoteControlURL(text string) string {
	lines := strings.Split(text, "\n")
	found := ""
	bannerAt := -1
	for i := 0; i < len(lines); i++ {
		lower := strings.ToLower(lines[i])
		if strings.Contains(lower, remoteControlBannerText) || strings.Contains(lower, remoteControlPanelText) {
			bannerAt = i
		}
		if bannerAt < 0 || i-bannerAt > bannerReach {
			continue
		}
		start := strings.Index(lines[i], "https://claude.ai/")
		if start < 0 {
			start = urlStartAtLineEnd(lines[i])
		}
		if start < 0 {
			continue
		}
		candidate, used := joinWrapped(lines, i, start)
		if url := strings.TrimRight(candidate, ".,;:)"); ValidRemoteControlURL(url) {
			found = url
		}
		i += used
	}
	return found
}

// The banner lines a pane URL must follow (R6-S10), lower case, and how many lines after one the
// URL may start (it wraps at narrow widths).
const (
	remoteControlBannerText = "remote-control is active"
	remoteControlPanelText  = "available in the claude mobile app"
	bannerReach             = 2
)

// urlStartAtLineEnd finds a URL cut short before "claude.ai/" by the line end ("https://cla").
func urlStartAtLineEnd(line string) int {
	start := strings.LastIndex(line, "https://")
	if start < 0 || strings.ContainsAny(line[start:], " \t") || !strings.HasPrefix("https://claude.ai/", line[start:]) {
		return -1
	}
	return start
}

// joinWrapped reads the URL starting at lines[i][start:], following it onto later lines while
// it ends a line and is not yet complete. It returns the text and how many extra lines it used.
func joinWrapped(lines []string, i, start int) (string, int) {
	url, ended := urlRun(lines[i][start:])
	used := 0
	for ended && incomplete(url) && i+used+1 < len(lines) {
		next := strings.TrimLeft(lines[i+used+1], " \t")
		if next == "" {
			break
		}
		more, nextEnded := urlRun(next)
		if more == "" {
			break
		}
		url += more
		ended = nextEnded
		used++
	}
	return url, used
}

// urlRun is the run of URL characters at the start of s, and whether it reached the end of s.
func urlRun(s string) (string, bool) {
	end := strings.IndexFunc(s, func(r rune) bool { return !isURLRune(r) })
	if end < 0 {
		return s, true
	}
	return s[:end], strings.TrimRight(s[end:], " \t") == ""
}

func isURLRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(":/._-", r)
}

// incomplete reports whether a URL read so far could still go on: it is a prefix of the session
// URL, or its id is shorter than any seen.
func incomplete(url string) bool {
	if len(url) < len(remoteControlURLPrefix) {
		return strings.HasPrefix(remoteControlURLPrefix, url)
	}
	return strings.HasPrefix(url, remoteControlURLPrefix) && len(url)-len(remoteControlURLPrefix) < minSessionIDRunes
}

// TranscriptRemoteControlURL returns the newest session URL Claude recorded in the transcript at
// path (its `bridge_status` entries), or "" when there is none or the file cannot be read. Lines
// that do not mention bridge_status are skipped without being decoded, so a long transcript
// costs one read.
func TranscriptRemoteControlURL(path string) string {
	file, err := os.Open(path) //nolint:gosec // the transcript path crowsnest computed for its own agent
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(file, maxTranscriptScan))
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	found := ""
	marker := []byte(`"bridge_status"`)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, marker) {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			URL     string `json:"url"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Type == "system" && entry.Subtype == "bridge_status" && ValidRemoteControlURL(entry.URL) {
			found = entry.URL
		}
	}
	return found
}
