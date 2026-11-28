// Line comments on a diff (DESIGN.md 6.21): the human selects a line or a range on one side of a
// file (or a hunk, or the whole file), writes a note, and later sends every unsent note to the
// agent as one message. This file keeps the notes and works out what each one is now:
//
//   - draft: not sent yet;
//   - sent: sent, and the file still holds what the note was written against;
//   - outdated: the file's work-tree content (its new blob in the "all" comparison) is no longer
//     the one the note was written against, or the file no longer differs from the base. The
//     quote kept on the note still shows what it was about.
//
// A note's quote is read here from the blob the browser showed (named by the file's hash, 409
// stale otherwise), never taken from the browser, so what goes back to the agent is what git held.
// Composing the message is pure (Compose, in compose.go); delivering it is the caller's (a job's
// message path, or a lead's prompt), because this package knows nothing of jobs.

package changes

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/store"
)

// Comment sides.
const (
	SideOld  = "old"
	SideNew  = "new"
	SideFile = "file"
)

// Comment statuses.
const (
	StatusDraft    = "draft"
	StatusSent     = "sent"
	StatusOutdated = "outdated"
	// StatusWaiting: a send holds the note and has not delivered it yet (R9-C3, C5).
	StatusWaiting = "waiting"
)

const (
	// maxCommentBody is the longest note, in characters.
	maxCommentBody = 8000
	// maxCommentLines is the widest range one note may cover.
	maxCommentLines = 2000
	// quoteLines and quoteLineChars bound the quote kept on a note (and sent to the agent).
	quoteLines     = 40
	quoteLineChars = 400
	// commentPublish is the revision suffix a comment change publishes, so open viewers refetch.
	commentPublish = "-comments"
)

// Comment is one note as the browser gets it.
type Comment struct {
	ID        int64      `json:"id"`
	Path      string     `json:"path"`
	Side      string     `json:"side"`
	LineStart int        `json:"line_start"`
	LineEnd   int        `json:"line_end"`
	Quote     string     `json:"quote"`
	Body      string     `json:"body"`
	Scope     string     `json:"scope"`
	Hash      string     `json:"hash"`
	OldBlob   string     `json:"old_blob"`
	NewBlob   string     `json:"new_blob"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	// Pending: it goes with the next send (a draft, an outdated note not sent since it changed, or
	// a note edited since it was sent).
	Pending bool `json:"pending"`
	// Waiting: a send holds it and has not delivered it yet (a job that may not continue yet
	// types it later, R9-C3); it is neither a draft nor sent. Uncertain: a send of it may or may
	// not have arrived (crowsnest stopped mid-send); it is due again and the human decides.
	Waiting   bool `json:"waiting"`
	Uncertain bool `json:"uncertain"`

	version int64 // the text's version, for a send's claim
}

// CommentList is GET /changes/…/comments: a subject's notes (dismissed ones left out), with counts.
type CommentList struct {
	Subject  string    `json:"subject"`
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	JobID    string    `json:"job_id,omitempty"`
	JobState string    `json:"job_state,omitempty"`
	Comments []Comment `json:"comments"`
	// Drafts counts the notes the next send would carry; Sent and Outdated the rest.
	Drafts     int        `json:"drafts"`
	Sent       int        `json:"sent"`
	Outdated   int        `json:"outdated"`
	Waiting    int        `json:"waiting"`
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
}

// CommentAsk is a new note: where it is and what it says. Hash is the file's hash the browser
// showed; LineStart and LineEnd are on Side (both 0 for the whole file).
type CommentAsk struct {
	Path      string `json:"path"`
	Scope     string `json:"scope"`
	Side      string `json:"side"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Body      string `json:"body"`
	Hash      string `json:"hash"`
}

// Comments lists a subject's notes with their status now.
func (s *Service) Comments(ctx context.Context, ask Ask) (CommentList, error) {
	target, err := s.resolve(ctx, ask)
	if err != nil {
		return CommentList{}, err
	}
	r, err := s.read(ctx, target, false, true)
	if err != nil {
		return CommentList{}, err
	}
	rows, err := s.opts.Store.ListDiffComments(ctx, target.Subject)
	if err != nil {
		return CommentList{}, err
	}
	return listComments(target, r, rows), nil
}

