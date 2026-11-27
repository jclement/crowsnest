// Package changes is a diff at any time (DESIGN.md 6.20): what a job's worktree, a project's main
// checkout or the orchestrator's folder holds now against its base, in three comparisons
// (committed, uncommitted, all) plus "since you viewed", with a Viewed mark per file that is tied
// to the file's content and clears by itself when the file changes again.
//
// Reading git is internal/landing's (changes.go there); this package decides when to read it and
// what the browser gets:
//
//   - One computation per subject (and base) at a time, and never more often than MinInterval: a
//     request inside that window gets the last result. A subject someone is watching (asked for
//     in the last WatchFor) is read again after its job's activity (debounced) and on a slow
//     tick, and a `changes` event goes out when anything in it changed.
//   - Marks live in the store (diff_marks). A file counts as viewed when a mark on its path holds
//     its current hash, in whichever comparison the mark was made; a mark whose hash no longer
//     matches makes the file "changed since you viewed it", and the comparison from the version
//     seen to now is served for it.
//   - A job's marks go when it lands, is discarded or is deleted (Resolver.Retired).
//
// The package knows nothing of jobs or projects: a Resolver turns a subject into a Target.
package changes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/store"
)

// Scopes.
const (
	ScopeAll         = "all"
	ScopeCommitted   = "committed"
	ScopeUncommitted = "uncommitted"
	ScopeSinceViewed = "since_viewed"
)

// Subject kinds.
const (
	KindJob          = "job"
	KindProject      = "project"
	KindOrchestrator = "orchestrator"
)

// Base choices for a main checkout (Target.BaseRef).
const (
	BaseUpstream = ""        // the branch's upstream, else origin's default branch
	BaseSession  = "session" // the commit HEAD was at when the lead's conversation started
)

// EventType is the hub event a changed diff publishes: {subject, revision}.
const EventType = "changes"

const (
	// MinInterval is the least time between two git readings of one subject.
	MinInterval = 4 * time.Second
	// debounce waits this long after a job's activity before reading its diff again.
	debounce = 1500 * time.Millisecond
	// WatchFor is how long after its last request a subject is still read in the background.
	WatchFor = 2 * time.Minute
	// tick is how often watched subjects are read again without any activity.
	tick = 10 * time.Second
	// largeChange is the line count past which a file is collapsed by default.
	largeChange = 3000
	// reviewer is the one human's name on marks.
	reviewer = "you"
)

// Target is what a subject resolves to.
type Target struct {
	Subject string `json:"subject"` // j_12, project:3, orchestrator
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Root    string `json:"root"`
	// Gone, when set, says why there is no checkout to read any more (landed, torn down).
	Gone string `json:"-"`
	// A job's: its id, state and project, the branch it started from and its first commit.
	JobID      string `json:"job_id,omitempty"`
	JobState   string `json:"job_state,omitempty"`
	ProjectID  int64  `json:"project_id,omitempty"`
	Project    string `json:"project,omitempty"` // the project's slug
	BaseBranch string `json:"-"`
	BaseCommit string `json:"-"`
	// Since is when the job's agent or the lead's conversation started.
	Since time.Time `json:"-"`
	// BaseRef is the base asked for on a main checkout (BaseUpstream, BaseSession or a ref).
	BaseRef string `json:"-"`
}

// Resolver turns subjects into targets. Errors it returns are shown to the human as they are.
type Resolver interface {
	Job(ctx context.Context, id string) (Target, error)
	Project(ctx context.Context, ref string) (Target, error)
	Orchestrator(ctx context.Context) (Target, error)
	// ProjectJobs are the jobs of a project that have (or had) a checkout of their own, newest
	// first.
	ProjectJobs(ctx context.Context, ref string) ([]Target, error)
	// Retired reports whether subject's marks should go: a job that landed, was discarded or no
	// longer exists.
	Retired(ctx context.Context, subject string) bool
}

// Options configure a Service.
type Options struct {
	Store    *store.Store
	Hub      *hub.Hub
	Resolver Resolver
	Logger   *slog.Logger
	Now      func() time.Time
	// MinInterval overrides MinInterval (tests shorten it).
	MinInterval time.Duration
}

// Service is the live diff and its marks.
type Service struct {
	opts    Options
	mu      sync.Mutex
	entries map[string]*entry // by cache key: subject + "\x00" + base
	marked  map[string]bool   // subjects known to have marks
}

// entry is one subject's last reading.
type entry struct {
	key, subject string
	mu           sync.Mutex // held while reading git
	reading      *reading
	err          error
	at           time.Time // when reading or err was taken
	asked        time.Time // the last request: watched until asked + WatchFor
	target       Target
	timer        *time.Timer
}

