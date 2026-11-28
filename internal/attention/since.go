// "Since you left" (DESIGN.md 22.3): what finished, failed, was cancelled or asked since this
// browser was last in use. Each browser keeps a random id in its localStorage and sends a beat
// while its page is visible; the server remembers, per id, when it was last active and when it
// last left, so the phone and the desktop each see their own "since". The record lives in kv
// (`browser.<id>`), not in the browser, so clearing a tab's state does not replay a day.

package attention

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

const (
	// AwayAfter is how long a browser must have been silent for its next beat to count as a
	// return: shorter gaps are a tab switch, not leaving.
	AwayAfter = 10 * time.Minute
	// FirstVisitWindow is what a browser never seen before is shown: the last half day.
	FirstVisitWindow = 12 * time.Hour
	// maxSinceEvents caps the list; the board has the rest.
	maxSinceEvents  = 50
	kvBrowserPrefix = "browser."
)

var browserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ErrBadBrowser refuses a browser id that is not one the app makes.
var ErrBadBrowser = errors.New("browser id must be 8 to 64 of A-Z, a-z, 0-9, _ and -")

// browserRecord is one browser's presence as "since you left" needs it.
type browserRecord struct {
	ActiveAt int64 `json:"active_at"` // unix seconds of its last beat
	LeftAt   int64 `json:"left_at"`   // the start of its "since you left" window
}

// SinceEvent is one thing that happened in the window.
type SinceEvent struct {
	Kind    string      `json:"kind"` // done, failed, cancelled, asked, answered
	At      time.Time   `json:"at"`
	Title   string      `json:"title"`
	Detail  string      `json:"detail,omitempty"`
	Project *ProjectRef `json:"project"`
	JobID   string      `json:"job_id,omitempty"`
	Ref     string      `json:"ref,omitempty"` // the question id for asked
}

// Since is the "since you left" panel's data.
type Since struct {
	Since  time.Time      `json:"since"`
	First  bool           `json:"first"` // this browser was never seen before
	Events []SinceEvent   `json:"events"`
	Counts map[string]int `json:"counts"` // per kind
}

// SinceSources are what the events are read from.
type SinceSources struct {
	Jobs      []jobs.Job
	Questions []questions.Question
	Projects  map[int64]ProjectRef
}

