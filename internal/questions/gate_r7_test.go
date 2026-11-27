// Tests for GrantingText's normalisation (R7-S6): a label that reads as a grant is caught whatever
// look-alike letters, zero-width characters or inflection spell it, and ordinary text is not.

package questions

import "testing"

func TestGrantingTextSeesThroughSpellings(t *testing.T) {
	cyrillicA := string(rune(0x0410))
	zwj := string(rune(0x200D))
	zwsp := string(rune(0x200B))
	fullwidthM := string(rune(0xFF2D))
	cases := map[string]bool{
		cyrillicA + "llow: merge j_5":         true, // Cyrillic capital A
		"Al" + zwj + "low":                    true, // a zero-width joiner splitting the word
		"de" + zwsp + "ploy":                  true,
		fullwidthM + "erge it":                true, // a full-width M, plain after NFKC
		"Merging j_5":                         true,
		"Deploys now":                         true,
		"Ship?":                               false, // the plain confirm of ordinary questions
		"Go ahead":                            true,
		"Go ahead with step 3?":               true,
		"Approved":                            true,
		"Rebuild the docs site?":              false,
		"Which colour for the header?":        false,
		"Release notes: keep or rewrite?":     false,
		"Should I go to the next file ahead?": false,
	}
	for text, want := range cases {
		if got := GrantingText(text); got != want {
			t.Errorf("GrantingText(%q) = %v, want %v", text, got, want)
		}
	}
}
