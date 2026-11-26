package taglines

import (
	"slices"
	"testing"
	"unicode/utf8"
)

// The set is part of the product: exactly 200, each under 60 characters, no repeats.
func TestTaglines(t *testing.T) {
	all := All()
	if len(all) != 200 {
		t.Fatalf("%d taglines, want 200", len(all))
	}
	seen := map[string]bool{}
	for _, line := range all {
		if utf8.RuneCountInString(line) >= 60 {
			t.Errorf("too long (%d): %q", utf8.RuneCountInString(line), line)
		}
		if seen[line] {
			t.Errorf("duplicate: %q", line)
		}
		seen[line] = true
	}
	if !slices.Contains(all, Random()) {
		t.Fatal("Random returned something not in the set")
	}
}