// SinceEvents lists what happened after since, newest first (pure).
func SinceEvents(src SinceSources, since time.Time) []SinceEvent {
	var events []SinceEvent
	jobProject := map[string]int64{}
	for _, job := range src.Jobs {
		jobProject[job.ID] = job.ProjectID
		if job.SettledAt == nil || !job.SettledAt.After(since) {
			continue
		}
		event := SinceEvent{Kind: string(job.State), At: *job.SettledAt, Title: job.Title, JobID: job.ID, Project: refOf(src.Projects, job.ProjectID)}
		switch job.State {
		case jobs.StateDone:
			event.Detail = firstLine(job.ResultMD)
		case jobs.StateFailed:
			event.Detail = job.FailReason
		case jobs.StateCancelled:
		default:
			continue
		}
		events = append(events, event)
	}
	for _, question := range src.Questions {
		// A question still open is in Needs you, above this list (R10 O-F16: the first five
		// entries repeated the queue two screens up); one settled since is news.
		if question.Kind == questions.KindNotice || !question.CreatedAt.After(since) || question.Status == questions.StatusOpen {
			continue
		}
		events = append(events, SinceEvent{Kind: "asked", At: question.CreatedAt, Title: question.Title, JobID: question.JobID,
			Ref: question.ID, Detail: string(question.Status), Project: refOf(src.Projects, jobProject[question.JobID])})
	}
	// Answers given in the window, with how far each got to its agent (DESIGN.md 6.19): an
	// answer held at a dialog or uncertain is news, not a done deal.
	for _, question := range src.Questions {
		if question.Status != questions.StatusAnswered || question.SettledAt == nil || !question.SettledAt.After(since) || question.Receipt == "" {
			continue
		}
		events = append(events, SinceEvent{Kind: "answered", At: *question.SettledAt, Title: question.Title, JobID: question.JobID,
			Ref: question.ID, Detail: receiptWords(question.Receipt), Project: refOf(src.Projects, jobProject[question.JobID])})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if len(events) > maxSinceEvents {
		events = events[:maxSinceEvents]
	}
	return events
}

// receiptWords is an answer's receipt for the "since you left" line.
func receiptWords(receipt string) string {
	switch receipt {
	case questions.ReceiptReceived:
		return "received by the agent"
	case questions.ReceiptDelivered:
		return "typed to the agent, not yet seen taken in"
	case questions.ReceiptHeld:
		return "held: the agent could not take it yet"
	case questions.ReceiptUncertain:
		return "delivery uncertain: check whether it arrived"
	case questions.ReceiptFailed:
		return "not delivered"
	case questions.ReceiptStored:
		return "stored, not yet delivered"
	}
	return receipt
}

func refOf(projects map[int64]ProjectRef, id int64) *ProjectRef {
	if ref, ok := projects[id]; ok && id != 0 {
		return &ref
	}
	return nil
}

func firstLine(text string) string {
	for i, r := range text {
		if r == '\n' {
			return text[:i]
		}
	}
	return text
}

// ---- Per-browser presence ----------------------------------------------------------------

// Beat records that a browser is in use now. A beat after more than AwayAfter of silence opens a
// new "since you left" window starting when it was last active; a browser never seen before
// gets the last FirstVisitWindow.
func (s *Service) Beat(ctx context.Context, browser string) error {
	if !browserIDPattern.MatchString(browser) {
		return ErrBadBrowser
	}
	now := s.opts.Now()
	record, found := s.readBrowser(ctx, browser)
	switch {
	case !found:
		record.LeftAt = now.Add(-FirstVisitWindow).Unix()
	case now.Sub(time.Unix(record.ActiveAt, 0)) > AwayAfter:
		record.LeftAt = record.ActiveAt
	}
	record.ActiveAt = now.Unix()
	return s.writeBrowser(ctx, browser, record)
}

// MarkSeen closes a browser's window: "since you left" is empty until it next leaves.
func (s *Service) MarkSeen(ctx context.Context, browser string) error {
	if !browserIDPattern.MatchString(browser) {
		return ErrBadBrowser
	}
	now := s.opts.Now().Unix()
	record, _ := s.readBrowser(ctx, browser)
	record.ActiveAt, record.LeftAt = now, now
	return s.writeBrowser(ctx, browser, record)
}

// SinceFor builds a browser's "since you left" from the given sources. A browser with no record
// yet is treated as a first visit (the beat that follows records it).
func (s *Service) SinceFor(ctx context.Context, browser string, src SinceSources) (Since, error) {
	if !browserIDPattern.MatchString(browser) {
		return Since{}, ErrBadBrowser
	}
	record, found := s.readBrowser(ctx, browser)
	since := time.Unix(record.LeftAt, 0)
	if !found {
		since = s.opts.Now().Add(-FirstVisitWindow)
	}
	events := SinceEvents(src, since)
	if events == nil {
		events = []SinceEvent{}
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Kind]++
	}
	return Since{Since: since, First: !found, Events: events, Counts: counts}, nil
}

func (s *Service) readBrowser(ctx context.Context, browser string) (browserRecord, bool) {
	var record browserRecord
	if s.opts.KV == nil {
		return record, false
	}
	raw, err := s.opts.KV.GetKV(ctx, kvBrowserPrefix+browser)
	if err != nil || raw == "" || json.Unmarshal([]byte(raw), &record) != nil {
		return browserRecord{}, false
	}
	return record, true
}

func (s *Service) writeBrowser(ctx context.Context, browser string, record browserRecord) error {
	if s.opts.KV == nil {
		return errors.New("attention: no store to keep the browser's record in")
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.opts.KV.SetKV(ctx, kvBrowserPrefix+browser, string(body))
}
