// Package digest is the daily digest, made real (DESIGN.md 7.12): one short page a day of what
// finished, what is stuck or waiting on the human, what each project's lead did, what the day
// cost, and what is queued next. It is a built-in schedule, off by default, with one switch and a
// time in the Schedule sheet, and a "Digest now" in the palette.
//
// The facts come from crowsnest (jobs, questions, approvals, spend), never from a model's memory.
// When the orchestrator is running it is handed the facts in a file and asked to write the
// digest from them, delivering it with `crowsnest digest submit` (the socket); if it is off, not
// ready, or has not delivered within DeliverWithin, crowsnest writes a plain factual digest
// itself. Either way the digest is a `notice` in the Inbox (pushed once, as notices are), and a
// row in the digests table for the Digests list.
package digest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/store"
)

const (
	kvSettings = "digest.settings"
	// DeliverWithin is how long the orchestrator has to submit a digest before crowsnest sends
	// its own.
	DeliverWithin = 15 * time.Minute
	// checkInterval is how often the schedule is looked at.
	checkInterval = 30 * time.Second
	// maxBodyBytes caps a submitted digest.
	maxBodyBytes = 32 << 10
	// DefaultAt is the digest's default time of day.
	DefaultAt = "17:00"
)

// Settings are the Schedule sheet's switch and time.
type Settings struct {
	Enabled bool   `json:"enabled"`
	At      string `json:"at"` // HH:MM, local
}

// Facts are what a digest is written from.
type Facts struct {
	Day       string    `json:"day"` // YYYY-MM-DD, local
	Finished  []JobFact `json:"finished"`
	Failed    []JobFact `json:"failed"`
	Stuck     []JobFact `json:"stuck"` // live and stuck, or needing the human
	InReview  []JobFact `json:"in_review"`
	Queued    []JobFact `json:"queued"`   // planned next: waiting to start or for a standing agent
	Unlanded  []JobFact `json:"unlanded"` // finished with a worktree still around
	Projects  []Project `json:"projects"` // per project: what its jobs did today
	Questions int       `json:"open_questions"`
	Blocking  int       `json:"blocking_questions"`
	Approvals int       `json:"pending_approvals"`
	Spend     string    `json:"spend_today"` // "$1.20 est. (2.1M tokens)" or "not measured"
}

// JobFact is one job in the facts.
type JobFact struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Cause   string `json:"cause,omitempty"`
	Project string `json:"project,omitempty"`
	Holder  string `json:"holder,omitempty"` // the agent holding it (a lead's name)
}

// Project is one project's day.
type Project struct {
	Name     string `json:"name"`
	Lead     string `json:"lead"`
	Finished int    `json:"finished"`
	Live     int    `json:"live"`
}

// Store is what the digest reads and writes.
type Store interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	JobsSettledBetween(ctx context.Context, from, to time.Time) ([]store.JobRecord, error)
	ListJobs(ctx context.Context, states []string, limit int) ([]store.JobRecord, error)
	ListUnlandedWorktrees(ctx context.Context, limit int) ([]store.JobRecord, error)
	CountOpenQuestions(ctx context.Context) (open, blocking int, err error)
	InsertDigest(ctx context.Context, record store.DigestRecord) (int64, error)
	ListDigests(ctx context.Context, limit int) ([]store.DigestRecord, error)
	DigestForDay(ctx context.Context, day string) (store.DigestRecord, bool, error)
}

// Options configure a Service.
type Options struct {
	Store    Store
	Journal  *journal.Journal
	Logger   *slog.Logger
	Location *time.Location
	Now      func() time.Time
	StateDir string // facts files go in <StateDir>/digests
	// Notice puts the digest in the Inbox as a notice and returns its question id (0 when none).
	Notice func(ctx context.Context, title, body string) (int64, error)
	// Orchestrator hands the facts file to the orchestrator to write from; an error (it is off,
	// busy, at a dialog) means crowsnest writes the digest itself. Nil: always crowsnest's.
	Orchestrator func(ctx context.Context, day, factsFile string) error
	// Paused reports the pause switch: the scheduled digest waits while paused (Digest now does
	// not). Nil: never paused.
	Paused func() bool
	// Projects names projects by id and their leads. Nil: no project lines.
	Projects func(ctx context.Context) map[int64][2]string
	// Spend is today's spend in words. Nil: "not measured".
	Spend func(ctx context.Context) string
	// Approvals counts pending tool-call approvals. Nil: 0.
	Approvals func(ctx context.Context) int
}

