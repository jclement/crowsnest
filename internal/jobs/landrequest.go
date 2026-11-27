// An agent's land request (DESIGN.md 6.11, R5-S1, R5-C01, R5-C09). Only the human lands; an agent
// (a job's own agent, its parent, its project's lead, the orchestrator) may only ask, and the ask
// becomes a question that the human's plain Allow approves. The request and its question are bound
// to each other so that no other answer can run it:
//
//  1. Reserve: under the jobs lock, a job with no request takes one, with the action and a fresh
//     random token. A second request is refused while one is reserved, so two agents can never
//     overwrite each other's request.
//  2. Ask: the question carries land_for, land_action and land_token in its context. Agents may
//     not set those keys (questions.CheckAgentContext), so only crowsnest makes such a question.
//  3. Link: the question's id is stored. It is a convenience for recovery, not what binds: the
//     token is, so an answer that arrives before the link still works.
//  4. Settle: an answer runs the land only if, under the jobs lock, the job still holds a request
//     for the same action with the same token; the request is consumed in that same mutation, so
//     the land runs at most once. A stale, forged or unrelated answer matches nothing.
//
// The settle callback is in memory; ReconcileLandRequests repeats steps 3 and 4 from the database
// at start and on every supervisor tick, so a crash between any two steps is repaired: an answer
// that settled while crowsnest was down is applied, a missing link is found by token, and a
// request whose question was never asked is dropped after landRequestGrace.

package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// landRequestGrace is how long a reserved request may go without a question before it counts as
// never asked: RequestLand asks a moment after reserving.
const landRequestGrace = time.Minute

// errStaleLandAnswer stops an answer that does not match the request the job holds now.
var errStaleLandAnswer = errors.New("the answer is not for the land request this job holds")

// RequestLand turns an agent's land request into a question for the human. Only their plain
// Allow runs it (LandRequestSettled); the agent gets the outcome typed back like any answer.
func (s *Service) RequestLand(ctx context.Context, id, action string, caller questions.Caller, reason string) (questions.Question, error) {
	if !ValidLandAction(action) {
		return questions.Question{}, invalid("unknown land action %q: give merge, pr or discard", action)
	}
	if s.opts.AskHuman == nil {
		return questions.Question{}, invalid("land requests are not available on this server")
	}
	detail, err := s.Get(ctx, id)
	if err != nil {
		return questions.Question{}, err
	}
	job := detail.Job
	if err := s.mayRequestLand(caller, job); err != nil {
		return questions.Question{}, err
	}
	if why := s.landBlocked(job, action, nil); why != "" {
		return questions.Question{}, invalid("%s", why)
	}
	token, err := s.reserveLandRequest(ctx, id, action)
	if err != nil {
		return questions.Question{}, err
	}
	question, err := s.opts.AskHuman(ctx, landQuestion(job, action, reason, token, firstNonEmptyDir(s.landing().MergeMode, landing.ModeMerge)), caller)
	if err != nil {
		s.dropLandRequest(ctx, id, token, "the question could not be asked")
		return questions.Question{}, err
	}
	s.linkLandQuestion(ctx, id, token, question.ID)
	return question, nil
}

// mayRequestLand says whether the caller may ask to land job (R5-S1): a job's own agent, the
// agent of its parent job, the lead of its project, or the orchestrator. Anyone else, and any
// caller without a token, is refused.
func (s *Service) mayRequestLand(caller questions.Caller, job Job) error {
	if caller.PaneID == "" {
		return invalid("only an agent crowsnest started can ask to land a job")
	}
	if caller.JobID != "" {
		if caller.JobID == job.ID || caller.JobID == job.ParentID {
			return nil
		}
		return invalid("you hold job %s: you may ask to land only that job or one it delegated, not %s", caller.JobID, job.ID)
	}
	agent, ok := s.standingOf(caller.PaneID)
	switch {
	case !ok:
		return invalid("only a job's own agent, its parent, its project's lead or the orchestrator may ask to land it")
	case agent.ProjectID != 0 && agent.ProjectID != job.ProjectID:
		return invalid("job %s is not in your project; a lead may ask to land only its own project's jobs", job.ID)
	}
	return nil
}