// reading is one git reading of a checkout.
type reading struct {
	base, baseLabel, head, work string
	scopes                      map[string][]landing.DiffFile
	truncated                   bool
	commits                     []landing.Commit
	fingerprint                 string
	at                          time.Time
	baseChoices                 []BaseChoice
}

// BaseChoice is one base the human may pick for a main checkout.
type BaseChoice struct {
	Ref   string `json:"ref"` // "" (upstream), "session" or a ref
	Label string `json:"label"`
}

// New makes a Service. Call Run to follow job activity.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MinInterval <= 0 {
		opts.MinInterval = MinInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Service{opts: opts, entries: map[string]*entry{}, marked: map[string]bool{}}
}

// ---- Errors --------------------------------------------------------------------------------

// Error is a request the service refuses, with an HTTP-ish kind.
type Error struct {
	Kind    string // invalid | not_found | gone | stale
	Message string
}

func (e *Error) Error() string { return e.Message }

func fail(kind, format string, args ...any) error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// AsError returns err as an *Error when it is one.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// ---- Resolving and reading -----------------------------------------------------------------

// Ask names one subject: a job id, a project ref, or the orchestrator.
type Ask struct {
	Kind string
	Ref  string // job id or project ref
	Base string // a main checkout's base choice
}

func (s *Service) resolve(ctx context.Context, ask Ask) (Target, error) {
	var target Target
	var err error
	switch ask.Kind {
	case KindJob:
		target, err = s.opts.Resolver.Job(ctx, ask.Ref)
	case KindProject:
		target, err = s.opts.Resolver.Project(ctx, ask.Ref)
	case KindOrchestrator:
		target, err = s.opts.Resolver.Orchestrator(ctx)
	default:
		return Target{}, fail("invalid", "unknown kind %q", ask.Kind)
	}
	if err != nil {
		return Target{}, err
	}
	if ask.Base != "" && ask.Base != BaseSession && !landing.ValidRef(ask.Base) {
		return Target{}, fail("invalid", "%q is not a ref crowsnest will hand to git", ask.Base)
	}
	if target.Kind != KindJob {
		target.BaseRef = ask.Base
	}
	if target.Gone != "" {
		return target, fail("gone", "%s", target.Gone)
	}
	if target.Root == "" {
		return target, fail("not_found", "%s has no folder to read a diff from", firstNonEmpty(target.Label, target.Subject))
	}
	return target, nil
}

func (s *Service) entryFor(target Target) *entry {
	key := target.Subject + "\x00" + target.BaseRef
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		e = &entry{key: key, subject: target.Subject}
		s.entries[key] = e
	}
	e.target = target
	return e
}

// read returns the subject's reading, taking a new one when the last is older than the
// interval (or force asks and it is older than half of it).
func (s *Service) read(ctx context.Context, target Target, force, ask bool) (*reading, error) {
	e := s.entryFor(target)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := s.opts.Now()
	if ask {
		s.mu.Lock()
		e.asked = now
		s.mu.Unlock()
	}
	interval := s.opts.MinInterval
	if force {
		interval /= 2
	}
	if !e.at.IsZero() && now.Sub(e.at) < interval {
		return e.reading, e.err
	}
	previous := e.reading
	next, err := readCheckout(ctx, target, now)
	e.at = s.opts.Now()
	if err != nil {
		e.err = err
		return e.reading, err
	}
	e.reading, e.err = next, nil
	if previous != nil && previous.fingerprint != next.fingerprint {
		s.publish(target.Subject, next.fingerprint)
	}
	return next, nil
}

func (s *Service) publish(subject, revision string) {
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: EventType, Data: map[string]string{"subject": subject, "revision": revision}})
	}
}

