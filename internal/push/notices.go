// The notice: what gets pushed, and how each event becomes one.
//
// A notice is a title, one clean line, a tag and a deep link. Nothing secret, nothing long: the
// text passes through termtext.Clean and internal/redact and is capped, exactly as --notify-url's
// line is (internal/questions/notify.go); a question's body, options and files never travel, only
// its title. The tag lets the device replace an older notification about the same thing (the
// same question, the same dialog, the same job) instead of stacking them.
//
// The rate limits are --notify-url's too: per source (the asking pane, the job, the dialog's pane)
// a burst of 3 then one a minute, and across everything 10 then one a minute, so a looping or
// hijacked agent cannot turn the phone into its megaphone.

package push

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/jclement/crowsnest/internal/ratelimit"
	"github.com/jclement/crowsnest/internal/redact"
	"github.com/jclement/crowsnest/internal/termtext"
)

// Kind is a family of notice, each switchable in Settings.
type Kind string

// The kinds of notice.
const (
	KindQuestion     Kind = "question"     // a new open question, blocking or normal (low is never pushed)
	KindDialog       Kind = "dialog"       // an agent waits at a terminal dialog
	KindJob          Kind = "job"          // a job went stuck, failed, or needs a fix only Jeff can make
	KindDone         Kind = "done"         // a job finished
	KindOrchestrator Kind = "orchestrator" // the orchestrator needs the terminal, or has stopped
	KindApproval     Kind = "approval"     // a tool call waits for approval (DESIGN.md 8.7)
)

// AllKinds in the order the Settings sheet lists them.
var AllKinds = []Kind{KindApproval, KindQuestion, KindDialog, KindJob, KindDone, KindOrchestrator}

// Urgency is how soon the push service should wake the device (RFC 8030 section 5.3).
type Urgency string

// The urgencies crowsnest uses.
const (
	UrgencyHigh   Urgency = "high"
	UrgencyNormal Urgency = "normal"
	UrgencyLow    Urgency = "low"
)

const (
	maxTitleRunes = 60
	maxBodyRunes  = 160
	// The rate limits of internal/questions/notify.go (notifyBurst, notifyRefill, textBurst,
	// textRefill), repeated here because those are unexported there; keep them equal.
	perSourceBurst  = 3
	perSourceRefill = time.Minute
	overallBurst    = 10
	overallRefill   = time.Minute
)

// Notice is one thing to tell Jeff.
type Notice struct {
	Kind    Kind    `json:"kind"`
	Title   string  `json:"title"`
	Body    string  `json:"body"`
	Tag     string  `json:"tag"` // the device replaces an earlier notification with the same tag
	URL     string  `json:"url"` // a path in the app, opened when the notification is tapped
	Urgency Urgency `json:"urgency"`
	// Source is the rate-limit key (the asking pane, the job, the pane at a dialog); not sent.
	Source string `json:"-"`
	// Actions are the notification's buttons (DESIGN.md 8.7): only a low-risk approval and a
	// confirm or choice question carry them. ActionItem names the item they answer
	// ("approval:ap_3"); the watcher mints a single-use token for it before delivery, and a
	// notice whose token could not be minted goes without buttons.
	Actions     []NoticeAction `json:"actions,omitempty"`
	ActionItem  string         `json:"-"`
	ActionToken string         `json:"-"`
}

// NoticeAction is one notification button: the action name sent back, and its label.
type NoticeAction struct {
	Action string `json:"action"`
	Title  string `json:"title"`
}

// maxActions is how many buttons a notification gets (Chrome shows two).
const maxActions = 2

// actionName is what an action may be called: an approval's allow or deny, or a question's
// option key (questions/validate.go allows the same characters).
var actionName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

