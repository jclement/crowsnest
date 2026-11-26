// Prices: what a token costs, per model family, so spend can be shown in dollars (DESIGN.md
// 7.11). The table is embedded (prices.json, with the date it was checked against Anthropic's
// pricing page) and can be replaced, wholly or per model, by ~/.config/crowsnest/prices.json in
// the same format. Every figure is an API-equivalent estimate: a subscription plan is not billed
// per token, and partner clouds price differently.
//
// Usage is stored per pricing key, not per raw model name: the model's canonical id plus the
// modifiers that change its price for that call ("+fast", "+long", "+us"). Costs are computed
// when read, from the stored token counts, so a corrected price file reprices history too.

package spend

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

//go:embed prices.json
var embeddedPrices []byte

// Rates are USD per million tokens.
type Rates struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	CacheRead    float64 `json:"cache_read"`
}

// LongRates are a long-context tier: a call whose whole prompt is over Over tokens pays them
// (Claude Haiku 5.5 above 100,000 tokens).
type LongRates struct {
	Over int64 `json:"over"`
	Rates
}

// ModelPrice is one model's row.
type ModelPrice struct {
	ID      string   `json:"id"`                // canonical model id, e.g. claude-opus-5-5
	Aliases []string `json:"aliases,omitempty"` // short names that mean this model (opus, sonnet)
	Rates
	// Fast multiplies every rate in fast mode (the pricing page's fast prices are 2x, and the
	// cache multipliers stack on the fast input price). 0: no fast mode, priced as standard.
	Fast float64    `json:"fast,omitempty"`
	Long *LongRates `json:"long,omitempty"`
}

// Table is the whole price list.
type Table struct {
	Checked string `json:"checked"` // the date the figures were checked, YYYY-MM-DD
	Source  string `json:"source"`
	// Currency says what the numbers are; informational.
	Currency string `json:"currency,omitempty"`
	// USMultiplier applies to calls with inference_geo "us" (1.1 on the Claude API).
	USMultiplier float64      `json:"us_multiplier,omitempty"`
	Models       []ModelPrice `json:"models"`

	byID    map[string]*ModelPrice
	byAlias map[string]*ModelPrice
}

// Prices is the table in use and where it came from.
type Prices struct {
	table *Table
	// Override is the prices file that changed the embedded table, or "".
	Override string
}

// Embedded returns the built-in table.
func Embedded() *Prices {
	table, err := parseTable(embeddedPrices)
	if err != nil {
		panic(fmt.Sprintf("embedded prices.json is invalid: %v", err)) // a build defect, caught by tests
	}
	return &Prices{table: table}
}

// Load returns the embedded table with path's file applied on top: its models replace or add
// rows by id, and its checked date, source and US multiplier replace the embedded ones when set.
// A missing file is not an error (the embedded table alone); a malformed one is, so a typo in a
// price is never silently ignored.
func Load(path string) (*Prices, error) {
	prices := Embedded()
	if path == "" {
		return prices, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is configuration
	if errors.Is(err, os.ErrNotExist) {
		return prices, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading prices file %s: %w", path, err)
	}
	override, err := parseTable(data)
	if err != nil {
		return nil, fmt.Errorf("prices file %s: %w", path, err)
	}
	prices.table.merge(override)
	prices.Override = path
	return prices, nil
}

func parseTable(data []byte) (*Table, error) {
	var table Table
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&table); err != nil {
		return nil, fmt.Errorf("parsing prices: %w", err)
	}
	for _, model := range table.Models {
		if model.ID == "" {
			return nil, errors.New("a model has no id")
		}
		if model.Input < 0 || model.Output < 0 || model.CacheWrite5m < 0 || model.CacheWrite1h < 0 || model.CacheRead < 0 {
			return nil, fmt.Errorf("model %s: prices cannot be negative", model.ID)
		}
	}
	table.index()
	return &table, nil
}

func (t *Table) index() {
	t.byID, t.byAlias = map[string]*ModelPrice{}, map[string]*ModelPrice{}
	for i := range t.Models {
		model := &t.Models[i]
		t.byID[strings.ToLower(model.ID)] = model
		for _, alias := range model.Aliases {
			t.byAlias[strings.ToLower(alias)] = model
		}
	}
}