// readCheckout takes one reading of target's checkout.
func readCheckout(ctx context.Context, target Target, now time.Time) (*reading, error) {
	root := target.Root
	if top, err := landing.Root(ctx, root); err != nil {
		return nil, fail("not_found", "%s is not in a git repository", root)
	} else if top != "" {
		root = top
	}
	r := &reading{head: landing.HeadTree(ctx, root), at: now, scopes: map[string][]landing.DiffFile{}}
	r.base, r.baseLabel, r.baseChoices = chooseBase(ctx, root, target)
	work, skipped, err := landing.WorkTree(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("reading the work tree: %w", err)
	}
	r.work = work
	compare := func(scope, from, to string) error {
		files, truncated, err := landing.Compare(ctx, root, from, to)
		if err != nil {
			return err
		}
		r.truncated = r.truncated || truncated
		r.scopes[scope] = files
		return nil
	}
	headOrEmpty := r.head
	if r.base != "" && r.head != "" {
		if err := compare(ScopeCommitted, r.base, r.head); err != nil {
			return nil, err
		}
		if r.commits, err = landing.CommitsBetween(ctx, root, r.base, r.head); err != nil {
			return nil, err
		}
	} else {
		r.scopes[ScopeCommitted] = []landing.DiffFile{}
		r.commits = []landing.Commit{}
	}
	if err := compare(ScopeUncommitted, headOrEmpty, work); err != nil {
		return nil, err
	}
	allFrom := r.base
	if allFrom == "" {
		allFrom = headOrEmpty
	}
	if err := compare(ScopeAll, allFrom, work); err != nil {
		return nil, err
	}
	for _, scope := range []string{ScopeUncommitted, ScopeAll} {
		r.scopes[scope] = append(r.scopes[scope], landing.SkippedFiles(skipped, r.scopes[scope])...)
	}
	sum := sha256.New()
	for _, scope := range []string{ScopeAll, ScopeCommitted, ScopeUncommitted} {
		for _, file := range r.scopes[scope] {
			fmt.Fprintf(sum, "%s %s %s\n", scope, file.Path, file.Hash)
		}
	}
	fmt.Fprintf(sum, "%s %s", r.base, r.head)
	r.fingerprint = hex.EncodeToString(sum.Sum(nil))[:16]
	return r, nil
}

// chooseBase is the commit the committed comparison starts from, with a label, and for a main
// checkout the choices the human may switch between.
func chooseBase(ctx context.Context, root string, target Target) (string, string, []BaseChoice) {
	if target.Kind == KindJob {
		if target.BaseBranch != "" {
			if base := landing.MergeBase(ctx, root, "refs/heads/"+target.BaseBranch); base != "" {
				return base, "where it left " + target.BaseBranch, nil
			}
		}
		if target.BaseCommit != "" && landing.ResolveCommit(ctx, root, target.BaseCommit) != "" {
			return landing.ResolveCommit(ctx, root, target.BaseCommit), "the commit its worktree started from", nil
		}
		if !target.Since.IsZero() {
			if base := landing.CommitAt(ctx, root, target.Since); base != "" {
				return base, "HEAD when the job started", nil
			}
		}
		return "", "", nil
	}
	choices := []BaseChoice{}
	upstream := landing.Upstream(ctx, root)
	fallback := landing.DefaultBranch(ctx, root)
	switch {
	case upstream != "":
		choices = append(choices, BaseChoice{Ref: BaseUpstream, Label: "upstream " + upstream})
	case fallback != "":
		choices = append(choices, BaseChoice{Ref: BaseUpstream, Label: "the default branch " + fallback})
	default:
		choices = append(choices, BaseChoice{Ref: BaseUpstream, Label: "HEAD (no upstream)"})
	}
	if !target.Since.IsZero() {
		choices = append(choices, BaseChoice{Ref: BaseSession, Label: "since the conversation started (" + target.Since.Local().Format("Jan 2 15:04") + ")"})
	}
	switch target.BaseRef {
	case BaseSession:
		if base := landing.CommitAt(ctx, root, target.Since); base != "" && !target.Since.IsZero() {
			return base, choices[len(choices)-1].Label, choices
		}
		return "", "nothing committed since the conversation started", choices
	case BaseUpstream:
		for _, ref := range []string{upstream, fallback} {
			if ref == "" {
				continue
			}
			if base := landing.MergeBase(ctx, root, ref); base != "" {
				return base, choices[0].Label, choices
			}
		}
		return "", choices[0].Label, choices
	default:
		if base := landing.MergeBase(ctx, root, target.BaseRef); base != "" {
			return base, target.BaseRef, choices
		}
		return "", target.BaseRef + " (not found)", choices
	}
}

// ---- The summary ---------------------------------------------------------------------------

// File is one file of a summary.
type File struct {
	landing.DiffFile
	Group string `json:"group"`
	// Churn files are collapsed by default; ChurnWhy says why (lockfile, generated, large…).
	Churn    bool   `json:"churn"`
	ChurnWhy string `json:"churn_why,omitempty"`
	// Viewed: a mark holds this file's current hash. Changed: a mark exists in this comparison
	// (or in all) but the file has changed since; SinceAvailable says the version seen can still
	// be compared with now.
	Viewed         bool       `json:"viewed"`
	ViewedAt       *time.Time `json:"viewed_at,omitempty"`
	Changed        bool       `json:"changed_since_viewed"`
	SinceAvailable bool       `json:"since_available"`
}

