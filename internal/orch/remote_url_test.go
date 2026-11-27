// Remote Control URL capture against pane texts recorded from Claude Code 2.1.332 in a throwaway
// herdr session (testdata/remote_control; the session ids are made up, same shape): the banner on
// one line, wrapped at 44 columns, the /remote-control panel, no banner, URL-like strings that
// are not claude.ai, two banners (the newest wins), and the transcript's bridge_status entries.

package orch

import (
	"os"
	"path/filepath"
	"testing"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "remote_control", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

const (
	firstURL  = "https://claude.ai/code/session_01QzP4vRk8sTn2LmWx7yBc3D"
	secondURL = "https://claude.ai/code/session_01HfG6jKa9DeRt5UoP2wXs8N"
)

func TestParseRemoteControlURL(t *testing.T) {
	cases := []struct{ file, want string }{
		{"banner-80cols-resumed.txt", firstURL},
		{"banner-80cols-new.txt", secondURL},
		{"banner-44cols.txt", firstURL}, // ".../co" + "de/session_…" on the next line
		{"panel-80cols.txt", secondURL}, // the /remote-control panel ends it with a full stop
		{"no-banner.txt", ""},
		{"not-claude.txt", ""},
		{"two-banners.txt", secondURL}, // the resumed banner, then a new conversation's
	}
	for _, c := range cases {
		if got := ParseRemoteControlURL(readTestdata(t, c.file)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.file, got, c.want)
		}
	}
}

func TestParseRemoteControlURLEdges(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"cut before the host", "  or at https://cla\n  ude.ai/code/session_01QzP4vRk8sTn2LmWx7yBc3D\n", firstURL},
		{"cut inside the id", "  https://claude.ai/code/session_01QzP4vRk8\n  sTn2LmWx7yBc3D\n", firstURL},
		{"complete id, words below", "  " + firstURL + "\n  Something else\n", firstURL},
		{"too short", "https://claude.ai/code/session_01abc\n", ""},
		{"other path", "https://claude.ai/chat/session_01QzP4vRk8sTn2LmWx7yBc3D\n", ""},
		{"query string dropped", firstURL + "?x=1\n", firstURL},
		{"lookalike host", "https://claude.ai.evil.com/code/session_01QzP4vRk8sTn2LmWx7yBc3D", ""},
	}
	for _, c := range cases {
		if got := ParseRemoteControlURL(c.text); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTranscriptRemoteControlURL(t *testing.T) {
	path := filepath.Join("testdata", "remote_control", "transcript.jsonl")
	// The newest valid bridge_status wins; a URL in a user message and one to another host do not count.
	if got := TranscriptRemoteControlURL(path); got != secondURL {
		t.Fatalf("got %q, want %q", got, secondURL)
	}
	if got := TranscriptRemoteControlURL(filepath.Join(t.TempDir(), "missing.jsonl")); got != "" {
		t.Fatalf("missing file: %q", got)
	}
}