func (t *Table) merge(override *Table) {
	if override.Checked != "" {
		t.Checked = override.Checked
	}
	if override.Source != "" {
		t.Source = override.Source
	}
	if override.USMultiplier > 0 {
		t.USMultiplier = override.USMultiplier
	}
	for _, model := range override.Models {
		replaced := false
		for i := range t.Models {
			if strings.EqualFold(t.Models[i].ID, model.ID) {
				t.Models[i], replaced = model, true
			}
		}
		if !replaced {
			t.Models = append(t.Models, model)
		}
	}
	t.index()
}

// Checked is the date the table's figures were checked.
func (p *Prices) Checked() string { return p.table.Checked }

// Source is where the figures came from.
func (p *Prices) Source() string { return p.table.Source }

// datedSuffix is a snapshot date on a model id (claude-haiku-4-5-20251001).
var datedSuffix = regexp.MustCompile(`-\d{8}$`)

// Canonical maps a model name as a transcript, a flag or a job records it onto a price row's id:
// lower case, no "anthropic." prefix, no "[1m]" context suffix, no "@version" or date suffix;
// then an exact id or an alias (opus, sonnet, haiku, fable). Unknown models come back as cleaned
// and false.
func (p *Prices) Canonical(model string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	name = strings.TrimPrefix(name, "anthropic.")
	if i := strings.IndexAny(name, "[@"); i >= 0 {
		name = name[:i]
	}
	name = datedSuffix.ReplaceAllString(name, "")
	if row, ok := p.table.byID[name]; ok {
		return row.ID, true
	}
	if row, ok := p.table.byAlias[name]; ok {
		return row.ID, true
	}
	return name, false
}

// Key is the pricing key for one call: the canonical model plus the modifiers that change its
// price. Modifiers are added only when they apply to that model, in a fixed order, so equal
// calls share a key.
func (p *Prices) Key(model, speed, geo string, prompt int64) string {
	id, known := p.Canonical(model)
	if id == "" {
		id = "unknown"
	}
	if !known {
		return id
	}
	row := p.table.byID[id]
	key := id
	if speed == "fast" && row.Fast > 0 {
		key += "+fast"
	}
	if row.Long != nil && prompt > row.Long.Over {
		key += "+long"
	}
	if strings.EqualFold(geo, "us") && p.table.USMultiplier > 0 {
		key += "+us"
	}
	return key
}

// ModelOf is the model part of a pricing key.
func ModelOf(key string) string {
	model, _, _ := strings.Cut(key, "+")
	return model
}

// rates resolves a pricing key to its effective per-million rates.
func (p *Prices) rates(key string) (Rates, bool) {
	parts := strings.Split(key, "+")
	row, ok := p.table.byID[parts[0]]
	if !ok {
		return Rates{}, false
	}
	rates, multiplier := row.Rates, 1.0
	for _, modifier := range parts[1:] {
		switch modifier {
		case "fast":
			if row.Fast > 0 {
				multiplier *= row.Fast
			}
		case "long":
			if row.Long != nil {
				rates = row.Long.Rates
			}
		case "us":
			if p.table.USMultiplier > 0 {
				multiplier *= p.table.USMultiplier
			}
		}
	}
	rates.Input *= multiplier
	rates.Output *= multiplier
	rates.CacheWrite5m *= multiplier
	rates.CacheWrite1h *= multiplier
	rates.CacheRead *= multiplier
	return rates, true
}

// Cost prices tokens under a pricing key, in nano-dollars (1e-9 USD, so the cheapest cache read,
// a hundredth of a cent per million, is still a whole number per thousand tokens). ok is false
// for a model the table does not know: its cost is unknown, never zero.
func (p *Prices) Cost(key string, tokens Tokens) (nanoUSD int64, ok bool) {
	rates, ok := p.rates(key)
	if !ok {
		return 0, false
	}
	// USD per million tokens is 1000 nano-dollars per token.
	total := float64(tokens.Input)*rates.Input + float64(tokens.Output)*rates.Output +
		float64(tokens.CacheWrite5m)*rates.CacheWrite5m + float64(tokens.CacheWrite1h)*rates.CacheWrite1h +
		float64(tokens.CacheRead)*rates.CacheRead
	return int64(math.Round(total * 1000)), true
}