// reserveLandRequest is step 1: it records the request with a fresh token, or refuses when one
// is already waiting.
func (s *Service) reserveLandRequest(ctx context.Context, id, action string) (string, error) {
	token, err := newLandToken()
	if err != nil {
		return "", err
	}
	_, err = s.mutate(ctx, id, func(r *store.JobRecord, _ *change) error {
		if r.LandRequest != "" {
			return invalid("a land request for job %s is already waiting for the human", id)
		}
		if r.LandState != "" || !r.TornDownAt.IsZero() {
			return invalid("job %s is already landed", id)
		}
		r.LandRequest, r.LandToken, r.LandQuestionID, r.LandRequestedAt = action, token, 0, s.opts.Now()
		return nil
	})
	return token, err
}

// newLandToken returns 128 random bits in hex.
func newLandToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("making a land request token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// landQuestion is the human's question for one reserved request.
func landQuestion(job Job, action, reason, token, mergeMode string) questions.AskInput {
	body := fmt.Sprintf("An agent asks to **%s** job %s, %q.\n\n%s\n\n", landVerb(action), job.ID, job.Title, confirmText(job, action, mergeMode))
	if reason = strings.TrimSpace(reason); reason != "" {
		body += "Its reason (written by the agent, not crowsnest):\n\n" + indent(oneLine(reason, 600)) + "\n\n"
	}
	body += "Open the job's Review tab to see the change before you allow it."
	return questions.AskInput{
		Title: fmt.Sprintf("Land job %s: %s?", job.ID, landVerb(action)), BodyMD: body, Kind: "confirm", Urgency: "normal",
		Options: []questions.Option{{Key: "allow", Label: "Allow: " + landVerb(action)}, {Key: "deny", Label: "Deny"}},
		// A confirm needs a recommendation, and before R5 this question had none, so every real
		// land request was refused by validation (only a fake asker in the tests hid it). Deny,
		// as for a delegation approval: crowsnest has not looked at the change.
		Recommend: "deny",
		Context: map[string]string{questions.ContextLandFor: job.ID, questions.ContextLandAction: action, questions.ContextLandToken: token},
	}
}

// linkLandQuestion is step 3: it stores the question's id on the request it was asked for.
func (s *Service) linkLandQuestion(ctx context.Context, id, token, questionID string) {
	numeric, err := questions.ParseID(questionID)
	if err != nil {
		return
	}
	_, err = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if r.LandToken != token || r.LandQuestionID != 0 {
			return errStaleLandAnswer // settled already, or another request: nothing to link
		}
		r.LandQuestionID = numeric
		c.event(EventLand, "An agent asked to "+landVerb(r.LandRequest)+" it; waiting for you ("+questionID+")", map[string]any{"question": questionID})
		return nil
	})
	if err != nil && !errors.Is(err, errStaleLandAnswer) {
		s.opts.Logger.Warn("linking a land question; the reconciler will find it", "job", id, "question", questionID, "err", err)
	}
}

// dropLandRequest clears the request holding token, journaling why.
func (s *Service) dropLandRequest(ctx context.Context, id, token, why string) {
	_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if r.LandToken != token {
			return errStaleLandAnswer
		}
		action := r.LandRequest
		clearLandRequest(r)
		c.event(EventLand, "The request to "+landVerb(action)+" was dropped: "+why, nil)
		return nil
	})
}

// clearLandRequest removes every trace of a request from the record.
func clearLandRequest(r *store.JobRecord) {
	r.LandRequest, r.LandToken, r.LandQuestionID, r.LandRequestedAt = "", "", 0, time.Time{}
}

// landAnswer is what step 4 needs from a settled question, from the callback or the database.
type landAnswer struct {
	label   string // q_N, for the timeline
	status  string
	key     string // the chosen option, "" for none or several
	note    string
	context map[string]string
}

