// The Service: asking, reading, waiting, answering, cancelling and expiring questions.
//
// Settling (answer, expiry, cancel) happens exactly once per question: the store's UPDATE is
// conditional on status = 'open', so an answer racing the expiry sweep has one winner and the
// loser gets ErrNotOpen. At the moment of settling the service decides how the outcome reaches the
// asker: through the long-poll if someone is waiting on it (or was a moment ago, between two
// polls), else typed into its pane by the Deliverer.
//
// Only the asker counts as waiting: a poll from another caller reads the question but never
// changes how its answer is delivered (R1b N2). A poll delivery is confirmed when the settled
// question is actually handed to the asker's poll (Collected); one nobody collects within
// collectGrace is switched to typing, so an asker whose wait was cut off still gets its answer
// (R1b-C06).

package questions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
)

const (
	// DefaultSweepInterval is how often expired questions are settled.
	DefaultSweepInterval = 15 * time.Second
	// MaxWait caps one long-poll; the client polls again. 90 s is Meat Prompt's own value plus
	// headroom, and stays under common proxy idle timeouts.
	MaxWait = 120 * time.Second
	// pollGrace is how long after a long-poll ends its asker still counts as waiting: a CLI or
	// MCP loop re-polls within milliseconds, and the answer must not be typed into the pane of an
	// agent that is about to read it from the poll.
	pollGrace = 10 * time.Second
	// listLimit bounds one list response; the Inbox shows open questions and recent history.
	listLimit = 200
	// collectGrace is how long a poll delivery may wait to be collected before the answer is
	// typed instead: longer than pollGrace, so a looping asker that re-polls collects it first.
	collectGrace = 30 * time.Second
)

// ErrNotFound and ErrNotOpen are the lifecycle's refusals.
var (
	ErrNotFound = errors.New("no such question")
	ErrNotOpen  = errors.New("the question is no longer open")
)

// Options configure a Service. Store is required.
type Options struct {
	Store  *store.Store
	Hub    *hub.Hub // nil: no events
	Logger *slog.Logger
	Now    func() time.Time
	// OnAsked runs after a question is stored (serve wires --notify-url here). It must not block.
	OnAsked func(Question)
	// OnSettled runs after a question settles (the Deliverer's kick). It must not block.
	OnSettled func(Question)
	// Attachments lists a question's files for Get (internal/attach, wired in serve). Nil: none.
	Attachments AttachmentSource
}

// AttachmentSource lists the files of a question: the asker's (role ask) and the human's sent
// with the answer (role answer, by reference to the question).
type AttachmentSource interface {
	List(ctx context.Context, kind, ownerID, role string) ([]attach.Attachment, error)
	ListByRef(ctx context.Context, role, ref string) ([]attach.Attachment, error)
}

// Service is the question inbox.
type Service struct {
	opts Options

	mu      sync.Mutex
	waiters map[int64]*waitState
}

// waitState tracks the long-polls on one question. Pollers are counted by the caller's pane, so
// a settle can ask whether the asker itself is waiting; anyone may poll, only the asker counts.
type waitState struct {
	active   int                  // long-polls in flight, from anyone
	polling  map[string]int       // long-polls in flight by pane (identified callers only)
	lastSeen map[string]time.Time // per pane: when its last poll ended normally (or its wait=true ask)
	settled  chan struct{}        // closed when the question settles
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, waiters: map[int64]*waitState{}}
}

// ---- Asking and reading --------------------------------------------------------------------