// Clean returns the notice with its text sanitised, redacted and capped, and its URL forced to a
// path on this origin (the service worker opens it; it must never leave the app).
func (n Notice) Clean() Notice {
	n.Title = termtext.Cap(redact.String(termtext.Clean(n.Title)), maxTitleRunes)
	n.Body = termtext.Cap(redact.String(termtext.Clean(n.Body)), maxBodyRunes)
	n.Tag = termtext.Cap(termtext.Clean(n.Tag), 64)
	if !strings.HasPrefix(n.URL, "/") || strings.HasPrefix(n.URL, "//") || strings.ContainsFunc(n.URL, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f }) {
		n.URL = "/" // a control character (a tab, R5-S13) can be dropped by a URL parser into "//"
	}
	if n.Urgency == "" {
		n.Urgency = UrgencyNormal
	}
	var actions []NoticeAction
	for _, action := range n.Actions {
		if len(actions) == maxActions || !actionName.MatchString(action.Action) {
			continue
		}
		actions = append(actions, NoticeAction{Action: action.Action, Title: termtext.Cap(redact.String(termtext.Clean(action.Title)), 24)})
	}
	n.Actions = actions
	if len(n.Actions) == 0 || n.ActionToken == "" {
		n.Actions, n.ActionToken = nil, ""
	}
	return n
}

// ttl is how long the push service keeps an undelivered notice: the things that need Jeff stay
// relevant until he acts, but a day-old notice on a phone that was off is noise.
func (n Notice) ttl() int {
	switch n.Urgency {
	case UrgencyHigh:
		return int((24 * time.Hour).Seconds())
	case UrgencyLow:
		return int((4 * time.Hour).Seconds())
	default:
		return int((12 * time.Hour).Seconds())
	}
}

func (n Notice) webpushUrgency() webpush.Urgency {
	switch n.Urgency {
	case UrgencyHigh:
		return webpush.UrgencyHigh
	case UrgencyLow:
		return webpush.UrgencyLow
	default:
		return webpush.UrgencyNormal
	}
}

// ---- Building notices from events ------------------------------------------------

// ApprovalNotice is the notice for a tool call waiting for approval. Only a low-risk one carries
// Allow and Deny buttons: an elevated one is answered in the app, a dangerous one only with the
// card's second confirmation (DESIGN.md 8.7). The body is the call's one-line summary, cleaned
// and capped like every notice.
func ApprovalNotice(id, risk, agentName, summary, paneID string) Notice {
	who := agentName
	if who == "" {
		who = "An agent"
	}
	notice := Notice{
		Kind: KindApproval, Body: who + " wants to run " + summary, Tag: "approval:" + id,
		URL: "/inbox?q=" + url.QueryEscape("approval:"+id), Urgency: UrgencyHigh, Source: "pane:" + paneID,
	}
	switch risk {
	case "low":
		notice.Title = "Allow this tool call?"
		notice.Actions = []NoticeAction{{Action: "allow", Title: "Allow"}, {Action: "deny", Title: "Deny"}}
		notice.ActionItem = "approval:" + id
	case "elevated":
		notice.Title = "A tool call needs your approval"
	default:
		notice.Title = "A dangerous tool call needs you"
	}
	return notice
}

// QuestionActions are a confirm or choice question's first two options as notification buttons
// (none for a multi-select or any other kind).
func QuestionActions(kind string, multi bool, options []NoticeAction) []NoticeAction {
	if multi || (kind != "confirm" && kind != "choice") || len(options) == 0 {
		return nil
	}
	if len(options) > maxActions {
		options = options[:maxActions]
	}
	return options
}

// QuestionNotice is the notice for a newly asked question; ok is false for one that is not
// pushed (low urgency).
func QuestionNotice(id, title, kind, urgency, sourcePane string) (Notice, bool) {
	notice := Notice{Kind: KindQuestion, Body: title, Tag: id, URL: "/inbox?q=" + url.QueryEscape(id), Source: "pane:" + sourcePane}
	if sourcePane == "" {
		notice.Source = "unidentified"
	}
	switch urgency {
	case "blocking":
		notice.Title, notice.Urgency = "An agent is blocked on you", UrgencyHigh
	case "normal", "":
		notice.Title, notice.Urgency = "An agent asks", UrgencyNormal
		if kind == "notice" {
			notice.Title = "Notice from an agent"
		}
	default:
		return Notice{}, false
	}
	return notice, true
}