// ScopeInfo is one comparison's totals, for the scope switch.
type ScopeInfo struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Files   int    `json:"files"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
}

// Summary is GET /changes/…: the files of one comparison with their marks.
type Summary struct {
	Target
	Scope       string           `json:"scope"`
	Scopes      []ScopeInfo      `json:"scopes"`
	Base        string           `json:"base"`
	BaseLabel   string           `json:"base_label"`
	BaseRef     string           `json:"base_ref"`
	BaseChoices []BaseChoice     `json:"base_choices,omitempty"`
	Head        string           `json:"head"`
	Commits     []landing.Commit `json:"commits"`
	Files       []File           `json:"files"`
	Added       int              `json:"added"`
	Deleted     int              `json:"deleted"`
	Viewed      int              `json:"viewed"`
	Total       int              `json:"total"`
	Truncated   bool             `json:"truncated"`
	ComputedAt  time.Time        `json:"computed_at"`
	Revision    string           `json:"revision"`
}

// ValidScope reports whether scope is one of the four.
func ValidScope(scope string) bool {
	switch scope {
	case ScopeAll, ScopeCommitted, ScopeUncommitted, ScopeSinceViewed:
		return true
	}
	return false
}

var scopeLabels = map[string]string{
	ScopeAll: "All changes", ScopeCommitted: "Commits on this branch", ScopeUncommitted: "Uncommitted", ScopeSinceViewed: "Since you viewed",
}

// Summary reads (or reuses) the subject's diff and returns one comparison with its marks.
func (s *Service) Summary(ctx context.Context, ask Ask, scope string, force bool) (Summary, error) {
	if scope == "" {
		scope = ScopeAll
	}
	if !ValidScope(scope) {
		return Summary{}, fail("invalid", "unknown scope %q: give all, committed, uncommitted or since_viewed", scope)
	}
	target, err := s.resolve(ctx, ask)
	if err != nil {
		return Summary{}, err
	}
	r, err := s.read(ctx, target, force, true)
	if err != nil {
		return Summary{}, err
	}
	marks, err := s.opts.Store.ListDiffMarks(ctx, target.Subject)
	if err != nil {
		return Summary{}, err
	}
	return s.summarize(target, r, marks, scope), nil
}

func (s *Service) summarize(target Target, r *reading, marks []store.DiffMark, scope string) Summary {
	summary := Summary{Target: target, Scope: scope, Base: r.base, BaseLabel: r.baseLabel, BaseRef: target.BaseRef, BaseChoices: r.baseChoices,
		Head: r.head, Commits: r.commits, Files: []File{}, Truncated: r.truncated, ComputedAt: r.at, Revision: r.fingerprint}
	index := markIndex(marks)
	since := sinceFiles(r, index)
	for _, name := range []string{ScopeAll, ScopeCommitted, ScopeUncommitted, ScopeSinceViewed} {
		files := r.scopes[name]
		if name == ScopeSinceViewed {
			files = since
		}
		info := ScopeInfo{Name: name, Label: scopeLabels[name], Files: len(files)}
		for _, file := range files {
			info.Added += file.Added
			info.Deleted += file.Deleted
		}
		summary.Scopes = append(summary.Scopes, info)
	}
	files := r.scopes[scope]
	if scope == ScopeSinceViewed {
		files = since
	}
	for _, diff := range files {
		file := decorate(diff, index, scope)
		summary.Files = append(summary.Files, file)
		summary.Added += file.Added
		summary.Deleted += file.Deleted
		if file.Viewed {
			summary.Viewed++
		}
	}
	sort.SliceStable(summary.Files, func(i, j int) bool {
		a, b := summary.Files[i], summary.Files[j]
		if ra, rb := landing.GroupRank(a.Group), landing.GroupRank(b.Group); ra != rb {
			return ra < rb
		}
		return a.Path < b.Path
	})
	summary.Total = len(summary.Files)
	return summary
}

// markIndex groups marks by path.
func markIndex(marks []store.DiffMark) map[string][]store.DiffMark {
	index := map[string][]store.DiffMark{}
	for _, mark := range marks {
		if mark.Reviewer == reviewer {
			index[mark.Path] = append(index[mark.Path], mark)
		}
	}
	return index
}

// sinceFiles are the files of the all comparison that are not viewed: changed since a mark, or
// never marked.
func sinceFiles(r *reading, index map[string][]store.DiffMark) []landing.DiffFile {
	out := []landing.DiffFile{}
	for _, file := range r.scopes[ScopeAll] {
		if _, ok := viewedMark(index[file.Path], file.Hash); !ok {
			out = append(out, file)
		}
	}
	return out
}

func viewedMark(marks []store.DiffMark, hash string) (store.DiffMark, bool) {
	for _, mark := range marks {
		if mark.Hash == hash {
			return mark, true
		}
	}
	return store.DiffMark{}, false
}

// staleMark is the mark a changed file was last viewed under, in scope (or all, which
// since_viewed reads from), newest first.
func staleMark(marks []store.DiffMark, scope string) (store.DiffMark, bool) {
	if scope == ScopeSinceViewed {
		scope = ScopeAll
	}
	var best store.DiffMark
	found := false
	for _, mark := range marks {
		if mark.Scope != scope && mark.Scope != ScopeAll {
			continue
		}
		if !found || mark.ViewedAt.After(best.ViewedAt) || (mark.Scope == scope && best.Scope != scope) {
			best, found = mark, true
		}
	}
	return best, found
}

func decorate(diff landing.DiffFile, index map[string][]store.DiffMark, scope string) File {
	file := File{DiffFile: diff}
	file.Group, file.Churn = landing.Classify(diff.Path)
	if file.Churn {
		file.ChurnWhy = churnWhy(diff.Path)
	} else if diff.Added+diff.Deleted > largeChange {
		file.Churn, file.ChurnWhy = true, fmt.Sprintf("a large change (%d lines)", diff.Added+diff.Deleted)
	}
	if mark, ok := viewedMark(index[diff.Path], diff.Hash); ok {
		at := mark.ViewedAt
		file.Viewed, file.ViewedAt = true, &at
		return file
	}
	if mark, ok := staleMark(index[diff.Path], scope); ok {
		at := mark.ViewedAt
		file.Changed, file.ViewedAt = true, &at
		file.SinceAvailable = mark.NewBlob != "" && !strings.HasPrefix(mark.NewBlob, "stamp:") && !diff.TooLarge
	}
	return file
}

// churnWhy says why a churn file is collapsed.
func churnWhy(file string) string {
	lower := strings.ToLower(file)
	base := path.Base(lower)
	switch {
	case strings.HasSuffix(base, ".lock") || strings.HasSuffix(base, "-lock.json") || strings.HasSuffix(base, "-lock.yaml") || base == "go.sum" || base == "bun.lockb":
		return "a lockfile"
	case strings.Contains(lower, "__snapshots__") || strings.HasSuffix(base, ".snap"):
		return "snapshots"
	case strings.HasPrefix(lower, "vendor/"):
		return "vendored code"
	case strings.Contains(lower, "/dist/") || strings.HasSuffix(base, ".min.js"):
		return "build output"
	case strings.Contains(lower, "generated") || strings.HasSuffix(base, ".pb.go"):
		return "generated code"
	}
	return "an image or font"
}

// ---- One file ------------------------------------------------------------------------------

// FileAsk is one file's diff request.
type FileAsk struct {
	Path             string
	Scope            string
	Context          int
	IgnoreWhitespace bool
	// Since asks for the comparison from the version last viewed to now.
	Since bool
}

// FileView is one file's diff, with both sides' whole text when small enough (the browser
// highlights the whole file and expands context from it).
type FileView struct {
	Path     string `json:"path"`
	OldPath  string `json:"old_path,omitempty"`
	Status   string `json:"status"`
	Hash     string `json:"hash"`
	Diff     string `json:"diff"`
	Binary   bool   `json:"binary"`
	TooLarge bool   `json:"too_large"`
	Note     string `json:"note,omitempty"`
	Cut      bool   `json:"cut"`
	// OldText and NewText are the whole sides (nil when absent, binary or over the limit).
	OldText *string `json:"old_text"`
	NewText *string `json:"new_text"`
	// Since: this is the comparison from the version viewed at ViewedAt to now.
	Since    bool       `json:"since"`
	ViewedAt *time.Time `json:"viewed_at,omitempty"`
	Context  int        `json:"context"`
}

// File returns one file's diff in a comparison. Only files the comparison lists are served.
func (s *Service) File(ctx context.Context, ask Ask, fileAsk FileAsk) (FileView, error) {
	if fileAsk.Scope == "" {
		fileAsk.Scope = ScopeAll
	}
	if !ValidScope(fileAsk.Scope) {
		return FileView{}, fail("invalid", "unknown scope %q", fileAsk.Scope)
	}
	target, err := s.resolve(ctx, ask)
	if err != nil {
		return FileView{}, err
	}
	r, err := s.read(ctx, target, false, true)
	if err != nil {
		return FileView{}, err
	}
	lookup := fileAsk.Scope
	if lookup == ScopeSinceViewed {
		lookup = ScopeAll
	}
	var found *landing.DiffFile
	for i := range r.scopes[lookup] {
		if r.scopes[lookup][i].Path == fileAsk.Path {
			found = &r.scopes[lookup][i]
			break
		}
	}
	if found == nil {
		return FileView{}, fail("not_found", "%s is not one of the files in this diff", fileAsk.Path)
	}
	file := *found
	view := FileView{Path: file.Path, OldPath: file.OldPath, Status: file.Status, Hash: file.Hash, Binary: file.Binary, TooLarge: file.TooLarge,
		Note: file.Note, Context: fileAsk.Context}
	if file.TooLarge {
		return view, nil
	}
	root := target.Root
	if top, err := landing.Root(ctx, root); err == nil && top != "" {
		root = top
	}
	opts := landing.PatchOptions{Context: fileAsk.Context, IgnoreWhitespace: fileAsk.IgnoreWhitespace}
	oldBlob, newBlob := file.OldBlob, file.NewBlob
	if fileAsk.Since {
		marks, err := s.opts.Store.ListDiffMarks(ctx, target.Subject)
		if err != nil {
			return FileView{}, err
		}
		mark, ok := staleMark(markIndex(marks)[file.Path], fileAsk.Scope)
		switch {
		case !ok:
			return FileView{}, fail("not_found", "%s was never marked viewed here", file.Path)
		case mark.Hash == file.Hash:
			return FileView{}, fail("invalid", "%s has not changed since you viewed it", file.Path)
		case !landing.HasBlob(ctx, root, mark.NewBlob) && mark.NewBlob != landing.NullBlob:
			return FileView{}, fail("gone", "the version of %s you viewed is no longer in the repository", file.Path)
		}
		at := mark.ViewedAt
		view.Since, view.ViewedAt, oldBlob = true, &at, mark.NewBlob
		if newBlob == landing.NullBlob {
			view.Diff, view.Status = "", "deleted"
			return view, nil
		}
		if oldBlob == landing.NullBlob {
			view.Status = "added"
		} else {
			view.Status = "modified"
		}
		if oldBlob != landing.NullBlob {
			view.Diff, view.Binary, view.Cut, err = landing.BlobPatch(ctx, root, oldBlob, newBlob, file.Path, opts)
		} else {
			view.Diff, view.Binary, view.Cut, err = landing.Patch(ctx, root, "", r.work, file.Path, "", opts)
		}
	} else {
		from, to := scopeRange(r, fileAsk.Scope)
		view.Diff, view.Binary, view.Cut, err = landing.Patch(ctx, root, from, to, file.Path, file.OldPath, opts)
	}
	if err != nil {
		return FileView{}, err
	}
	view.Binary = view.Binary || file.Binary
	if !view.Binary {
		view.OldText = blobText(ctx, root, oldBlob)
		view.NewText = blobText(ctx, root, newBlob)
	}
	return view, nil
}

func blobText(ctx context.Context, root, blob string) *string {
	if blob == landing.NullBlob {
		empty := ""
		return &empty
	}
	text, err := landing.Blob(ctx, root, blob, landing.MaxBlobBytes)
	if err != nil {
		return nil
	}
	return &text
}

func scopeRange(r *reading, scope string) (string, string) {
	switch scope {
	case ScopeCommitted:
		return r.base, r.head
	case ScopeUncommitted:
		return r.head, r.work
	}
	if r.base != "" {
		return r.base, r.work
	}
	return r.head, r.work
}

// ---- Marks ---------------------------------------------------------------------------------

// MarkAsk is one file to mark: its path and the hash the browser showed.
type MarkAsk struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// Mark records Viewed on files, each only if its hash is still the file's hash in scope (409
// stale otherwise: the file changed after it was shown). An empty list marks every file.
func (s *Service) Mark(ctx context.Context, ask Ask, scope string, files []MarkAsk, all bool) (Summary, error) {
	if scope == "" {
		scope = ScopeAll
	}
	if !ValidScope(scope) {
		return Summary{}, fail("invalid", "unknown scope %q", scope)
	}
	target, err := s.resolve(ctx, ask)
	if err != nil {
		return Summary{}, err
	}
	r, err := s.read(ctx, target, false, true)
	if err != nil {
		return Summary{}, err
	}
	stored := scope
	if stored == ScopeSinceViewed {
		stored = ScopeAll
	}
	byPath := map[string]landing.DiffFile{}
	for _, file := range r.scopes[stored] {
		byPath[file.Path] = file
	}
	if all {
		files = files[:0]
		for _, file := range r.scopes[stored] {
			files = append(files, MarkAsk{Path: file.Path, Hash: file.Hash})
		}
	}
	marks := []store.DiffMark{}
	now := s.opts.Now().UTC()
	for _, want := range files {
		file, ok := byPath[want.Path]
		if !ok {
			return Summary{}, fail("not_found", "%s is not one of the files in this diff", want.Path)
		}
		if file.Hash != want.Hash {
			return Summary{}, fail("stale", "%s changed after it was shown; look at it again before marking it viewed", want.Path)
		}
		marks = append(marks, store.DiffMark{Subject: target.Subject, Reviewer: reviewer, Scope: stored, Path: file.Path, Hash: file.Hash,
			OldBlob: file.OldBlob, NewBlob: file.NewBlob, ViewedAt: now})
	}
	if err := s.opts.Store.PutDiffMarks(ctx, marks); err != nil {
		return Summary{}, err
	}
	s.noteMarked(target.Subject)
	s.publish(target.Subject, r.fingerprint+"-marks")
	return s.Summary(ctx, ask, scope, false)
}

// Unmark clears marks on paths in scope (every mark in it when paths is empty).
func (s *Service) Unmark(ctx context.Context, ask Ask, scope string, paths []string) (Summary, error) {
	if scope == "" {
		scope = ScopeAll
	}
	if !ValidScope(scope) {
		return Summary{}, fail("invalid", "unknown scope %q", scope)
	}
	target, err := s.resolve(ctx, ask)
	if err != nil {
		return Summary{}, err
	}
	scopes := []string{scope}
	if scope == ScopeSinceViewed || len(paths) > 0 {
		scopes = []string{ScopeAll, ScopeCommitted, ScopeUncommitted} // a file's tick goes wherever it was made
	}
	for _, name := range scopes {
		if _, err := s.opts.Store.DeleteDiffMarks(ctx, target.Subject, reviewer, name, paths); err != nil {
			return Summary{}, err
		}
	}
	s.publish(target.Subject, "unmarked")
	return s.Summary(ctx, ask, scope, false)
}

func (s *Service) noteMarked(subject string) {
	s.mu.Lock()
	s.marked[subject] = true
	s.mu.Unlock()
}

// Unviewed counts the files of a job's committed work (what Land merges) that are not viewed,
// with the total. A job with no checkout to read counts 0 of 0.
func (s *Service) Unviewed(ctx context.Context, jobID string) (int, int, error) {
	target, err := s.resolve(ctx, Ask{Kind: KindJob, Ref: jobID})
	if err != nil {
		if e, ok := AsError(err); ok && (e.Kind == "gone" || e.Kind == "not_found") {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	r, err := s.read(ctx, target, false, false)
	if err != nil {
		return 0, 0, err
	}
	marks, err := s.opts.Store.ListDiffMarks(ctx, target.Subject)
	if err != nil {
		return 0, 0, err
	}
	summary := s.summarize(target, r, marks, ScopeCommitted)
	return summary.Total - summary.Viewed, summary.Total, nil
}

// ---- Progress and a project's jobs ---------------------------------------------------------

// Progress is a job's viewed count from its last reading (no git is run for it).
type Progress struct {
	Viewed  int       `json:"viewed"`
	Total   int       `json:"total"`
	Added   int       `json:"added"`
	Deleted int       `json:"deleted"`
	At      time.Time `json:"at"`
}

// ProgressAll is every job subject's progress from the readings in memory, by job id.
func (s *Service) ProgressAll(ctx context.Context) (map[string]Progress, error) {
	s.mu.Lock()
	type pair struct {
		target Target
		r      *reading
	}
	pairs := []pair{}
	for _, e := range s.entries {
		if e.target.Kind == KindJob && e.reading != nil {
			pairs = append(pairs, pair{e.target, e.reading})
		}
	}
	s.mu.Unlock()
	out := map[string]Progress{}
	for _, p := range pairs {
		marks, err := s.opts.Store.ListDiffMarks(ctx, p.target.Subject)
		if err != nil {
			return nil, err
		}
		summary := s.summarize(p.target, p.r, marks, ScopeAll)
		out[p.target.JobID] = Progress{Viewed: summary.Viewed, Total: summary.Total, Added: summary.Added, Deleted: summary.Deleted, At: p.r.at}
	}
	return out, nil
}

// ProjectRow is one row of a project's Changes: a job (or the main checkout) with its diffstat.
type ProjectRow struct {
	Target
	Files   int       `json:"files"`
	Added   int       `json:"added"`
	Deleted int       `json:"deleted"`
	Viewed  int       `json:"viewed"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