// Ask validates and stores a question from caller and returns it.
func (s *Service) Ask(ctx context.Context, input AskInput, caller Caller) (Question, error) {
	input, err := normalise(input)
	if err != nil {
		return Question{}, err
	}
	record, err := toRecord(input, caller, s.opts.Now())
	if err != nil {
		return Question{}, err
	}
	id, err := s.opts.Store.InsertQuestion(ctx, record)
	if err != nil {
		return Question{}, err
	}
	if input.Wait && caller.PaneID != "" {
		s.mu.Lock()
		s.waitStateLocked(id).lastSeen[caller.PaneID] = s.opts.Now()
		s.mu.Unlock()
	}
	question, err := s.Get(ctx, FormatID(id))
	if err != nil {
		return Question{}, err
	}
	s.publish(question, "asked")
	if s.opts.OnAsked != nil {
		s.opts.OnAsked(question)
	}
	return question, nil
}

// Get returns one question with its files.
func (s *Service) Get(ctx context.Context, id string) (Question, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return Question{}, ErrNotFound
	}
	record, err := s.opts.Store.GetQuestion(ctx, numeric)
	if errors.Is(err, store.ErrNotFound) {
		return Question{}, ErrNotFound
	}
	if err != nil {
		return Question{}, err
	}
	question := fromRecord(record, true)
	if source := s.opts.Attachments; source != nil {
		question.Attachments, _ = source.List(ctx, attach.OwnerQuestion, question.ID, attach.RoleAsk)
		question.AnswerAttachments, _ = source.ListByRef(ctx, attach.RoleAnswer, question.ID)
	}
	s.withReceipt(ctx, &question)
	return question, nil
}

// List returns questions with the given status ("" or "all" for every status, or a
// comma-separated list), open first, without file contents.
func (s *Service) List(ctx context.Context, status string) ([]Question, error) {
	statuses, err := parseStatuses(status)
	if err != nil {
		return nil, err
	}
	records, err := s.opts.Store.ListQuestions(ctx, statuses, listLimit)
	if err != nil {
		return nil, err
	}
	out := make([]Question, 0, len(records))
	for _, record := range records {
		out = append(out, fromRecord(record, false))
	}
	return out, nil
}

// Summary counts open questions for the Inbox badge.
func (s *Service) Summary(ctx context.Context) (Summary, error) {
	open, blocking, err := s.opts.Store.CountOpenQuestions(ctx)
	return Summary{Open: open, Blocking: blocking}, err
}

// OpenCounts returns how many questions are open from pane ("" counts the unidentified askers
// together), and in total: the agent socket's quotas.
func (s *Service) OpenCounts(ctx context.Context, pane string) (mine, total int, err error) {
	return s.opts.Store.CountOpenFrom(ctx, pane)
}

func parseStatuses(text string) ([]string, error) {
	if text == "" || text == "all" {
		return nil, nil
	}
	var out []string
	for _, part := range strings.Split(text, ",") {
		switch Status(strings.TrimSpace(part)) {
		case StatusOpen, StatusAnswered, StatusExpired, StatusCancelled:
			out = append(out, strings.TrimSpace(part))
		default:
			return nil, invalid("status must be open, answered, expired, cancelled or all (got %q)", part)
		}
	}
	return out, nil
}

// ---- Long-poll -----------------------------------------------------------------------------

// Wait returns the question once it is settled, or as it stands after timeout (capped at
// MaxWait), or when ctx ends. While it waits, a caller who is the asker counts as waiting, so a
// settle in that window is answered through the poll rather than typed into its pane.
//
// A poll that ends because its caller went away (ctx cancelled) does not leave the asker
// counted as about to poll again: the shipped clients give up on cancellation, so the answer is
// typed instead (R1b-C06). The entry is removed as soon as nothing needs it, including when the
// question is missing or already settled (R1b-C07).
func (s *Service) Wait(ctx context.Context, id string, timeout time.Duration, caller Caller) (Question, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return Question{}, ErrNotFound
	}
	if timeout > MaxWait {
		timeout = MaxWait
	}
	pane := caller.PaneID
	s.mu.Lock()
	state := s.waitStateLocked(numeric)
	state.active++
	if pane != "" {
		state.polling[pane]++
	}
	settled := state.settled
	s.mu.Unlock()
	resolved := false // false: the caller went away, or the question was never waitable
	defer func() { s.endPoll(numeric, state, pane, resolved) }()

	// The channel was taken before this read, so a settle between the two still wakes us.
	question, err := s.Get(ctx, id)
	if err != nil || question.Status != StatusOpen || timeout <= 0 {
		return question, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-settled:
	case <-timer.C:
	case <-ctx.Done():
		return Question{}, ctx.Err()
	}
	resolved = true
	return s.Get(context.WithoutCancel(ctx), id)
}