// Service writes digests.
type Service struct {
	opts Options

	mu      sync.Mutex
	pending map[string]time.Time // day → when the orchestrator was asked
	tried   string               // the day the schedule last started a digest
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, pending: map[string]time.Time{}}
}

// Settings reads the switch and time.
func (s *Service) Settings(ctx context.Context) Settings {
	settings := Settings{At: DefaultAt}
	if raw, err := s.opts.Store.GetKV(ctx, kvSettings); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &settings)
	}
	if _, ok := parseClock(settings.At); !ok {
		settings.At = DefaultAt
	}
	return settings
}

// SaveSettings stores the switch and time.
func (s *Service) SaveSettings(ctx context.Context, settings Settings) (Settings, error) {
	if settings.At == "" {
		settings.At = DefaultAt
	}
	if _, ok := parseClock(settings.At); !ok {
		return Settings{}, fmt.Errorf("the digest time %q is not a time of day; use HH:MM", settings.At)
	}
	body, _ := json.Marshal(settings)
	if err := s.opts.Store.SetKV(ctx, kvSettings, string(body)); err != nil {
		return Settings{}, fmt.Errorf("saving the digest settings: %w", err)
	}
	return settings, nil
}

func parseClock(text string) (time.Duration, bool) {
	at, err := time.Parse("15:04", strings.TrimSpace(text))
	if err != nil {
		return 0, false
	}
	return time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute, true
}

// List returns the digests, newest first.
func (s *Service) List(ctx context.Context, limit int) ([]store.DigestRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	return s.opts.Store.ListDigests(ctx, limit)
}

// Run checks the schedule until ctx ends.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		s.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Check starts today's digest when it is due, and sends crowsnest's own when the orchestrator
// has not delivered in time.
func (s *Service) Check(ctx context.Context) {
	now := s.opts.Now().In(s.opts.Location)
	day := now.Format("2006-01-02")
	s.fallBackIfLate(ctx, now)
	settings := s.Settings(ctx)
	if !settings.Enabled || (s.opts.Paused != nil && s.opts.Paused()) {
		return
	}
	at, _ := parseClock(settings.At)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.opts.Location)
	if now.Before(midnight.Add(at)) {
		return
	}
	s.mu.Lock()
	tried := s.tried == day
	s.tried = day
	s.mu.Unlock()
	if tried {
		return
	}
	if _, exists, err := s.opts.Store.DigestForDay(ctx, day); err != nil || exists {
		return
	}
	if _, err := s.Start(ctx); err != nil {
		s.opts.Logger.Warn("starting the daily digest", "err", err)
	}
}

// StartResult says how a digest is on its way.
type StartResult struct {
	Day    string `json:"day"`
	Source string `json:"source"` // orchestrator (asked; crowsnest's own follows if it is late) | crowsnest (sent)
	Note   string `json:"note"`
}

// Start writes today's digest now: asks the orchestrator, or sends crowsnest's own.
func (s *Service) Start(ctx context.Context) (StartResult, error) {
	now := s.opts.Now().In(s.opts.Location)
	day := now.Format("2006-01-02")
	facts, err := s.Facts(ctx, now)
	if err != nil {
		return StartResult{}, err
	}
	if s.opts.Orchestrator != nil {
		if file, err := s.writeFacts(day, facts); err == nil {
			if err := s.opts.Orchestrator(ctx, day, file); err == nil {
				s.mu.Lock()
				s.pending[day] = now
				s.mu.Unlock()
				return StartResult{Day: day, Source: "orchestrator", Note: "The orchestrator is writing it from crowsnest's facts; crowsnest sends its own if it has not in 15 minutes."}, nil
			} else {
				s.opts.Logger.Info("the orchestrator cannot write the digest; crowsnest writes it", "why", err)
			}
		}
	}
	if err := s.deliver(ctx, day, "crowsnest", Render(facts)); err != nil {
		return StartResult{}, err
	}
	return StartResult{Day: day, Source: "crowsnest", Note: "Crowsnest wrote it from its own records."}, nil
}