// listComments decorates rows with their status against reading r.
func listComments(target Target, r *reading, rows []store.DiffComment) CommentList {
	list := CommentList{Subject: target.Subject, Kind: target.Kind, Label: target.Label, JobID: target.JobID, JobState: target.JobState, Comments: []Comment{}}
	current := map[string]landing.DiffFile{}
	for _, file := range r.scopes[ScopeAll] {
		current[file.Path] = file
	}
	for _, row := range rows {
		if !row.DismissedAt.IsZero() {
			continue
		}
		comment := toComment(row)
		file, ok := current[row.Path]
		switch {
		case !ok || !anchorHolds(row, file):
			comment.Status = StatusOutdated
			list.Outdated++
		case comment.Waiting:
			comment.Status = StatusWaiting
			list.Waiting++
		case !row.SentAt.IsZero() && !comment.Pending:
			comment.Status = StatusSent
			list.Sent++
		default:
			comment.Status = StatusDraft
		}
		if comment.Pending {
			list.Drafts++
		}
		if comment.SentAt != nil && (list.LastSentAt == nil || comment.SentAt.After(*list.LastSentAt)) {
			at := *comment.SentAt
			list.LastSentAt = &at
		}
		list.Comments = append(list.Comments, comment)
	}
	return list
}

// anchorHolds reports whether the side a note was written on still holds the content it was
// written against (R9-C11): the new blob for a new-side note, the old blob for an old-side one,
// both for a whole-file note. A commit that leaves the work-tree content as it was changes
// neither, so it does not outdate a note.
func anchorHolds(row store.DiffComment, file landing.DiffFile) bool {
	switch row.Side {
	case SideOld:
		return file.OldBlob == row.OldBlob
	case SideNew:
		return file.NewBlob == row.NewBlob
	}
	return file.OldBlob == row.OldBlob && file.NewBlob == row.NewBlob
}

func toComment(row store.DiffComment) Comment {
	comment := Comment{ID: row.ID, Path: row.Path, Side: row.Side, LineStart: row.LineStart, LineEnd: row.LineEnd, Quote: row.Quote, Body: row.Body,
		Scope: row.Scope, Hash: row.Hash, OldBlob: row.OldBlob, NewBlob: row.NewBlob, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Pending: row.Due(), Waiting: row.Claim != "", Uncertain: !row.UncertainAt.IsZero() && row.Due(), version: row.Version}
	if !row.SentAt.IsZero() {
		at := row.SentAt
		comment.SentAt = &at
	}
	return comment
}

