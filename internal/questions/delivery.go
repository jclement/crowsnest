// The Deliverer types a settled question's outcome into the asker's pane, as one line:
//
//	[crowsnest answer q_7] Ship the migration tonight? → Ship it (note: after 10pm)
//
// It works whether the asker is idle (the line wakes it) or working (Claude queues it). When the
// asker is at a dialog, or has a draft in its input box, or cannot be found, nothing is typed and
// the delivery is held and retried with backoff. Delivery is at most once: a write-ahead claim is
// set before typing, released only when the attempt provably typed nothing, and a claim left by a
// crash is never retried (typing an answer twice is worse than once).

package questions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
)

// ErrHold is what a Prompter returns when it typed nothing and a later attempt may succeed: the
// agent is at a dialog, has a draft, or is not there right now.
var ErrHold = errors.New("nothing typed; try again later")

// ErrUncertain is what a Prompter returns when the text was handed to the terminal but whether it
// was typed is unknown (herdr's answer was lost). Like any other unknown outcome it is recorded
// and never retried; it has its own wording so the Inbox says what to check.
var ErrUncertain = errors.New("delivery uncertain: crowsnest lost herdr's answer after handing it the line; check the agent's terminal before saying it again")

// Prompter types text into the agent in paneID. It returns nil when the text was typed (whether
// or not a turn visibly started), an error wrapping ErrHold when nothing was typed, and any other
// error when the outcome is unknown.
type Prompter func(ctx context.Context, paneID, text string) error

const (
	// DefaultDeliveryInterval is how often pending deliveries are looked at without a kick.
	DefaultDeliveryInterval = 3 * time.Second
	// MaxLineRunes caps the typed line. An answer longer than that is not cut: the line tells the
	// agent to read it with the get_question tool or `crowsnest question` instead.
	MaxLineRunes = 600
	// maxQuotedTitleRunes bounds the title quoted at the end of the line (validation allows 200).
	maxQuotedTitleRunes = 200
	// holdBackoffMax caps the wait between attempts on a held delivery.
	holdBackoffMax = time.Minute
	// holdGiveUp is how long a held delivery keeps being retried.
	holdGiveUp = 7 * 24 * time.Hour
	// deliveryTimeout bounds one attempt (agent.prompt waits up to 30 s for the turn).
	deliveryTimeout = 45 * time.Second
	// pendingScanLimit bounds the ids one pass looks at; deliveryBatch bounds the attempts.
	pendingScanLimit = 1000
	deliveryBatch    = 20
	// cleanupTimeout bounds the store writes that close an attempt. They run detached from the
	// attempt's context: shutdown cancelling an attempt that provably typed nothing must still
	// release its claim, or the next start would abandon a retryable answer (R1b-C08).
	cleanupTimeout = 5 * time.Second
)

// DelivererOptions configure a Deliverer. Store and Prompt are required.
type DelivererOptions struct {
	Store    *store.Store
	Prompt   Prompter
	Logger   *slog.Logger
	Now      func() time.Time
	Interval time.Duration
	// OnDelivered runs after a delivery finished (delivered or given up), so the service can
	// announce it. It must not block.
	OnDelivered func(id int64)
}

// Deliverer types answers back. Build with NewDeliverer, then Run.
type Deliverer struct {
	opts DelivererOptions
	kick chan struct{}

	mu      sync.Mutex
	backoff map[int64]heldDelivery
	// cursor is where the next pass's scan starts (R2-C14): each pass reads the next
	// pendingScanLimit ids and wraps to the start after a short page, so a thousand held
	// answers cannot keep a newer, deliverable one out of every scan.
	cursor int64
}

// heldDelivery is the retry schedule of one held delivery.
type heldDelivery struct {
	next time.Time
	wait time.Duration
}

// NewDeliverer returns a Deliverer.
func NewDeliverer(opts DelivererOptions) *Deliverer {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultDeliveryInterval
	}
	return &Deliverer{opts: opts, kick: make(chan struct{}, 1), backoff: map[int64]heldDelivery{}}
}

// Kick asks for an immediate pass (after a settle). It never blocks.
func (d *Deliverer) Kick() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run abandons deliveries a previous run left half done, then delivers until ctx ends.
func (d *Deliverer) Run(ctx context.Context) {
	if abandoned, err := d.opts.Store.AbandonInterruptedDeliveries(ctx); err != nil {
		d.opts.Logger.Warn("checking interrupted answer deliveries", "err", err)
	} else if abandoned > 0 {
		d.opts.Logger.Warn("answers whose typing was interrupted by a restart were not retried", "count", abandoned)
	}
	ticker := time.NewTicker(d.opts.Interval)
	defer ticker.Stop()
	for {
		d.DeliverPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.kick:
		}
	}
}

// DeliverPending makes one attempt at every pending delivery that is due. It looks at ids
// first and loads a question only once its retry backoff has passed, at most deliveryBatch per
// pass, so a backlog of held answers costs one small query a pass (R1b-C09). The scan pages
// through the backlog pass by pass (R2-C14).
func (d *Deliverer) DeliverPending(ctx context.Context) {
	ids, err := d.opts.Store.PendingDeliveryIDs(ctx, d.cursor, pendingScanLimit)
	if err != nil {
		if ctx.Err() == nil {
			d.opts.Logger.Warn("listing answers to deliver", "err", err)
		}
		return
	}
	if len(ids) < pendingScanLimit {
		d.cursor = 0 // the end of the backlog: the next pass starts over
	} else {
		d.cursor = ids[len(ids)-1]
	}
	attempted := 0
	for _, id := range ids {
		if ctx.Err() != nil || attempted == deliveryBatch {
			return
		}
		if !d.due(id) {
			continue
		}
		record, err := d.opts.Store.GetDelivery(ctx, id)
		if err != nil {
			continue
		}
		attempted++
		d.deliver(ctx, record)
	}
}