// maxProjectRows bounds how many jobs a project's Changes reads.
const maxProjectRows = 20

// ProjectChanges lists a project's main checkout and its jobs that still have a checkout, each
// with its diffstat (all changes) and viewed count.
func (s *Service) ProjectChanges(ctx context.Context, ref string) ([]ProjectRow, error) {
	project, err := s.opts.Resolver.Project(ctx, ref)
	if err != nil {
		return nil, err
	}
	jobs, err := s.opts.Resolver.ProjectJobs(ctx, ref)
	if err != nil {
		return nil, err
	}
	targets := []Target{project}
	for _, job := range jobs {
		if job.Gone == "" && job.Root != "" && len(targets) <= maxProjectRows {
			targets = append(targets, job)
		}
	}
	rows := make([]ProjectRow, len(targets))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 2)
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target Target) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			row := ProjectRow{Target: target}
			r, err := s.read(ctx, target, false, false)
			if err != nil {
				row.Error = err.Error()
				rows[i] = row
				return
			}
			marks, _ := s.opts.Store.ListDiffMarks(ctx, target.Subject)
			summary := s.summarize(target, r, marks, ScopeAll)
			row.Files, row.Added, row.Deleted, row.Viewed, row.At = summary.Total, summary.Added, summary.Deleted, summary.Viewed, r.at
			rows[i] = row
		}(i, target)
	}
	wg.Wait()
	return rows, nil
}

