package questions

import (
	"testing"
	"time"
)

func TestReceiptOf(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		q    Question
		want string
	}{
		{"open", Question{Status: StatusOpen}, ReceiptNone},
		{"a notice", Question{Status: StatusAnswered, Delivery: DeliveryNone}, ReceiptNone},
		{"decided, not typed", Question{Status: StatusAnswered, Delivery: DeliveryType}, ReceiptStored},
		{"held at a dialog", Question{Status: StatusAnswered, Delivery: DeliveryType, HeldReason: "a dialog is open"}, ReceiptHeld},
		{"uncertain", Question{Status: StatusAnswered, Delivery: DeliveryType, DeliveryError: ErrUncertain.Error()}, ReceiptUncertain},
		{"failed", Question{Status: StatusAnswered, Delivery: DeliveryType, DeliveryError: "typing failed: x"}, ReceiptFailed},
		{"typed", Question{Status: StatusAnswered, Delivery: DeliveryType, DeliveredAt: &now}, ReceiptDelivered},
		{"seen in the conversation", Question{Status: StatusAnswered, Delivery: DeliveryType, DeliveredAt: &now, ReceivedAt: &now}, ReceiptReceived},
		{"uncertain, then seen", Question{Status: StatusAnswered, Delivery: DeliveryType, DeliveryError: ErrUncertain.Error(), ReceivedAt: &now}, ReceiptReceived},
		{"poll, waiting", Question{Status: StatusAnswered, Delivery: DeliveryPoll}, ReceiptStored},
		{"poll, collected", Question{Status: StatusAnswered, Delivery: DeliveryPoll, DeliveredAt: &now}, ReceiptReceived},
	}
	for _, tc := range cases {
		if got := ReceiptOf(tc.q); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
