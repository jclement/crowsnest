// Decision-grade notices (R4 Fable 3.4, Codex P2): a lock-screen notification should let Jeff
// decide even when he must open the app to tap. So a question, an approval or a dialog says which
// project it belongs to (title), and which job, what is asked and the recommendation (body), and a
// tap opens its decision packet (`/decide?item=…`) with the answer bar under his thumb.
//
// Buttons, where the platform shows them (Chrome on desktop and Android; never iOS), are kept
// narrow on purpose, under the security review's rules (R5-S2, R5-S7):
//   - a low-risk approval keeps Allow and Deny, but only when the whole call fits the notice: a
//     summary that had to be cut never carries Allow (what is past the cut is unseen) and gets
//     Snooze instead; an elevated or dangerous one gets no button and no token at all;
//   - a question shaped like a gate (crowsnest's own land and delegation questions, anything whose
//     options allow, approve, merge or push) gets no button and no token: a deliberate tap in the
//     app answers it;
//   - a routine question (one choice, no files, a short body) gets its recommended option plus
//     Snooze; any other question, and a dialog, gets Snooze alone, which hides the item from the
//     queue for ten minutes on every device and decides nothing.

package push

import (
	"context"
	"net/url"
	"strings"

	"github.com/jclement/crowsnest/internal/questions"
)

// Where says where an item belongs: its project's name ("" for unfiled work) and its job.
type Where struct {
	Project  string
	JobID    string
	JobTitle string
	// JobStopped: the job has stopped on its questions now (attention.JobStoppedOnQuestions), so a
	// question of it is blocking, as the queue says (R9-C12).
	JobStopped bool
}

// Describer finds where an item belongs from its job id or, failing that, the pane that asked.
type Describer func(ctx context.Context, jobID, paneID string) Where

// ActionSnooze is the snooze button's action name; /push/action snoozes the item for ten minutes.
const ActionSnooze = "snooze"

// snoozeButton is the Snooze button.
var snoozeButton = NoticeAction{Action: ActionSnooze, Title: "Snooze 10 min"}

// routineBodyChars mirrors the web app's isRoutine (lib/decision.ts): a longer body is evidence
// to read, not a preview.
const routineBodyChars = 400

// GateShaped is questions.GateShaped, the one rule for what a notification may never answer
// (R5-S1, R5-S2); the push action endpoint applies the same rule and is the authority.
func GateShaped(context map[string]string, optionKeys []string) bool {
	return questions.GateShaped(context, optionKeys)
}

// DecideURL is the decision packet's path for an attention item id.
func DecideURL(item string) string { return "/decide?item=" + url.QueryEscape(item) }

// place is the project's name, or "Unfiled".
func (w Where) place() string {
	if strings.TrimSpace(w.Project) == "" {
		return "Unfiled"
	}
	return w.Project
}

// jobLead is "j_15 Homepage hero: " (or "j_15: ", or "") to start a body with.
func (w Where) jobLead() string {
	switch {
	case w.JobID != "" && w.JobTitle != "":
		return w.JobID + " " + w.JobTitle + ": "
	case w.JobID != "":
		return w.JobID + ": "
	default:
		return ""
	}
}

// QuestionDetails is what a question notice needs beyond its id and title.
type QuestionDetails struct {
	Kind      string
	Multi     bool
	Urgency   string // the queue's label for it (attention.QuestionLabel): "blocking" or "can_wait"
	Recommend string // the recommended option's key
	Options   []NoticeAction
	Context   map[string]string
	HasFiles  bool
	BodyChars int
}

// DecorateQuestion makes a question notice decision-grade: project in the title, job, question and
// recommendation in the body, the packet as the link, and the narrow buttons described above.
func DecorateQuestion(notice Notice, id, title string, details QuestionDetails, where Where) Notice {
	what := "question"
	if details.Urgency == "blocking" {
		what = "blocking question"
	}
	if details.Kind == "notice" {
		what = "notice"
	}
	notice.Title = where.place() + ": " + what
	body := where.jobLead() + title
	recommended := ""
	keys := make([]string, 0, 2*len(details.Options)+1)
	keys = append(keys, title)
	for _, option := range details.Options {
		keys = append(keys, option.Action, option.Title)
		if option.Action == details.Recommend {
			recommended = option.Title
		}
	}
	if recommended != "" {
		body += " · Recommended: " + recommended
	}
	notice.Body = body
	item := "question:" + id
	notice.URL = DecideURL(item)
	notice.ActionItem = item
	routine := (details.Kind == "confirm" || details.Kind == "choice") && !details.Multi && !details.HasFiles && details.BodyChars <= routineBodyChars
	switch {
	case GateShaped(details.Context, keys):
		// A gate gets no token at all (R5-S2): it is answered with a deliberate tap in the app.
		notice.Actions, notice.ActionItem = nil, ""
	case routine && recommended != "":
		notice.Actions = []NoticeAction{{Action: details.Recommend, Title: recommended}, snoozeButton}
	default:
		notice.Actions = []NoticeAction{snoozeButton}
	}
	return notice
}

// DecorateApproval makes an approval notice decision-grade. truncated says the call's summary
// was cut: then a low-risk approval loses Allow and Deny too, and the notice opens the packet.
func DecorateApproval(notice Notice, id, risk, agentName, summary string, truncated bool, where Where) Notice {
	what := "approval"
	switch risk {
	case "dangerous":
		what = "DANGEROUS approval"
	case "elevated":
		what = "approval (elevated)"
	}
	notice.Title = where.place() + ": " + what
	who := agentName
	if who == "" {
		who = "an agent"
	}
	notice.Body = where.jobLead() + who + " wants " + summary
	item := "approval:" + id
	notice.URL = DecideURL(item)
	notice.ActionItem = item
	fits := !truncated && len([]rune(notice.Body)) <= maxBodyRunes
	switch {
	case risk != "low":
		// No token of any kind for an elevated or dangerous call (DESIGN 8.7): not even Snooze,
		// which would let whoever sees the payload hide it from the queue.
		notice.Actions, notice.ActionItem = nil, ""
	case fits:
		notice.Actions = []NoticeAction{{Action: "allow", Title: "Allow"}, {Action: "deny", Title: "Deny"}}
	default:
		notice.Actions = []NoticeAction{snoozeButton}
	}
	return notice
}

// DecorateDialog makes a dialog notice say where it is and open the packet.
func DecorateDialog(notice Notice, paneID string, where Where) Notice {
	notice.Title = where.place() + ": terminal dialog"
	if lead := where.jobLead(); lead != "" {
		notice.Body = lead + notice.Body
	}
	item := "dialog:" + paneID
	notice.URL = DecideURL(item)
	notice.ActionItem = item
	notice.Actions = []NoticeAction{snoozeButton}
	return notice
}
