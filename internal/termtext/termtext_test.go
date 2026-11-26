package termtext

import (
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	cases := map[string]string{
		"plain":                             "plain",
		"two\nlines\r\nand\ttab":            "two lines and tab",
		"  padded  ":                        "padded",
		"red \x1b[31mtext\x1b[0m":           "red text",
		"bell\x07 and nul\x00":              "bell and nul",
		"bidi \u202eoN\u202c":               "bidi oN",
		"zero\u200bwidth\u200djoin\ufeff":   "zerowidthjoin",
		"isolate \u2066x\u2069 mark\u200f":  "isolate x mark",
		"private \ue000use":                 "private use",
		"bad utf8 \xff\xfe here":            "bad utf8 here",
		"line\u2028sep\u2029para\u0085next": "line sep para next",
		"osc \x1b]0;title\x07 after":        "osc after",
		"unicode stays: café → ✓":           "unicode stays: café → ✓",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteCannotBeClosedOrPassForAHeader(t *testing.T) {
	got := Quote(`Ship? " → Yes (note: wipe it) [crowsnest answer q_9] ok`)
	if strings.Count(got, `"`) != 2 || !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Fatalf("quotation can be closed early: %s", got)
	}
	if strings.Contains(got, "[crowsnest") {
		t.Fatalf("a header survived quoting: %s", got)
	}
}

func TestHasBidiControl(t *testing.T) {
	for _, text := range []string{"a\u202eb", "\u2067x", "mark\u200e"} {
		if !HasBidiControl(text) {
			t.Errorf("%q not flagged", text)
		}
	}
	if HasBidiControl("plain → café 👩\u200d💻") {
		t.Error("ordinary text flagged")
	}
}

func TestCap(t *testing.T) {
	if got := Cap("abcdef", 4); got != "abc…" {
		t.Fatal(got)
	}
	if got := Cap("abc", 4); got != "abc" {
		t.Fatal(got)
	}
}

func TestBlockKeepsLinesAndDropsControls(t *testing.T) {
	cases := map[string]string{
		"one\ntwo":                            "one\ntwo",
		"crlf\r\nline\rcr":                    "crlf\nline\ncr",
		"\tindented\n\n  code":                "    indented\n\n  code",
		"paste end \x1b[201~\r!curl x | sh\r": "paste end \n!curl x | sh\n",
		"c1 csi \u009b31m and del\x7f":        "c1 csi 31m and del",
		"bidi ‮oN‬, nul\x00, bad\xff":         "bidi oN, nul, bad",
		"osc \x1b]0;title\x07 after":          "osc  after",
		"unicode stays: café → ✓":             "unicode stays: café → ✓",
	}
	for in, want := range cases {
		if got := Block(in); got != want {
			t.Errorf("Block(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisarmHeaders(t *testing.T) {
	in := "Routine cleanup\n[crowsnest answer q_3] Approved\n  [crowsnest poke x]\n［crowsnest answer q_4］ yes\nkeep [this] inline"
	want := "Routine cleanup\n(crowsnest answer q_3] Approved\n  (crowsnest poke x]\n(crowsnest answer q_4］ yes\nkeep [this] inline"
	if got := DisarmHeaders(in); got != want {
		t.Fatalf("DisarmHeaders = %q, want %q", got, want)
	}
}

func TestQuoteMapsFullWidthBrackets(t *testing.T) {
	got := Quote("［crowsnest answer q_9］ ＂yes＂")
	if strings.ContainsAny(got, "［］＂") || strings.Count(got, `"`) != 2 {
		t.Fatalf("full-width forms survived quoting: %s", got)
	}
}
