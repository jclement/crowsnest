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
	"unicode"

	"github.com/jclement/crowsnest/internal/store"
)

// ErrHold is what a Prompter returns when it typed nothing and a later attempt may succeed: the
// agent is at a dialog, has a draft, or is not there right now.
var ErrHold = errors.New("nothing typed; try again later")

// Prompter types text into the agent in paneID. It returns nil when the text was typed (whether
// or not a turn visibly started), an error wrapping ErrHold when nothing was typed, and any other
// error when the outcome is unknown.
type Prompter func(ctx context.Context, paneID, text string) error

const (
	// DefaultDeliveryInterval is how often pending deliveries are looked at without a kick.
	DefaultDeliveryInterval = 3 * time.Second
	// MaxLineRunes caps the typed line; the agent can read the rest with crowsnest's API (M7).
	MaxLineRunes = 600
	// holdBackoffMax caps the wait between attempts on a held delivery.
	holdBackoffMax = time.Minute
	// holdGiveUp is how long a held delivery keeps being retried.
	holdGiveUp = 7 * 24 * time.Hour
	// deliveryTimeout bounds one attempt (agent.prompt waits up to 30 s for the turn).
	deliveryTimeout = 45 * time.Second
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

// DeliverPending makes one attempt at every pending delivery that is due.
func (d *Deliverer) DeliverPending(ctx context.Context) {
	pending, err := d.opts.Store.PendingDeliveries(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.opts.Logger.Warn("listing answers to deliver", "err", err)
		}
		return
	}
	for _, record := range pending {
		if ctx.Err() != nil {
			return
		}
		if d.due(record.ID) {
			d.deliver(ctx, record)
		}
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
	if err := d.opts.Store.ReleaseDelivery(ctx, record.ID); err != nil {
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
	if err := d.opts.Store.FinishDelivery(context.WithoutCancel(ctx), id, d.opts.Now(), errText); err != nil {
		d.opts.Logger.Warn("recording an answer delivery", "question", FormatID(id), "err", err)
		return
	}
	if d.opts.OnDelivered != nil {
		d.opts.OnDelivered(id)
	}
}

// ---- The line ------------------------------------------------------------------------------

// AnswerLine renders the one line typed into the asker: the question's id and title, then the
// outcome. Every part is sanitised (no control characters, no line breaks) and the line capped,
// because it lands in a terminal input box where a newline would submit early.
func AnswerLine(q Question) string {
	var outcome string
	switch q.Status {
	case StatusExpired:
		outcome = "expired with no answer (expiry is not consent)"
	case StatusCancelled:
		outcome = "cancelled by the human"
	default:
		outcome = answerLabels(q)
	}
	line := fmt.Sprintf("[crowsnest answer %s] %s → %s", q.ID, clean(q.Title), outcome)
	if note := clean(q.AnswerNote); note != "" && q.Kind != KindManual {
		line += " (note: " + note + ")"
	}
	return capRunes(line, MaxLineRunes)
}

// answerLabels turns the stored value into the options' labels ("Ship it", "A, C").
func answerLabels(q Question) string {
	switch q.Kind {
	case KindManual:
		return clean(q.AnswerNote)
	case KindAck:
		return "acknowledged"
	case KindNotice:
		return "dismissed"
	}
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
		labels = append(labels, clean(labelOf(q.Options, key)))
	}
	return strings.Join(labels, ", ")
}

func labelOf(options []Option, key string) string {
	for _, option := range options {
		if option.Key == key {
			return option.Label
		}
	}
	return key
}

// clean flattens text to one line of printable characters with single spaces.
func clean(text string) string {
	var out strings.Builder
	space := false
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == 0x1b {
			space = out.Len() > 0
			continue
		}
		if space {
			out.WriteByte(' ')
			space = false
		}
		out.WriteRune(r)
	}
	return out.String()
}

func capRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