// SeedDemo stores today's plain digest in the history without an Inbox notice (--demo shows one
// digest in the Digests list without changing the Inbox's counts).
func (s *Service) SeedDemo(ctx context.Context) error {
	now := s.opts.Now().In(s.opts.Location)
	facts, err := s.Facts(ctx, now)
	if err != nil {
		return err
	}
	_, err = s.opts.Store.InsertDigest(ctx, store.DigestRecord{Day: facts.Day, At: s.opts.Now(), Source: "crowsnest", BodyMD: Render(facts)})
	return err
}

// Submit is the orchestrator's digest for day (from `crowsnest digest submit`). Only a day it was
// asked for is taken, once.
func (s *Service) Submit(ctx context.Context, day, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("the digest is empty")
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("the digest is longer than %d bytes; keep it to what the human would act on", maxBodyBytes)
	}
	s.mu.Lock()
	if day == "" {
		for pending := range s.pending {
			day = pending
		}
	}
	_, asked := s.pending[day]
	delete(s.pending, day)
	s.mu.Unlock()
	if !asked {
		return fmt.Errorf("no digest was asked for %q (or it was already delivered)", day)
	}
	return s.deliver(ctx, day, "orchestrator", body)
}

func (s *Service) fallBackIfLate(ctx context.Context, now time.Time) {
	s.mu.Lock()
	var late []string
	for day, asked := range s.pending {
		if now.Sub(asked) >= DeliverWithin {
			late = append(late, day)
			delete(s.pending, day)
		}
	}
	s.mu.Unlock()
	for _, day := range late {
		facts, err := s.Facts(ctx, now)
		if err != nil {
			continue
		}
		body := Render(facts) + "\n\n_The orchestrator did not deliver a digest in time, so crowsnest wrote this one from its records._"
		if err := s.deliver(ctx, day, "crowsnest", body); err != nil {
			s.opts.Logger.Warn("sending the fallback digest", "err", err)
		}
	}
}

func (s *Service) deliver(ctx context.Context, day, source, body string) error {
	title := "Digest " + day
	var questionID int64
	if s.opts.Notice != nil {
		id, err := s.opts.Notice(ctx, title, body)
		if err != nil {
			s.opts.Logger.Warn("putting the digest in the Inbox", "err", err)
		}
		questionID = id
	}
	if _, err := s.opts.Store.InsertDigest(ctx, store.DigestRecord{Day: day, At: s.opts.Now(), Source: source, BodyMD: body, QuestionID: questionID}); err != nil {
		return err
	}
	if s.opts.Journal != nil {
		s.opts.Journal.Add(ctx, journal.KindDigest, title+" written by "+source, map[string]any{"day": day, "source": source})
	}
	return nil
}