// ---- Following activity --------------------------------------------------------------------

// Run follows job activity and the slow tick until ctx ends, reading watched subjects again, and
// removes the marks of retired jobs (at start, after a job event, and hourly).
func (s *Service) Run(ctx context.Context) {
	if subjects, err := s.opts.Store.DiffMarkSubjects(ctx); err == nil {
		for _, subject := range subjects {
			s.noteMarked(subject)
		}
	}
	s.sweep(ctx)
	var events <-chan hub.Event
	if s.opts.Hub != nil {
		sub := s.opts.Hub.Subscribe(hub.DefaultBuffer * 4)
		defer sub.Close()
		events = sub.Events()
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				events = nil // the hub closed this subscription; the tick still runs
				continue
			}
			s.onEvent(ctx, event)
		case <-ticker.C:
			s.refreshWatched(ctx, tick)
		case <-hourly.C:
			s.sweep(ctx)
		}
	}
}

// onEvent reacts to one hub event: a job's activity schedules a reading of its diff; a job that
// settled may retire its marks.
func (s *Service) onEvent(ctx context.Context, event hub.Event) {
	if event.Type != "job" {
		return
	}
	id := jobIDOf(event.Data)
	if id == "" {
		return
	}
	subject := id
	s.mu.Lock()
	marked := s.marked[subject]
	s.mu.Unlock()
	if marked && s.opts.Resolver.Retired(ctx, subject) {
		s.retire(ctx, subject)
		return
	}
	s.schedule(ctx, subject)
}

