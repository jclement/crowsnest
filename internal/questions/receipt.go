// Answer receipt (DESIGN.md 6.19): an answer's journey after the human settles it, as the Inbox
// shows it. Stored (decided, not typed yet), held (the asker is at a dialog, has a draft, or is
// busy: crowsnest retries), uncertain (handed to the terminal, herdr's answer lost: never retried
// by itself), delivered (typed), received (the asker took it in: its answer line is in its
// conversation, or it collected it on the long-poll). Retrying is the human's explicit choice,
// and only for an answer crowsnest cannot see arrived.

package questions

import (
	"context"
	"errors"
	"strings"
)

// Receipt states.
const (
	ReceiptNone      = ""          // still open, or nothing to deliver
	ReceiptStored    = "stored"    // decided; waiting to be typed (or collected)
	ReceiptHeld      = "held"      // the asker could not take it yet; crowsnest retries
	ReceiptUncertain = "uncertain" // handed to the terminal; whether it was typed is unknown
	ReceiptFailed    = "failed"    // typing failed, or crowsnest gave up
	ReceiptDelivered = "delivered" // typed into the asker
	ReceiptReceived  = "received"  // the asker took it in
)

// ReceiptOf derives a question's receipt state.
func ReceiptOf(q Question) string {
	if q.Status == StatusOpen || q.Delivery == DeliveryNone || q.Delivery == DeliveryUndecided {
		return ReceiptNone
	}
	if q.ReceivedAt != nil {
		return ReceiptReceived
	}
	if q.Delivery == DeliveryPoll {
		if q.DeliveredAt != nil {
			return ReceiptReceived // the asker's own call returned it
		}
		return ReceiptStored
	}
	switch {
	case q.DeliveredAt != nil:
		return ReceiptDelivered
	case strings.HasPrefix(q.DeliveryError, "delivery uncertain"):
		return ReceiptUncertain
	case q.DeliveryError != "":
		return ReceiptFailed
	case q.HeldReason != "":
		return ReceiptHeld
	}
	return ReceiptStored
}

// withReceipt fills a question's receipt fields from the store.
func (s *Service) withReceipt(ctx context.Context, q *Question) {
	numeric, err := ParseID(q.ID)
	if err != nil {
		return
	}
	if receipt, err := s.opts.Store.GetQuestionReceipt(ctx, numeric); err == nil {
		q.ReceivedAt, q.HeldReason = timePtr(receipt.ReceivedAt), receipt.HeldReason
	}
	q.Receipt = ReceiptOf(*q)
}

// ErrNotRetypable refuses a retype of an answer that arrived, or is still on its way.
var ErrNotRetypable = errors.New("only an answer whose delivery was uncertain or failed, and that was not seen to arrive, can be typed again")

// Retype is the human's explicit "type it again" for an uncertain or failed delivery: the
// answer goes back in the delivery queue once. Never automatic: a duplicate answer is worse
// than a late one, so the Inbox first offers "check whether it arrived".
func (s *Service) Retype(ctx context.Context, id string) (Question, error) {
	question, err := s.Get(ctx, id)
	if err != nil {
		return Question{}, err
	}
	if question.Receipt != ReceiptUncertain && question.Receipt != ReceiptFailed {
		return Question{}, ErrNotRetypable
	}
	numeric, _ := ParseID(id)
	if err := s.opts.Store.ResetDelivery(ctx, numeric); err != nil {
		return Question{}, err
	}
	s.publish(question, "delivered")
	if s.opts.OnSettled != nil {
		s.opts.OnSettled(question) // the Deliverer's kick
	}
	return s.Get(ctx, id)
}

// MarkReceived records that question id's asker took the answer in (the jobs service saw it in
// the transcript) and tells the Inbox.
func (s *Service) MarkReceived(ctx context.Context, numeric int64) {
	question, err := s.Get(ctx, FormatID(numeric))
	if err != nil {
		return
	}
	s.publish(question, "received")
}