// AddComment saves a new draft note on a file the comparison lists, at the version the browser
// showed (409 stale otherwise), with the commented lines quoted from git.
func (s *Service) AddComment(ctx context.Context, ask Ask, want CommentAsk) (Comment, error) {
	body, err := checkBody(want.Body)
	if err != nil {
		return Comment{}, err
	}
	if err := checkRange(want); err != nil {
		return Comment{}, err
	}
	target, err := s.resolve(ctx, ask)
	if err != nil {
		return Comment{}, err
	}
	r, err := s.read(ctx, target, false, true)
	if err != nil {
		return Comment{}, err
	}
	scope := want.Scope
	if scope == "" || scope == ScopeSinceViewed {
		scope = ScopeAll
	}
	if !ValidScope(scope) {
		return Comment{}, fail("invalid", "unknown scope %q", want.Scope)
	}
	file, ok := findFile(r.scopes[scope], want.Path)
	if !ok {
		return Comment{}, fail("not_found", "%s is not one of the files in this diff", want.Path)
	}
	if want.Hash != file.Hash {
		return Comment{}, fail("stale", "%s changed after it was shown; look at it again before commenting", want.Path)
	}
	quote, err := s.quoteOf(ctx, target, file, want)
	if err != nil {
		return Comment{}, err
	}
	now := s.opts.Now().UTC()
	row, err := s.opts.Store.InsertDiffComment(ctx, store.DiffComment{Subject: target.Subject, Author: reviewer, Scope: scope, Path: file.Path, Side: want.Side,
		LineStart: want.LineStart, LineEnd: want.LineEnd, Quote: quote, Body: body, Hash: file.Hash, OldBlob: file.OldBlob, NewBlob: file.NewBlob,
		CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return Comment{}, err
	}
	s.noteMarked(target.Subject) // a job with comments is swept like one with marks
	s.publish(target.Subject, r.fingerprint+commentPublish)
	comment := toComment(row)
	comment.Status = StatusDraft
	return comment, nil
}

func checkBody(text string) (string, error) {
	body := strings.TrimSpace(text)
	switch {
	case body == "":
		return "", fail("invalid", "write a note")
	case utf8.RuneCountInString(body) > maxCommentBody:
		return "", fail("invalid", "a note is at most %d characters", maxCommentBody)
	}
	return body, nil
}

func checkRange(want CommentAsk) error {
	switch want.Side {
	case SideFile:
		if want.LineStart != 0 || want.LineEnd != 0 {
			return fail("invalid", "a note on the whole file has no lines")
		}
	case SideOld, SideNew:
		if want.LineStart < 1 || want.LineEnd < want.LineStart {
			return fail("invalid", "lines run from line_start to line_end, from 1")
		}
		if want.LineEnd-want.LineStart >= maxCommentLines {
			return fail("invalid", "a note covers at most %d lines", maxCommentLines)
		}
	default:
		return fail("invalid", "side is old, new or file")
	}
	return nil
}

func findFile(files []landing.DiffFile, path string) (landing.DiffFile, bool) {
	for _, file := range files {
		if file.Path == path {
			return file, true
		}
	}
	return landing.DiffFile{}, false
}

// quoteOf reads the commented lines from the blob of the note's side. A whole-file note quotes
// nothing; a side the file does not have (the old side of an added file) is refused, and so is a
// range past its end. A side too large or binary to read is quoted as nothing.
func (s *Service) quoteOf(ctx context.Context, target Target, file landing.DiffFile, want CommentAsk) (string, error) {
	if want.Side == SideFile {
		return "", nil
	}
	blob := file.NewBlob
	if want.Side == SideOld {
		blob = file.OldBlob
	}
	if blob == "" || blob == landing.NullBlob {
		return "", fail("invalid", "%s has no %s side to comment on", file.Path, want.Side)
	}
	if file.Binary || file.TooLarge || strings.HasPrefix(blob, "stamp:") {
		return "", nil
	}
	root, err := s.repositoryTop(ctx, target)
	if err != nil {
		return "", err
	}
	text, err := landing.Blob(ctx, root, blob, landing.MaxBlobBytes)
	if err != nil {
		return "", nil //nolint:nilerr // too large to quote: the note stands on its line numbers
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if want.LineEnd > len(lines) {
		return "", fail("invalid", "%s has %d lines on the %s side; the note asks for line %d", file.Path, len(lines), want.Side, want.LineEnd)
	}
	return quoteText(lines[want.LineStart-1 : want.LineEnd]), nil
}

// quoteText joins lines, each cut at quoteLineChars, at most quoteLines of them.
func quoteText(lines []string) string {
	out := make([]string, 0, min(len(lines), quoteLines)+1)
	for i, line := range lines {
		if i == quoteLines {
			out = append(out, "… ("+strconv.Itoa(len(lines)-quoteLines)+" more lines)")
			break
		}
		if utf8.RuneCountInString(line) > quoteLineChars {
			line = string([]rune(line)[:quoteLineChars]) + "…"
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// EditComment changes a note's text.
func (s *Service) EditComment(ctx context.Context, ask Ask, id int64, text string) error {
	body, err := checkBody(text)
	if err != nil {
		return err
	}
	return s.changeComment(ctx, ask, func(subject string) error {
		return s.opts.Store.UpdateDiffCommentBody(ctx, subject, id, body, s.opts.Now().UTC())
	})
}

// DeleteComment removes a note.
func (s *Service) DeleteComment(ctx context.Context, ask Ask, id int64) error {
	return s.changeComment(ctx, ask, func(subject string) error { return s.opts.Store.DeleteDiffComment(ctx, subject, id) })
}

// DismissComment hides a note for good (an outdated one the agent has answered).
func (s *Service) DismissComment(ctx context.Context, ask Ask, id int64) error {
	return s.changeComment(ctx, ask, func(subject string) error {
		return s.opts.Store.DismissDiffComment(ctx, subject, id, s.opts.Now().UTC())
	})
}

// ResendComment puts a sent note back with the drafts, so the next send carries it again.
func (s *Service) ResendComment(ctx context.Context, ask Ask, id int64) error {
	return s.changeComment(ctx, ask, func(subject string) error {
		row, err := s.opts.Store.GetDiffComment(ctx, subject, id)
		if err != nil {
			return err
		}
		if row.Claim != "" {
			return fail("busy", "a send holds this comment right now; it is re-sent only once that send is over")
		}
		return s.opts.Store.ResendDiffComment(ctx, subject, id)
	})
}

// changeComment resolves the subject (a landed job's notes are gone with it) and applies change.
func (s *Service) changeComment(ctx context.Context, ask Ask, change func(subject string) error) error {
	target, err := s.resolve(ctx, ask)
	if err != nil {
		return err
	}
	if err := change(target.Subject); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fail("not_found", "no such comment here")
		}
		return err
	}
	s.publish(target.Subject, time.Now().Format(time.RFC3339Nano)+commentPublish)
	return nil
}

// Outgoing is what a send carries: the subject's target, the composed body, and the notes in it.
type Outgoing struct {
	Target Target
	Body   string
	IDs    []int64
	// Hidden is how many invisible characters the quotes and notes held (each written out as
	// ⟨U+XXXX⟩ in Body), and Mixed the words that mix alphabets.
	Hidden int
	Mixed  []string
	// Claim is the send holding the notes (Claim), "" for a preview.
	Claim string
	notes []store.ClaimedComment
}

// Outgoing composes every pending note of the subject into one message body (Compose), with
// overall as the human's short note on the whole; no pending note is an error.
func (s *Service) Outgoing(ctx context.Context, ask Ask, overall string) (Outgoing, error) {
	if utf8.RuneCountInString(overall) > maxCommentBody {
		return Outgoing{}, fail("invalid", "the overall note is at most %d characters", maxCommentBody)
	}
	list, err := s.Comments(ctx, ask)
	if err != nil {
		return Outgoing{}, err
	}
	target, err := s.resolve(ctx, ask)
	if err != nil {
		return Outgoing{}, err
	}
	pending := []Comment{}
	for _, comment := range list.Comments {
		if comment.Pending {
			pending = append(pending, comment)
		}
	}
	if len(pending) == 0 {
		if list.Waiting > 0 {
			return Outgoing{}, fail("invalid", "every unsent comment is already in a send that waits to be typed")
		}
		return Outgoing{}, fail("invalid", "there are no unsent comments to send")
	}
	composed := Compose(pending, overall)
	out := Outgoing{Target: target, Body: composed.Text, Hidden: composed.Hidden, Mixed: composed.Mixed}
	for _, comment := range pending {
		out.IDs = append(out.IDs, comment.ID)
		out.notes = append(out.notes, store.ClaimedComment{ID: comment.ID, Version: comment.version})
	}
	return out, nil
}

// Claim composes the subject's due notes (as Outgoing) and takes them for send claim in one
// transaction, at exactly the versions composed (R9-C5): a second send at the same time, or an
// edit between reading and claiming, finds them taken or changed and is refused (409 stale), so
// two sends never carry the same note and an edit is never marked sent with text it did not
// carry. The caller delivers Body and then settles the claim (Settle) by what happened.
func (s *Service) Claim(ctx context.Context, ask Ask, overall, claim string) (Outgoing, error) {
	out, err := s.Outgoing(ctx, ask, overall)
	if err != nil {
		return Outgoing{}, err
	}
	if err := s.opts.Store.ClaimDiffComments(ctx, out.Target.Subject, claim, out.notes, s.opts.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrClaimConflict) {
			return Outgoing{}, fail("stale", "the comments changed, or another send took them, while this one was being made; look again and send")
		}
		return Outgoing{}, err
	}
	out.Claim = claim
	s.publish(out.Target.Subject, time.Now().Format(time.RFC3339Nano)+commentPublish)
	return out, nil
}

// SendOutcome is what became of a send's notes.
type SendOutcome string

// Send outcomes.
const (
	SendDelivered SendOutcome = "delivered" // typed or queued by the agent: the notes are sent, at the version carried
	SendReturned  SendOutcome = "returned"  // provably not delivered: the notes are due again
	SendUncertain SendOutcome = "uncertain" // may or may not have arrived: due again, marked so the human decides
)

// Settle ends send claim's hold on its notes by outcome and tells open viewers.
func (s *Service) Settle(ctx context.Context, claim string, outcome SendOutcome) error {
	subject, n, err := s.opts.Store.SettleDiffCommentClaim(ctx, claim, outcome == SendDelivered, outcome == SendUncertain, s.opts.Now().UTC())
	if err != nil {
		return err
	}
	if n > 0 {
		s.publish(subject, time.Now().Format(time.RFC3339Nano)+commentPublish)
	}
	return nil
}

// MarkSent records that notes went to the agent at at (the demo's seeded history): a claim
// settled as delivered.
func (s *Service) MarkSent(ctx context.Context, subject string, ids []int64, at time.Time) error {
	notes := []store.ClaimedComment{}
	for _, id := range ids {
		row, err := s.opts.Store.GetDiffComment(ctx, subject, id)
		if err != nil {
			return err
		}
		notes = append(notes, store.ClaimedComment{ID: id, Version: row.Version})
	}
	claim := "mark:" + strconv.FormatInt(at.UnixNano(), 36)
	if err := s.opts.Store.ClaimDiffComments(ctx, subject, claim, notes, at); err != nil {
		return err
	}
	if _, _, err := s.opts.Store.SettleDiffCommentClaim(ctx, claim, true, false, at); err != nil {
		return err
	}
	s.publish(subject, at.Format(time.RFC3339Nano)+commentPublish)
	return nil
}

// claimStale is how old a send's claim must be before the sweep may settle it: every send is
// over well inside it unless the process that made it died (a deferred batch is asked about).
const claimStale = 10 * time.Minute

// ClaimLive reports whether a send is still holding its notes for a reason the sweep must
// respect (a job's queued feedback batch). Nil: only the age counts.
type ClaimLive func(ctx context.Context, subject, claim string) (bool, error)

// SweepClaims settles sends a dead or failed request left holding notes (R9-C5): older than
// claimStale and not held by live (a batch still queued on its job), they are due again, marked
// uncertain because the send may have reached the agent before it was lost. An unreadable answer
// from live leaves the claim alone (fail closed: the batch may still be typed).
func (s *Service) SweepClaims(ctx context.Context, live ClaimLive, startup bool) (int, error) {
	claims, err := s.opts.Store.DiffCommentClaims(ctx)
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, claim := range claims {
		// At start-up every request's claim is from a process that is gone, whatever its age.
		if !startup && s.opts.Now().Sub(claim.ClaimedAt) < claimStale {
			continue
		}
		if live != nil {
			held, err := live(ctx, claim.Subject, claim.Claim)
			if err != nil || held {
				continue
			}
		}
		if err := s.Settle(ctx, claim.Claim, SendUncertain); err != nil {
			return settled, err
		}
		settled++
	}
	return settled, nil
}

// sweepClaims runs SweepClaims with the configured liveness check, logging a failure.
func (s *Service) sweepClaims(ctx context.Context, startup bool) {
	if n, err := s.SweepClaims(ctx, s.opts.ClaimLive, startup); err != nil {
		s.opts.Logger.Warn("settling stale comment sends", "err", err)
	} else if n > 0 {
		s.opts.Logger.Info("comment sends left unfinished were handed back as may-or-may-not-have-arrived", "sends", n)
	}
}
