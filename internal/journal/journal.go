// Package journal is crowsnest's record of what it did on its own, or did for the human, that is
// worth seeing later (DESIGN.md 7.4 and 11): pokes and skipped pokes with the reason, prompts
// sent from the web app, answers, orchestrator restarts, supervisor alerts and backstop nudges.
//
// Writing an entry never fails the caller's work: an entry that cannot be stored is logged and
// dropped, because a poke or an answer must not be undone by a full disk. Every stored entry is
// published as a `journal` event so an open Journal panel refreshes.
package journal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
)

// Kind names what an entry records.
type Kind string

// Journal kinds.
const (
	KindPoke     Kind = "poke"     // a poke was typed into the orchestrator
	KindSkip     Kind = "skip"     // a poke was due (or asked for) but not sent, with the reason
	KindSend     Kind = "send"     // the human sent a prompt from the web app
	KindAnswer   Kind = "answer"   // the human answered, dismissed or cancelled a question
	KindRestart  Kind = "restart"  // the orchestrator was restarted (automatically or by hand)
	KindAlert    Kind = "alert"    // something needs the human: a supervisor notice, a held poke
	KindBackstop Kind = "backstop" // the herdr-side backstop nudged an agent without hooks
	KindTask     Kind = "task"     // a scheduled task started its job (7.8)
	KindUpkeep   Kind = "upkeep"   // the orchestrator was compacted or handed over to a new conversation (7.9)
	KindSettings Kind = "settings" // a setting was changed in the app, or the first run finished (3.2)
	// KindStorage records backups, restores and the retention sweeper (4.17, 4.19).
	KindStorage Kind = "storage"
)

// Kinds lists every kind, for validating a filter.
var Kinds = []Kind{KindPoke, KindSkip, KindSend, KindAnswer, KindRestart, KindAlert, KindBackstop, KindTask, KindUpkeep, KindSettings, KindStorage}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	for _, known := range Kinds {
		if k == known {
			return true
		}
	}
	return false
}

const (
	// maxSummaryRunes keeps a summary to one readable line.
	maxSummaryRunes = 300
	// DefaultLimit and MaxLimit bound one page.
	DefaultLimit = 50
	MaxLimit     = 200
)

// Entry is one journal entry as the API returns it.
type Entry struct {
	ID      int64          `json:"id"`
	At      time.Time      `json:"at"`
	Kind    Kind           `json:"kind"`
	Summary string         `json:"summary"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// Event is the data of a `journal` event.
type Event struct {
	ID      int64  `json:"id"`
	Kind    Kind   `json:"kind"`
	Summary string `json:"summary"`
}

// Query selects a page: newest first, optionally only some kinds, and before an id for paging.
type Query struct {
	Kinds    []Kind
	BeforeID int64
	Limit    int
}

// Page is one page of entries. NextBefore is the BeforeID of the next (older) page, 0 when this
// is the last.
type Page struct {
	Entries    []Entry `json:"entries"`
	Total      int     `json:"total"`
	NextBefore int64   `json:"next_before"`
}

// Store is the part of store.Store the journal uses.
type Store interface {
	InsertJournal(ctx context.Context, record store.JournalRecord) (int64, error)
	ListJournal(ctx context.Context, query store.JournalQuery) ([]store.JournalRecord, int, error)
}

// Journal writes and reads entries. A nil *Journal is valid and records nothing, so callers in
// tests and in --no-orchestrator paths need no special case.
type Journal struct {
	store  Store
	hub    *hub.Hub
	logger *slog.Logger
	now    func() time.Time
}

// New returns a journal over st. hub may be nil (no events); now defaults to time.Now.
func New(st Store, events *hub.Hub, logger *slog.Logger, now func() time.Time) *Journal {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Journal{store: st, hub: events, logger: logger, now: now}
}

// Add records an entry. detail may be nil. Failures are logged, never returned (package doc).
func (j *Journal) Add(ctx context.Context, kind Kind, summary string, detail map[string]any) {
	if j == nil || j.store == nil {
		return
	}
	summary = oneLine(summary)
	encoded := "{}"
	if len(detail) > 0 {
		if body, err := json.Marshal(detail); err == nil {
			encoded = string(body)
		}
	}
	id, err := j.store.InsertJournal(context.WithoutCancel(ctx), store.JournalRecord{At: j.now(), Kind: string(kind), Summary: summary, DetailJSON: encoded})
	if err != nil {
		j.logger.Warn("writing a journal entry", "kind", kind, "err", err)
		return
	}
	if j.hub != nil {
		j.hub.Publish(hub.Event{Type: "journal", Data: Event{ID: id, Kind: kind, Summary: summary}})
	}
}

// List returns one page of entries.
func (j *Journal) List(ctx context.Context, query Query) (Page, error) {
	if j == nil || j.store == nil {
		return Page{Entries: []Entry{}}, nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	kinds := make([]string, len(query.Kinds))
	for i, kind := range query.Kinds {
		kinds[i] = string(kind)
	}
	// One more than asked tells whether an older page exists without a second query.
	records, total, err := j.store.ListJournal(ctx, store.JournalQuery{Kinds: kinds, BeforeID: query.BeforeID, Limit: limit + 1})
	if err != nil {
		return Page{}, fmt.Errorf("reading the journal: %w", err)
	}
	page := Page{Entries: make([]Entry, 0, min(len(records), limit)), Total: total}
	for i, record := range records {
		if i == limit {
			page.NextBefore = page.Entries[limit-1].ID
			break
		}
		page.Entries = append(page.Entries, toEntry(record))
	}
	return page, nil
}

func toEntry(record store.JournalRecord) Entry {
	entry := Entry{ID: record.ID, At: record.At, Kind: Kind(record.Kind), Summary: record.Summary}
	if record.DetailJSON != "" && record.DetailJSON != "{}" {
		_ = json.Unmarshal([]byte(record.DetailJSON), &entry.Detail)
	}
	return entry
}

// ParseKinds turns a comma-separated filter ("poke,skip") into kinds, refusing unknown ones.
func ParseKinds(text string) ([]Kind, error) {
	var kinds []Kind
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !Kind(part).Valid() {
			return nil, fmt.Errorf("unknown journal kind %q", part)
		}
		kinds = append(kinds, Kind(part))
	}
	return kinds, nil
}

// oneLine collapses whitespace and caps the summary.
func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > maxSummaryRunes {
		text = string(runes[:maxSummaryRunes-1]) + "…"
	}
	return text
}