func (d *Deliverer) due(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	held, ok := d.backoff[id]
	return !ok || !d.opts.Now().Before(held.next)
}

// deliver makes one claimed attempt at one question.
func (d *Deliverer) deliver(ctx context.Context, record store.QuestionRecord) {
	claimed, err := d.opts.Store.ClaimDelivery(ctx, record.ID, d.opts.Now())
	if err != nil || !claimed {
		return
	}
	line := AnswerLine(fromRecord(record, false))
	attemptCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	err = d.opts.Prompt(attemptCtx, record.SourcePane, line)
	cancel()
	switch {
	case err == nil:
		d.finish(ctx, record.ID, "")
	case errors.Is(err, ErrHold):
		d.hold(ctx, record, err)
	case errors.Is(err, ErrUncertain):
		d.opts.Logger.Warn("typing an answer back may or may not have worked; not retried", "question", FormatID(record.ID), "pane", record.SourcePane)
		d.finish(ctx, record.ID, err.Error())
	default:
		d.opts.Logger.Warn("typing an answer back failed; not retried", "question", FormatID(record.ID), "pane", record.SourcePane, "err", err)
		d.finish(ctx, record.ID, "typing failed: "+err.Error())
	}
}

// hold releases the claim and schedules the next attempt, or gives up after holdGiveUp.
func (d *Deliverer) hold(ctx context.Context, record store.QuestionRecord, reason error) {
	if d.opts.Now().Sub(record.SettledAt) > holdGiveUp {
		d.finish(ctx, record.ID, "gave up: "+reason.Error())
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := d.opts.Store.ReleaseDelivery(cleanupCtx, record.ID); err != nil {
		d.opts.Logger.Warn("releasing a held answer delivery", "question", FormatID(record.ID), "err", err)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	held := d.backoff[record.ID]
	held.wait = min(max(held.wait*2, d.opts.Interval), holdBackoffMax)
	held.next = d.opts.Now().Add(held.wait)
	d.backoff[record.ID] = held
	d.opts.Logger.Debug("holding an answer delivery", "question", FormatID(record.ID), "reason", reason, "retry_in", held.wait)
}

func (d *Deliverer) finish(ctx context.Context, id int64, errText string) {
	d.mu.Lock()
	delete(d.backoff, id)
	d.mu.Unlock()
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := d.opts.Store.FinishDelivery(cleanupCtx, id, d.opts.Now(), errText); err != nil {
		d.opts.Logger.Warn("recording an answer delivery", "question", FormatID(id), "err", err)
		return
	}
	if d.opts.OnDelivered != nil {
		d.opts.OnDelivered(id)
	}
}

// ---- The line ------------------------------------------------------------------------------

// AnswerLine renders the one line typed into the asker: the question's id, the outcome (the
// chosen labels and the human's note, in full), then the title the asker wrote, quoted. Every
// part goes through termtext, because the line lands in a terminal input box where a newline
// would submit early and an escape sequence would be keystrokes. When the full line would be
// longer than MaxLineRunes it is replaced by OverflowLine: a shortened answer could drop the very
// note that outranks the choice.
func AnswerLine(q Question) string {
	line := fmt.Sprintf("[crowsnest answer %s] %s; the question was %s", q.ID, outcome(q), quotedTitle(q))
	if utf8.RuneCountInString(line) > MaxLineRunes {
		return OverflowLine(q)
	}
	return line
}

// OverflowLine is typed instead of an answer too long for one line. It carries no part of the
// answer, so nothing incomplete can be acted on, and says how to read the whole of it.
func OverflowLine(q Question) string {
	return fmt.Sprintf("[crowsnest answer %s] answered, but the answer is too long for one line. Read it in full before acting "+
		"(the get_question tool, or run: crowsnest question %s); the human's note outranks the choice. The question was %s",
		q.ID, q.ID, quotedTitle(q))
}

// outcome is the human's side of the line: the answer and the note, or why there is none.
func outcome(q Question) string {
	switch q.Status {
	case StatusExpired:
		return "expired with no answer (expiry is not consent)"
	case StatusCancelled:
		return "cancelled by the human (not consent)"
	}
	text := termtext.Clean(answerLabels(q))
	if note := termtext.Clean(q.AnswerNote); note != "" && q.Kind != KindManual {
		text += " (note: " + note + ")"
	}
	return text
}

func quotedTitle(q Question) string {
	return termtext.Quote(termtext.Cap(termtext.Clean(q.Title), maxQuotedTitleRunes))
}

// answerLabels turns the stored value into the options' labels ("Ship it", "A, C").
func answerLabels(q Question) string {
	switch q.Kind {
	case KindManual:
		return q.AnswerNote
	case KindAck:
		return "acknowledged"
	case KindNotice:
		return "dismissed"
	}
	return strings.Join(AnswerLabelList(q), ", ")
}

// AnswerLabelList returns the labels of the chosen options, in the order stored (a key with no
// matching option is shown as the key).
func AnswerLabelList(q Question) []string {
	var keys []string
	switch value := q.AnswerValue.(type) {
	case string:
		keys = []string{value}
	case []any:
		for _, item := range value {
			if key, ok := item.(string); ok {
				keys = append(keys, key)
			}
		}
	}
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		labels = append(labels, labelOf(q.Options, key))
	}
	return labels
}

func labelOf(options []Option, key string) string {
	for _, option := range options {
		if option.Key == key {
			return option.Label
		}
	}
	return key
}
