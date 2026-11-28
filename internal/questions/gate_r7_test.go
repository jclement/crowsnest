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

// R9-S10: the review's spellings and verbs are seen; ordinary words with an I or an accent are not
// turned into grants.
func TestGrantingTextSeesTheR9Spellings(t *testing.T) {
	cases := map[string]bool{
		"AIIow":                         true, // capital I for l
		"Allów":                         true, // an accent NFKC would have kept
		"ᴀʟʟᴏᴡ":                         true, // Latin small capitals
		"ᎪᏞᏞᎾᎳ":                         true, // Cherokee
		"Ałłow":                         true,
		"Alloω":                         true,
		"Alloԝ":                         true,
		"Proceed with the rewrite":      true,
		"Authorize it":                  true,
		"Accept the change?":            true,
		"Confirm":                       true,
		"Publish now":                   true,
		"Apply the patch":               true,
		"Is it OK?":                     false,
		"Idle or busy?":                 false,
		"Café colour, or the dark one?": false,
	}
	for text, want := range cases {
		if got := GrantingText(text); got != want {
			t.Errorf("GrantingText(%q) = %v, want %v", text, got, want)
		}
	}
}
