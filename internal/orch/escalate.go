// Lead-first routing (DESIGN.md 7.4, 21.2): the pure half of who hears about a project's jobs.
// A project's lead owns its jobs: it is told when one finishes, gets stuck or asks. The
// orchestrator hears about a project job only as the outcome of work it handed over, or as an
// escalation the lead could not resolve: the project has no lead able to take pokes, a job has
// been stuck or waiting on the human for longer than EscalateAfter, or the lead itself failed or
// stopped at a terminal prompt. This file holds the escalation rules and the sentences that make
// the division legible to the agents and the web app; cmd/crowsnest/pokes.go does the routing.

package orch

import (
	"fmt"
	"time"

	"github.com/jclement/crowsnest/internal/termtext"
)

// EscalateAfter is how long a project job may stay stuck or waiting on the human, with no
// change, before the orchestrator is told its lead has not resolved it.
const EscalateAfter = 30 * time.Minute

// Ownership sentences, one per standing-agent kind: the web app shows them on the Now rows and
// the agents' instructions say the same in more words.
const (
	OwnershipOrchestrator = "Routes and summarises across projects; a project's lead owns its jobs, so the orchestrator hears about them only as outcomes of work it handed over and as escalations."
	OwnershipLead         = "Owns every job in its project: told when one finishes, gets stuck or asks; the orchestrator hears only what the lead could not resolve."
)

// OwnershipSentence is the one-line division of labour for a standing agent: kind "lead" for a
// project's lead, anything else for the orchestrator.
func OwnershipSentence(kind string) string {
	if kind == "lead" {
		return OwnershipLead
	}
	return OwnershipOrchestrator
}

// ProjectJob is a live job of a project, as the escalation rule needs it.
type ProjectJob struct {
	ID, Title string
	Project   string // the project's name
	NeedsYou  bool   // waiting on the human
	Stuck     bool
	Cause     string    // the stuck cause in words
	Since     time.Time // when it last changed (its last activity); zero: unknown, never escalated
}

// Escalations are the reasons due for project jobs stuck or waiting on the human for at least
// EscalateAfter (pure). Each is keyed by job and cause, so the poker's delivered set sends it
// once; a job that changes cause is a new escalation.
func Escalations(now time.Time, jobs []ProjectJob) []Reason {
	var out []Reason
	for _, job := range jobs {
		if job.Since.IsZero() || now.Sub(job.Since) < EscalateAfter {
			continue
		}
		var cause, what string
		switch {
		case job.Stuck:
			cause, what = "stuck:"+job.Cause, "has been stuck"
			if job.Cause != "" {
				what += " (" + job.Cause + ")"
			}
		case job.NeedsYou:
			cause, what = "needs_you", "has been waiting on the human"
		default:
			continue
		}
		text := fmt.Sprintf("escalation: project %s: job %s %s for %s and its lead has not resolved it: %s",
			termtext.Quote(job.Project), job.ID, what, roundHeld(now.Sub(job.Since)), termtext.Quote(job.Title))
		out = append(out, Reason{Key: "escalate:" + job.ID + ":" + cause, Source: SourceJob, Text: text, At: now})
	}
	return out
}

// LeadDownReason is the escalation for a lead that failed or stopped at a terminal prompt.
// since makes the key: a lead that recovers and fails again is told again.
func LeadDownReason(project string, state State, since time.Time) Reason {
	what := "failed"
	if state == StateNeedsTerminal {
		what = "stopped at a prompt only its terminal can answer"
	}
	return Reason{
		Key:    fmt.Sprintf("lead-down:%s:%s:%d", project, state, since.Unix()),
		Source: SourceJob,
		Text:   fmt.Sprintf("the lead of %s has %s; its project's jobs are not being looked after", termtext.Quote(project), what),
		At:     since,
	}
}

// LeadCanTakePokes reports whether a lead in this state can take pokes (now or once it settles):
// not off, stopped, failed, at a terminal prompt or disabled.
func LeadCanTakePokes(state State) bool {
	switch state {
	case StateFailed, StateNeedsTerminal, StateStopped, StateDisabled, "off", "":
		return false
	}
	return true
}