// jobIDOf reads the job id from a job hub event's data (jobs.HubEvent, or a map in tests).
func jobIDOf(data any) string {
	switch value := data.(type) {
	case interface{ HubJobID() string }:
		return value.HubJobID()
	case map[string]any:
		id, _ := value["id"].(string)
		return id
	case map[string]string:
		return value["id"]
	}
	return reflectID(data)
}

// schedule reads subject's watched entries again after the debounce.
func (s *Service) schedule(ctx context.Context, subject string) {
	now := s.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.subject != subject || now.Sub(e.asked) > WatchFor {
			continue
		}
		if e.timer != nil {
			continue // already due
		}
		target := e.target
		entry := e
		e.timer = time.AfterFunc(debounce, func() {
			s.mu.Lock()
			entry.timer = nil
			s.mu.Unlock()
			if _, err := s.read(ctx, target, false, false); err != nil {
				s.opts.Logger.Debug("reading a watched diff", "subject", target.Subject, "err", err)
			}
		})
	}
}

// refreshWatched reads every watched subject whose reading is older than age.
func (s *Service) refreshWatched(ctx context.Context, age time.Duration) {
	now := s.opts.Now()
	s.mu.Lock()
	due := []Target{}
	for _, e := range s.entries {
		if now.Sub(e.asked) <= WatchFor && now.Sub(e.at) >= age {
			due = append(due, e.target)
		}
	}
	s.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].Subject < due[j].Subject })
	for _, target := range due {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.read(ctx, target, false, false); err != nil {
			s.opts.Logger.Debug("reading a watched diff", "subject", target.Subject, "err", err)
		}
	}
}

// sweep retires the marks of every subject the resolver says is done with.
func (s *Service) sweep(ctx context.Context) {
	subjects, err := s.opts.Store.DiffMarkSubjects(ctx)
	if err != nil {
		return
	}
	for _, subject := range subjects {
		if s.opts.Resolver.Retired(ctx, subject) {
			s.retire(ctx, subject)
		}
	}
}

func (s *Service) retire(ctx context.Context, subject string) {
	if _, err := s.opts.Store.DeleteSubjectMarks(ctx, subject); err != nil {
		s.opts.Logger.Warn("removing a job's viewed marks", "subject", subject, "err", err)
		return
	}
	s.mu.Lock()
	delete(s.marked, subject)
	for key, e := range s.entries {
		if e.subject == subject {
			delete(s.entries, key)
		}
	}
	s.mu.Unlock()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
