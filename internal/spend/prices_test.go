package spend

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/jclement/crowsnest/internal/transcript"
)

// Model names as transcripts, flags and Bedrock write them all land on one price row.
func TestCanonical(t *testing.T) {
	prices := Embedded()
	cases := []struct {
		in, want string
		known    bool
	}{
		{"claude-opus-5-5", "claude-opus-5-5", true},
		{"Claude-Opus-5-5", "claude-opus-5-5", true},
		{"opus", "claude-opus-5-5", true},
		{"sonnet", "claude-sonnet-5-5", true},
		{"haiku", "claude-haiku-5-5", true},
		{"fable", "claude-fable-5-2", true},
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5", true},
		{"anthropic.claude-sonnet-4-6", "claude-sonnet-4-6", true},
		{"claude-opus-4-5@20251101", "claude-opus-4-5", true},
		{"claude-sonnet-5-5[1m]", "claude-sonnet-5-5", true},
		{"gpt-9", "gpt-9", false},
	}
	for _, c := range cases {
		got, known := prices.Canonical(c.in)
		if got != c.want || known != c.known {
			t.Errorf("Canonical(%q) = %q, %v; want %q, %v", c.in, got, known, c.want, c.known)
		}
	}
}

// Keys carry only the modifiers that change that model's price.
func TestKey(t *testing.T) {
	prices := Embedded()
	cases := []struct {
		model, speed, geo string
		prompt            int64
		want              string
	}{
		{"claude-opus-5-5", "", "", 10, "claude-opus-5-5"},
		{"claude-opus-5-5", "fast", "global", 10, "claude-opus-5-5+fast"},
		{"claude-sonnet-5-5", "fast", "", 10, "claude-sonnet-5-5"}, // no fast mode on Sonnet
		{"claude-haiku-5-5", "", "", 100_001, "claude-haiku-5-5+long"},
		{"claude-haiku-5-5", "", "", 100_000, "claude-haiku-5-5"},
		{"claude-opus-5-5", "", "", 900_000, "claude-opus-5-5"}, // 1M context at standard price
		{"claude-opus-5-5", "fast", "us", 1, "claude-opus-5-5+fast+us"},
		{"mystery", "fast", "us", 1, "mystery"},
	}
	for _, c := range cases {
		if got := prices.Key(c.model, c.speed, c.geo, c.prompt); got != c.want {
			t.Errorf("Key(%q, %q, %q, %d) = %q; want %q", c.model, c.speed, c.geo, c.prompt, got, c.want)
		}
	}
}

// Costs match the pricing page's figures, including the cache-hit multipliers that are not 0.1x
// on the newest models, fast mode and the US multiplier stacking.
func TestCost(t *testing.T) {
	prices := Embedded()
	million := int64(1_000_000)
	cases := []struct {
		key    string
		tokens Tokens
		usd    float64
	}{
		{"claude-opus-5-5", Tokens{Input: million}, 4},
		{"claude-opus-5-5", Tokens{Output: million}, 20},
		{"claude-opus-5-5", Tokens{CacheWrite5m: million, CacheWrite1h: million, CacheRead: million}, 5 + 8 + 0.20},
		{"claude-fable-5-2", Tokens{CacheRead: million}, 0.20},
		{"claude-sonnet-5-5", Tokens{CacheRead: million}, 0.10},
		{"claude-sonnet-5", Tokens{CacheRead: million}, 0.20},
		{"claude-haiku-5-5", Tokens{Input: million, Output: million}, 0.60},
		{"claude-haiku-5-5+long", Tokens{Input: million, Output: million}, 3},
		{"claude-opus-5-5+fast", Tokens{Input: million, Output: million, CacheRead: million}, 8 + 40 + 0.40},
		{"claude-opus-5-5+fast+us", Tokens{Input: million}, 8 * 1.1},
		{"claude-haiku-5-5", Tokens{CacheRead: 1000}, 0.00001},
	}
	for _, c := range cases {
		nano, ok := prices.Cost(c.key, c.tokens)
		if !ok || math.Abs(float64(nano)/1e9-c.usd) > 1e-9 {
			t.Errorf("Cost(%s, %+v) = %d nano (%v); want $%v", c.key, c.tokens, nano, ok, c.usd)
		}
	}
	if _, ok := prices.Cost("gpt-9", Tokens{Input: 1}); ok {
		t.Error("an unknown model was priced")
	}
}

// A prices file replaces rows by id, adds new ones, and changes the checked date; a missing file
// is the embedded table; a malformed one is an error, never silently ignored.
func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()
	missing, err := Load(filepath.Join(dir, "none.json"))
	if err != nil || missing.Override != "" || missing.Checked() != Embedded().Checked() {
		t.Fatalf("missing file: %v %+v", err, missing)
	}
	path := filepath.Join(dir, "prices.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"checked":"2027-01-01","models":[
		{"id":"claude-opus-5-5","input":1,"output":2,"cache_write_5m":0,"cache_write_1h":0,"cache_read":0},
		{"id":"claude-new-6","aliases":["new"],"input":3,"output":4,"cache_write_5m":0,"cache_write_1h":0,"cache_read":0}]}`)
	prices, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if prices.Checked() != "2027-01-01" || prices.Override != path {
		t.Fatalf("meta: %s %s", prices.Checked(), prices.Override)
	}
	if nano, _ := prices.Cost("claude-opus-5-5", Tokens{Input: 1_000_000}); nano != 1e9 {
		t.Fatalf("override not applied: %d", nano)
	}
	if id, ok := prices.Canonical("new"); !ok || id != "claude-new-6" {
		t.Fatalf("added model: %s %v", id, ok)
	}
	if nano, _ := prices.Cost("claude-sonnet-5-5", Tokens{Input: 1_000_000}); nano != 2e9 {
		t.Fatalf("untouched row changed: %d", nano)
	}
	write(`{"models":[{"id":"x","input":1,"outptu":2}]}`)
	if _, err := Load(path); err == nil {
		t.Fatal("a misspelt field was accepted")
	}
	write(`{"models":[{"id":"x","input":-1}]}`)
	if _, err := Load(path); err == nil {
		t.Fatal("a negative price was accepted")
	}
}

// Every model id seen in real transcripts on the development machine has a price.
func TestEmbeddedCoversModelsSeen(t *testing.T) {
	prices := Embedded()
	for _, model := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-2", "claude-haiku-5-5", "claude-fable-5-1"} {
		if _, ok := prices.Canonical(model); !ok {
			t.Errorf("%s has no price", model)
		}
	}
	_ = transcript.UsageEntry{}
}