// LandRequestSettled applies the human's answer to a land request (the questions service's settle
// callback). Allow with no note runs it; anything else drops it. A note is a condition crowsnest
// cannot follow, as for delegation. An answer that does not match the request the job holds now
// does nothing.
func (s *Service) LandRequestSettled(ctx context.Context, jobID string, question questions.Question) {
	s.applyLandAnswer(ctx, jobID, landAnswer{
		label: question.ID, status: string(question.Status), key: answerKey(question.AnswerValue),
		note: question.AnswerNote, context: question.Context,
	})
}

// applyLandAnswer is step 4.
func (s *Service) applyLandAnswer(ctx context.Context, jobID string, answer landAnswer) {
	token := answer.context[questions.ContextLandToken]
	if answer.status == string(questions.StatusOpen) || token == "" || answer.context[questions.ContextLandFor] != jobID {
		return
	}
	allowed := answer.status == string(questions.StatusAnswered) && answer.key == "allow" && strings.TrimSpace(answer.note) == ""
	var action string
	_, err := s.mutate(ctx, jobID, func(r *store.JobRecord, c *change) error {
		if r.LandRequest == "" || r.LandToken != token || answer.context[questions.ContextLandAction] != r.LandRequest {
			return errStaleLandAnswer
		}
		action = r.LandRequest
		clearLandRequest(r)
		if allowed {
			c.event(EventLand, "You allowed the request to "+landVerb(action)+" ("+answer.label+")", nil)
		} else {
			c.event(EventLand, "You did not allow the request to "+landVerb(action)+" ("+answer.label+" "+answer.status+")", nil)
		}
		return nil
	})
	if err != nil || !allowed {
		return
	}
	if _, err := s.Land(ctx, jobID, action, "you, as an agent asked"); err != nil {
		_ = s.Journal(ctx, jobID, EventLand, "The land you allowed was refused: "+oneLine(err.Error(), 240), nil)
	}
}

// ReconcileLandRequests repairs every waiting land request from the database (serve calls it at
// start; the supervisor's tick on every pass): it applies an answer the callback missed, links a
// question whose id was never stored, and drops a request whose question was never asked.
func (s *Service) ReconcileLandRequests(ctx context.Context) {
	records, err := s.opts.Store.ListLandRequests(ctx, listLimit)
	if err != nil {
		if ctx.Err() == nil {
			s.opts.Logger.Warn("reading waiting land requests", "err", err)
		}
		return
	}
	for _, r := range records {
		s.reconcileLandRequest(ctx, r)
	}
}

func (s *Service) reconcileLandRequest(ctx context.Context, r store.JobRecord) {
	id := FormatID(r.ID)
	var question store.QuestionRecord
	var err error
	if r.LandQuestionID != 0 {
		question, err = s.opts.Store.GetQuestion(ctx, r.LandQuestionID)
	} else {
		question, err = s.opts.Store.FindLandQuestion(ctx, id, r.LandToken)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		if s.opts.Now().Sub(r.LandRequestedAt) >= landRequestGrace {
			s.dropLandRequest(ctx, id, r.LandToken, "its question was never asked (crowsnest stopped while asking); the agent may ask again")
		}
		return
	case err != nil:
		return
	}
	label := questions.FormatID(question.ID)
	if question.Status == string(questions.StatusOpen) {
		if r.LandQuestionID == 0 {
			s.linkLandQuestion(ctx, id, r.LandToken, label)
		}
		return
	}
	var context map[string]string
	var value any
	_ = json.Unmarshal(question.Context, &context)
	_ = json.Unmarshal(question.AnswerValue, &value)
	if context[questions.ContextLandToken] != r.LandToken {
		// The linked question is not this request's (it cannot be, short of a hand-edited
		// database): nothing approves the request, so it goes.
		s.dropLandRequest(ctx, id, r.LandToken, "its linked question "+label+" was not asked for it")
		return
	}
	s.applyLandAnswer(ctx, id, landAnswer{label: label, status: question.Status, key: answerKey(value), note: question.AnswerNote, context: context})
}
