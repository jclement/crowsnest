package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSpinningOnTranscripts runs the detector over the fixtures in testdata/spinning (Claude
// Code's JSONL shape; see generate.py for how each was made), at every point of each transcript
// for the healthy ones (a false alarm anywhere fails), and at the end for the spinning ones.
func TestSpinningOnTranscripts(t *testing.T) {
	files, err := filepath.Glob("testdata/spinning/*.jsonl")
	if err != nil || len(files) < 6 {
		t.Fatalf("fixtures: %v %v", files, err)
	}
	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			file, err := os.Open(path) //nolint:gosec // a fixture
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			steps := ParseSteps(file)
			if len(steps) == 0 {
				t.Fatal("no steps parsed")
			}
			if strings.HasPrefix(name, "spin-") {
				verdict := DetectSpinning(steps, time.Time{})
				if !verdict.Spinning {
					t.Fatalf("missed: %s", verdict.Why)
				}
				if !strings.Contains(verdict.Why, "tool calls in") {
					t.Fatalf("the explanation %q", verdict.Why)
				}
				// The tree changed during the window (crowsnest's own sample): not spinning.
				if DetectSpinning(steps, steps[len(steps)-5].At).Spinning {
					t.Fatal("a tree change inside the window must clear it")
				}
				return
			}
			for i := 1; i <= len(steps); i++ {
				if verdict := DetectSpinning(steps[:i], time.Time{}); verdict.Spinning {
					t.Fatalf("false alarm after %d calls: %s", i, verdict.Why)
				}
			}
		})
	}
}

func TestSpinningExplainsFailingTests(t *testing.T) {
	file, err := os.Open("testdata/spinning/spin-test-read-loop.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	verdict := DetectSpinning(ParseSteps(file), time.Time{})
	if !strings.Contains(verdict.Why, "TestWidgetParse") || !strings.Contains(verdict.Why, "no file changed") {
		t.Fatalf("why: %s", verdict.Why)
	}
}