// DialogNotice is the notice for an agent waiting at a terminal dialog.
func DialogNotice(paneID, agentName, workspace string) Notice {
	who := agentName
	if who == "" {
		who = "an agent"
	}
	body := fmt.Sprintf("%s is waiting at a terminal dialog", who)
	if workspace != "" {
		body += " in " + workspace
	}
	return Notice{
		Kind: KindDialog, Title: "A dialog needs an answer", Body: body, Tag: "dialog:" + paneID,
		URL: "/inbox?q=" + url.QueryEscape("dialog:"+paneID), Urgency: UrgencyHigh, Source: "pane:" + paneID,
	}
}

// JobNotice is the notice for a job event: stuck, failed, needing a fix, or done. reason is the
// dedupe key the watcher keeps per job ("failed", "stuck:limit", "done", …); ok is false when the
// event is not one that is pushed.
func JobNotice(id, title, state, cause string, stuck bool) (notice Notice, reason string, ok bool) {
	notice = Notice{Kind: KindJob, Tag: "job:" + id, URL: "/jobs?job=" + url.QueryEscape(id), Urgency: UrgencyHigh, Source: "job:" + id}
	line := id + ": " + title
	switch {
	case state == "failed":
		notice.Title, notice.Body, reason = "A job failed", line, "failed"
	case state == "done":
		notice.Kind, notice.Title, notice.Body, notice.Urgency, reason = KindDone, "A job is done", line, UrgencyNormal, "done"
	case state == "needs_you" && (cause == "auth" || cause == "config" || cause == "launch"):
		notice.Title, notice.Body, reason = "A job needs you", line+" ("+causeWords[cause]+")", "needs:"+cause
	case stuck && cause != "" && cause != "exited":
		words := causeWords[cause]
		if words == "" {
			words = cause
		}
		notice.Title, notice.Body, reason = "A job is stuck", line+" ("+words+")", "stuck:"+cause
	default:
		return Notice{}, "", false
	}
	return notice, reason, true
}

// causeWords say a cause the way the job card does.
var causeWords = map[string]string{
	"unresponsive": "stopped three times without saying why",
	"permissions":  "permission denials",
	"limit":        "usage limit; retrying",
	"server_error": "API errors",
	"silent":       "silent while working",
	"idle":         "idle with nothing declared",
	"auth":         "Claude needs a login or billing fix",
	"config":       "Claude refused the request",
	"launch":       "the agent could not start",
}

// OrchestratorNotice is the notice for the orchestrator's state; ok is false for a state that is
// not pushed. Only a start-up prompt (needs_terminal) and failed are: a dialog while it runs is a
// terminal dialog like any other agent's, pushed as KindDialog.
func OrchestratorNotice(state, detail string) (Notice, bool) {
	notice := Notice{Kind: KindOrchestrator, Tag: "orchestrator", URL: "/chat", Urgency: UrgencyHigh, Source: "orchestrator"}
	switch state {
	case "needs_terminal":
		notice.Title, notice.Body = "The orchestrator needs the terminal", "It stopped at a start-up prompt (folder trust or login)"
	case "failed":
		notice.Title, notice.Body = "The orchestrator stopped", "It could not start or kept exiting"
		if detail != "" {
			notice.Body = detail
		}
	default:
		return Notice{}, false
	}
	return notice, true
}

// TestNotice is what "Send a test notification" pushes.
func TestNotice() Notice {
	return Notice{Kind: "test", Title: "crowsnest", Body: "Notifications work on this device.", Tag: "test", URL: "/", Urgency: UrgencyNormal, Source: "test"}
}

// ---- Rate limits ------------------------------------------------------------------

// gate applies the per-source and overall limits.
type gate struct {
	perSource *ratelimit.Limiter
	overall   *ratelimit.Limiter
}

func newGate(now func() time.Time) *gate {
	return &gate{perSource: ratelimit.New(perSourceBurst, perSourceRefill, now), overall: ratelimit.New(overallBurst, overallRefill, now)}
}

// allow reports whether a notice from source may go now, and if not, which limit said no.
func (g *gate) allow(source string) (bool, string) {
	if ok, _ := g.perSource.Allow(source); !ok {
		return false, "too many notices from one source"
	}
	if ok, _ := g.overall.Allow("all"); !ok {
		return false, "too many notices at once"
	}
	return true, ""
}
