// Package questions is crowsnest's inbox of questions from agents (DESIGN.md section 8.1): the
// Meat Prompt shaped record plus `multi`, its validation, the lifecycle (open, then answered,
// expired or cancelled, exactly once), the long-poll a blocking asker waits on, the expiry
// sweeper, and the delivery of an answer back into the asker's pane when nobody was waiting
// (section 6.3, "Answers come back as messages").
//
// The agent side reaches it over the unix socket (internal/agentapi), the browser over the HTTP
// API (internal/httpd). Both call the same Service, so there is one implementation of every rule.
package questions

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jclement/crowsnest/internal/attach"
)

// Kind is what sort of answer a question wants.
type Kind string

// Question kinds.
const (
	KindConfirm Kind = "confirm" // yes or no (default options yes/no), recommendation required
	KindChoice  Kind = "choice"  // one of the options (or several with multi), recommendation required
	KindAck     Kind = "ack"     // "seen it": one button
	KindManual  Kind = "manual"  // free text, in the note
	KindNotice  Kind = "notice"  // information only; nothing is sent back
)

// Status is where a question is in its life. Only open questions can change.
type Status string

// Statuses.
const (
	StatusOpen      Status = "open"
	StatusAnswered  Status = "answered"
	StatusExpired   Status = "expired" // never an answer: expiry is not consent
	StatusCancelled Status = "cancelled"
)

// Urgency orders the inbox and decides whether --notify-url hears about it.
type Urgency string

// Urgencies.
const (
	UrgencyLow      Urgency = "low"
	UrgencyNormal   Urgency = "normal"
	UrgencyBlocking Urgency = "blocking"
)

// Delivery says how a settled question's outcome reaches the asker.
type Delivery string

// Deliveries.
const (
	DeliveryUndecided Delivery = ""     // still open
	DeliveryType      Delivery = "type" // typed into the asker's pane as a message
	DeliveryPoll      Delivery = "poll" // the asker was waiting on the long-poll and got it there
	DeliveryNone      Delivery = "none" // nothing to send (a notice, cancelled by the asker, no pane)
)

// Option is one choice of a confirm or choice question.
type Option struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// File is one file shown as a diff: the original and the proposed content.
type File struct {
	Filename string `json:"filename"`
	Original string `json:"original"`
	Final    string `json:"final"`
}

// AskInput is a question as an agent posts it (POST /api/v1/questions on the socket, the
// `ask` MCP tool, `crowsnest ask --file`).
type AskInput struct {
	Title          string            `json:"title"`
	BodyMD         string            `json:"body_md,omitempty"`
	Kind           Kind              `json:"kind,omitempty"`
	Multi          bool              `json:"multi,omitempty"`
	Options        []Option          `json:"options,omitempty"`
	Recommend      string            `json:"recommend,omitempty"`
	Context        map[string]string `json:"context,omitempty"`
	Urgency        Urgency           `json:"urgency,omitempty"`
	ExpiresInHours float64           `json:"expires_in_hours,omitempty"`
	Files          []File            `json:"files,omitempty"`
	// Wait says the asker will block on the long-poll for the answer (`ask --wait`, the MCP
	// tool's wait=true). It is recorded so the answer is not also typed into its pane.
	Wait bool `json:"wait,omitempty"`
	// JobID names the job the question is about (the MCP tool's job_id). The agent socket
	// checks it against the caller's token: a job agent may name only its own job, and the
	// orchestrator (whose token names no job) only a job it holds (R2-C06).
	JobID string `json:"job_id,omitempty"`
	// Attachments are files the agent attaches to the question (absolute paths, inside its
	// working directory or job directory: DESIGN.md 8.6). The agent socket imports them before
	// asking; the questions service itself never reads a path.
	Attachments []string `json:"attachments,omitempty"`
}

// Caller identifies who asked: the herdr pane the answer is typed back into, and the job the
// asker holds (M7). The agent-side CLI fills both from its environment.
type Caller struct {
	PaneID string
	JobID  string
}

// AnswerInput is the human's answer (POST /api/v1/questions/{id}/answer). Value is a key, or an
// array of keys for a multi-select; the note outranks the choice.
type AnswerInput struct {
	Value json.RawMessage `json:"answer_value,omitempty"`
	Note  string          `json:"answer_note,omitempty"`
	// Attachments are uploads (a_…) sent with the answer. The browser API copies them into the
	// asker's inbox before answering; the line typed back names them (DESIGN.md 8.6).
	Attachments []string `json:"attachments,omitempty"`
}

// Question is the full record as both APIs return it.
type Question struct {
	ID        string            `json:"id"` // q_<n>
	Title     string            `json:"title"`
	BodyMD    string            `json:"body_md"`
	Kind      Kind              `json:"kind"`
	Multi     bool              `json:"multi"`
	Options   []Option          `json:"options"`
	Recommend string            `json:"recommend,omitempty"`
	Context   map[string]string `json:"context"`
	Urgency   Urgency           `json:"urgency"`
	// Files is omitted from lists (they can be large); FileCount says how many there are.
	Files     []File `json:"files,omitempty"`
	FileCount int    `json:"file_count"`
	// Attachments are the asker's files; AnswerAttachments the human's, sent with the answer
	// (with the path of the asker's copy). Filled by Get, not by lists.
	Attachments       []attach.Attachment `json:"attachments,omitempty"`
	AnswerAttachments []attach.Attachment `json:"answer_attachments,omitempty"`

	Status      Status `json:"status"`
	AnswerValue any    `json:"answer_value"` // null until answered; a key or an array of keys
	AnswerNote  string `json:"answer_note"`

	SourcePane string `json:"source_pane,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	Wait       bool   `json:"wait"`

	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	SettledAt *time.Time `json:"settled_at"`

	Delivery      Delivery   `json:"delivery"`
	DeliveredAt   *time.Time `json:"delivered_at"`
	DeliveryError string     `json:"delivery_error,omitempty"`
}

// Event is the data of a `question` hub event: something about question ID changed. The title,
// kind and urgency let the browser raise a notification without a fetch.
type Event struct {
	ID      string  `json:"id"`
	Status  Status  `json:"status"`
	Title   string  `json:"title"`
	Kind    Kind    `json:"kind"`
	Urgency Urgency `json:"urgency"`
	// Change is "asked", "answered", "expired", "cancelled" or "delivered".
	Change string `json:"change"`
}

// Summary is the Inbox badge: open questions, and how many of them block an agent.
type Summary struct {
	Open     int `json:"open"`
	Blocking int `json:"blocking"`
}

// FormatID renders a numeric id as the public q_<n>.
func FormatID(id int64) string { return fmt.Sprintf("q_%d", id) }

// ParseID accepts q_<n> (or a bare number, which is what Meat Prompt clients send back) and
// returns the numeric id.
func ParseID(text string) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(text, "q_%d", &id); err == nil && id > 0 && FormatID(id) == text {
		return id, nil
	}
	if _, err := fmt.Sscanf(text, "%d", &id); err == nil && id > 0 && fmt.Sprint(id) == text {
		return id, nil
	}
	return 0, fmt.Errorf("%q is not a question id (expected q_<number>)", text)
}