// endPoll is Wait's bookkeeping on the way out. resolved says the poll ran its course (settled
// or timed out) rather than being cut off, so its pane may re-poll within pollGrace.
func (s *Service) endPoll(id int64, state *waitState, pane string, resolved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state.active--
	if pane != "" {
		if state.polling[pane]--; state.polling[pane] <= 0 {
			delete(state.polling, pane)
		}
		if resolved && !isClosed(state.settled) {
			state.lastSeen[pane] = s.opts.Now()
		}
	}
	if state.active == 0 && (isClosed(state.settled) || len(state.lastSeen) == 0) && s.waiters[id] == state {
		delete(s.waiters, id) // settled, missing, or nobody's grace to remember
	}
}

func (s *Service) waitStateLocked(id int64) *waitState {
	state, ok := s.waiters[id]
	if !ok {
		state = &waitState{polling: map[string]int{}, lastSeen: map[string]time.Time{}, settled: make(chan struct{})}
		s.waiters[id] = state
	}
	return state
}

func isClosed(channel chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

// pruneWaitersLocked drops entries nobody polls whose grace has run out (s.mu held), so asks
// with wait=true and polls on questions that are never settled do not accumulate (R1b-C07).
func (s *Service) pruneWaitersLocked() {
	now := s.opts.Now()
	for id, state := range s.waiters {
		for pane, seen := range state.lastSeen {
			if now.Sub(seen) >= pollGrace {
				delete(state.lastSeen, pane)
			}
		}
		if state.active == 0 && len(state.lastSeen) == 0 {
			delete(s.waiters, id)
		}
	}
}

// isWaitedOnLocked reports whether the asker in pane is polling id now or was within pollGrace.
func (s *Service) isWaitedOnLocked(id int64, pane string) bool {
	state, ok := s.waiters[id]
	if !ok || pane == "" {
		return false
	}
	seen, recent := state.lastSeen[pane]
	return state.polling[pane] > 0 || (recent && s.opts.Now().Sub(seen) < pollGrace)
}

// Collected records that a settled question was handed to its asker's poll (the agent socket
// calls it after writing the answer). It confirms a poll delivery; for any other caller or
// delivery it does nothing.
func (s *Service) Collected(ctx context.Context, question Question, caller Caller) {
	if question.Delivery != DeliveryPoll || question.DeliveredAt != nil || caller.PaneID == "" || caller.PaneID != question.SourcePane {
		return
	}
	numeric, _ := ParseID(question.ID)
	if err := s.opts.Store.MarkPollCollected(ctx, numeric, s.opts.Now()); err != nil {
		s.opts.Logger.Warn("recording a collected answer", "question", question.ID, "err", err)
	}
	_, _ = s.opts.Store.MarkQuestionReceived(ctx, numeric, s.opts.Now()) // its own call returned it (6.19)
}

// RetypeUncollected switches poll deliveries nobody collected within collectGrace, and whose
// asker is not polling right now, to typing, and returns how many (R1b-C06). The asker's wait
// was cut off (a cancelled tool call, a dropped connection, a crowsnest restart), so without
// this the answer would sit in the Inbox marked delivered-by-poll and never reach the agent.
func (s *Service) RetypeUncollected(ctx context.Context) (int, error) {
	candidates, err := s.opts.Store.UncollectedPolls(ctx, s.opts.Now().Add(-collectGrace))
	if err != nil {
		return 0, err
	}
	retyped := 0
	for _, candidate := range candidates {
		s.mu.Lock()
		waiting := s.isWaitedOnLocked(candidate.ID, candidate.SourcePane)
		s.mu.Unlock()
		if waiting {
			continue
		}
		changed, err := s.opts.Store.RetypePoll(ctx, candidate.ID)
		if err != nil {
			return retyped, err
		}
		if changed {
			retyped++
		}
	}
	return retyped, nil
}

// ---- Settling ------------------------------------------------------------------------------

// Answer records the human's answer to an open question.
func (s *Service) Answer(ctx context.Context, id string, answer AnswerInput) (Question, error) {
	question, err := s.Get(ctx, id)
	if err != nil {
		return Question{}, err
	}
	if question.Status != StatusOpen {
		return Question{}, ErrNotOpen
	}
	value, note, err := checkAnswer(question, answer)
	if err != nil {
		return Question{}, err
	}
	return s.settle(ctx, question, StatusAnswered, value, note, false)
}

// Cancel withdraws an open question. byAsker is true when the asker itself cancelled it (the
// socket's DELETE): it knows, so nothing is typed back to it.
func (s *Service) Cancel(ctx context.Context, id string, byAsker bool) (Question, error) {
	question, err := s.Get(ctx, id)
	if err != nil {
		return Question{}, err
	}
	if question.Status != StatusOpen {
		return Question{}, ErrNotOpen
	}
	return s.settle(ctx, question, StatusCancelled, nil, "", byAsker)
}

// SweepExpired settles every open question past its expiry as expired, and returns how many.
func (s *Service) SweepExpired(ctx context.Context) (int, error) {
	ids, err := s.opts.Store.ExpiredOpenQuestions(ctx, s.opts.Now())
	if err != nil {
		return 0, err
	}
	expired := 0
	for _, id := range ids {
		question, err := s.Get(ctx, FormatID(id))
		if err != nil {
			continue
		}
		if _, err := s.settle(ctx, question, StatusExpired, nil, "", false); err == nil {
			expired++
		} else if !errors.Is(err, ErrNotOpen) {
			return expired, err
		}
	}
	return expired, nil
}

// RunSweeper expires questions, re-routes uncollected poll answers and prunes idle waiter entries
// every interval (DefaultSweepInterval when zero) until ctx ends.
func (s *Service) RunSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.SweepExpired(ctx); err != nil && ctx.Err() == nil {
			s.opts.Logger.Warn("expiring questions", "err", err)
		}
		if retyped, err := s.RetypeUncollected(ctx); err != nil && ctx.Err() == nil {
			s.opts.Logger.Warn("re-routing uncollected answers", "err", err)
		} else if retyped > 0 {
			s.opts.Logger.Info("answers whose waiting asker went away will be typed into its pane", "count", retyped)
		}
		s.mu.Lock()
		s.pruneWaitersLocked()
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// settle is the one place a question leaves open. It decides the delivery, writes the row
// conditionally, wakes the long-polls and announces the change.
func (s *Service) settle(ctx context.Context, question Question, status Status, value json.RawMessage, note string, quiet bool) (Question, error) {
	numeric, _ := ParseID(question.ID)
	s.mu.Lock()
	delivery := s.deliveryForLocked(numeric, question, quiet)
	err := s.opts.Store.SettleQuestion(ctx, numeric, string(status), value, note, string(delivery), s.opts.Now())
	if err == nil {
		state := s.waitStateLocked(numeric)
		close(state.settled)
		// Keep the entry while a poll is in flight; a settled question needs no new ones.
		if state.active == 0 {
			delete(s.waiters, numeric)
		}
	}
	s.mu.Unlock()
	switch {
	case errors.Is(err, store.ErrNotOpen):
		return Question{}, ErrNotOpen
	case errors.Is(err, store.ErrNotFound):
		return Question{}, ErrNotFound
	case err != nil:
		return Question{}, err
	}
	settled, err := s.Get(context.WithoutCancel(ctx), question.ID)
	if err != nil {
		return Question{}, err
	}
	s.publish(settled, string(status))
	if s.opts.OnSettled != nil {
		s.opts.OnSettled(settled)
	}
	return settled, nil
}

// deliveryForLocked decides how the outcome of a question reaches its asker (s.mu held).
func (s *Service) deliveryForLocked(id int64, question Question, quiet bool) Delivery {
	switch {
	case question.Kind == KindNotice, quiet, question.SourcePane == "":
		return DeliveryNone
	case s.isWaitedOnLocked(id, question.SourcePane):
		return DeliveryPoll
	default:
		return DeliveryType
	}
}

// publish announces a change on the hub.
func (s *Service) publish(question Question, change string) {
	if s.opts.Hub == nil {
		return
	}
	s.opts.Hub.Publish(hub.Event{Type: "question", Data: Event{
		ID: question.ID, Status: question.Status, Title: question.Title, Kind: question.Kind, Urgency: question.Urgency, Change: change,
	}})
}

// ---- Record conversion ---------------------------------------------------------------------

func toRecord(input AskInput, caller Caller, now time.Time) (store.QuestionRecord, error) {
	options, err := json.Marshal(nonNil(input.Options))
	if err != nil {
		return store.QuestionRecord{}, fmt.Errorf("encoding options: %w", err)
	}
	contextJSON, err := json.Marshal(nonNilMap(input.Context))
	if err != nil {
		return store.QuestionRecord{}, fmt.Errorf("encoding context: %w", err)
	}
	files, err := json.Marshal(nonNil(input.Files))
	if err != nil {
		return store.QuestionRecord{}, fmt.Errorf("encoding files: %w", err)
	}
	expiry := time.Duration(input.ExpiresInHours * float64(time.Hour))
	return store.QuestionRecord{
		Title: input.Title, BodyMD: input.BodyMD, Kind: string(input.Kind), Multi: input.Multi,
		Options: options, Recommend: input.Recommend, Context: contextJSON, Urgency: string(input.Urgency),
		Files: files, SourcePane: caller.PaneID, JobID: caller.JobID, Wait: input.Wait,
		CreatedAt: now, ExpiresAt: now.Add(expiry),
	}, nil
}

func fromRecord(record store.QuestionRecord, withFiles bool) Question {
	question := Question{
		ID: FormatID(record.ID), Title: record.Title, BodyMD: record.BodyMD, Kind: Kind(record.Kind), Multi: record.Multi,
		Recommend: record.Recommend, Urgency: Urgency(record.Urgency), Status: Status(record.Status),
		AnswerNote: record.AnswerNote, SourcePane: record.SourcePane, JobID: record.JobID, Wait: record.Wait,
		CreatedAt: record.CreatedAt.UTC(), ExpiresAt: record.ExpiresAt.UTC(), SettledAt: timePtr(record.SettledAt),
		Delivery: Delivery(record.Delivery), DeliveredAt: timePtr(record.DeliveredAt), DeliveryError: record.DeliveryError,
		Options: []Option{}, Context: map[string]string{},
	}
	// The JSON columns were written by toRecord from validated input; a row that does not decode
	// is shown with that part empty rather than failing the whole inbox.
	_ = json.Unmarshal(record.Options, &question.Options)
	_ = json.Unmarshal(record.Context, &question.Context)
	question.FileCount = record.FileCount
	if withFiles {
		_ = json.Unmarshal(record.Files, &question.Files)
	}
	if len(record.AnswerValue) > 0 {
		_ = json.Unmarshal(record.AnswerValue, &question.AnswerValue)
	}
	return question
}

func timePtr(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func nonNilMap(items map[string]string) map[string]string {
	if items == nil {
		return map[string]string{}
	}
	return items
}