func (s *Service) writeFacts(day string, facts Facts) (string, error) {
	if s.opts.StateDir == "" {
		return "", errors.New("no state directory")
	}
	dir := filepath.Join(s.opts.StateDir, "digests")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "facts-"+day+".md")
	_ = os.Remove(path)
	body := "# Facts for the digest of " + day + "\n\nGathered by crowsnest from its records. Write the digest from these facts only.\n\n" + Render(facts)
	if raw, err := json.MarshalIndent(facts, "", "  "); err == nil {
		body += "\n\n## The same facts as JSON\n\n```json\n" + string(raw) + "\n```\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ---- Facts ---------------------------------------------------------------------------------

// Facts gathers the day's facts (local midnight to now).
func (s *Service) Facts(ctx context.Context, now time.Time) (Facts, error) {
	now = now.In(s.opts.Location)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.opts.Location)
	facts := Facts{Day: now.Format("2006-01-02"), Finished: []JobFact{}, Failed: []JobFact{}, Stuck: []JobFact{}, InReview: []JobFact{},
		Queued: []JobFact{}, Unlanded: []JobFact{}, Projects: []Project{}, Spend: "not measured"}
	projects := map[int64][2]string{}
	if s.opts.Projects != nil {
		projects = s.opts.Projects(ctx)
	}
	fact := func(r store.JobRecord) JobFact {
		f := JobFact{ID: fmt.Sprintf("j_%d", r.ID), Title: r.Title, State: r.State, Cause: r.Cause, Holder: r.AgentName}
		if p, ok := projects[r.ProjectID]; ok {
			f.Project = p[0]
		}
		return f
	}
	perProject := map[int64]*Project{}
	project := func(id int64) *Project {
		p, ok := perProject[id]
		if !ok {
			names := projects[id]
			p = &Project{Name: names[0], Lead: names[1]}
			perProject[id] = p
		}
		return p
	}
	settled, err := s.opts.Store.JobsSettledBetween(ctx, midnight, now.Add(time.Second))
	if err != nil {
		return Facts{}, err
	}
	for _, r := range settled {
		if r.ReviewOf != 0 {
			continue // reviewer jobs are part of the job they reviewed
		}
		switch r.State {
		case "done":
			facts.Finished = append(facts.Finished, fact(r))
			if r.ProjectID != 0 {
				project(r.ProjectID).Finished++
			}
		case "failed":
			facts.Failed = append(facts.Failed, fact(r))
		}
	}
	live, err := s.opts.Store.ListJobs(ctx, []string{"queued", "running", "needs_you", "blocked", "in_review"}, 500)
	if err != nil {
		return Facts{}, err
	}
	sort.Slice(live, func(i, j int) bool { return live[i].ID < live[j].ID })
	for _, r := range live {
		if r.ReviewOf != 0 {
			continue
		}
		if r.ProjectID != 0 {
			project(r.ProjectID).Live++
		}
		switch {
		case r.State == "in_review":
			facts.InReview = append(facts.InReview, fact(r))
		case r.State == "queued":
			facts.Queued = append(facts.Queued, fact(r))
		case r.Stuck || r.State == "needs_you" || r.State == "blocked":
			facts.Stuck = append(facts.Stuck, fact(r))
		}
	}
	if unlanded, err := s.opts.Store.ListUnlandedWorktrees(ctx, 100); err == nil {
		for _, r := range unlanded {
			facts.Unlanded = append(facts.Unlanded, fact(r))
		}
	}
	facts.Questions, facts.Blocking, _ = s.opts.Store.CountOpenQuestions(ctx)
	if s.opts.Approvals != nil {
		facts.Approvals = s.opts.Approvals(ctx)
	}
	if s.opts.Spend != nil {
		facts.Spend = s.opts.Spend(ctx)
	}
	ids := make([]int64, 0, len(perProject))
	for id := range perProject {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if p := perProject[id]; p.Name != "" {
			facts.Projects = append(facts.Projects, *p)
		}
	}
	return facts, nil
}

// Render is crowsnest's own digest: plain and factual.
func Render(f Facts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Digest for %s**\n\n", f.Day)
	section(&b, "Finished today", f.Finished, "nothing finished")
	if len(f.Failed) > 0 {
		section(&b, "Failed today", f.Failed, "")
	}
	b.WriteString("### Waiting on you\n\n")
	waiting := 0
	if f.Questions > 0 {
		fmt.Fprintf(&b, "- %d open question(s), %d blocking\n", f.Questions, f.Blocking)
		waiting++
	}
	if f.Approvals > 0 {
		fmt.Fprintf(&b, "- %d tool call(s) waiting for approval\n", f.Approvals)
		waiting++
	}
	for _, j := range f.Stuck {
		fmt.Fprintf(&b, "- %s %s (%s%s)\n", j.ID, j.Title, j.State, causeSuffix(j.Cause))
		waiting++
	}
	for _, j := range f.InReview {
		fmt.Fprintf(&b, "- %s %s is in review\n", j.ID, j.Title)
		waiting++
	}
	if len(f.Unlanded) > 0 {
		fmt.Fprintf(&b, "- %d finished job(s) with a worktree still to land or discard\n", len(f.Unlanded))
		waiting++
	}
	if waiting == 0 {
		b.WriteString("- nothing\n")
	}
	b.WriteString("\n")
	if len(f.Projects) > 0 {
		b.WriteString("### Projects\n\n")
		for _, p := range f.Projects {
			fmt.Fprintf(&b, "- %s (lead %s): %d finished today, %d live\n", p.Name, firstNonEmpty(p.Lead, "none"), p.Finished, p.Live)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "### Spend today\n\n%s\n\n", f.Spend)
	section(&b, "Next up (queued)", f.Queued, "nothing queued")
	return strings.TrimSpace(b.String())
}

func section(b *strings.Builder, title string, jobs []JobFact, empty string) {
	fmt.Fprintf(b, "### %s\n\n", title)
	if len(jobs) == 0 {
		fmt.Fprintf(b, "- %s\n\n", empty)
		return
	}
	for _, j := range jobs {
		line := "- " + j.ID + " " + j.Title
		if j.Project != "" {
			line += " (" + j.Project + ")"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

func causeSuffix(cause string) string {
	if cause == "" {
		return ""
	}
	return ": " + cause
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
