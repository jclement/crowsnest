// Package projects is crowsnest's projects (DESIGN.md 21.1): a project is a repository's main
// checkout with a name, defaults for the jobs given in it, its own instruction layer
// (internal/instructions) and, optionally, a standing lead agent (leads.go).
//
// The rules that matter:
//   - A project's directory is resolved like a job's (absolute, cleaned, an existing directory)
//     and recorded twice: as given (`dir`, what the human sees and the lead runs in) and with
//     its links resolved (`root_dir`, what jobs are matched against). `/`, `$HOME` and any
//     directory holding `$HOME` are refused: a project there would claim every job.
//   - Two live projects never share a root, and a root inside another live project's root is
//     refused too, so a job's directory names at most one project (ForDir).
//   - Projects are archived, never deleted: a job's project_id always names a row.
package projects

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
)

// Lead start policies: when a project's lead starts without a press of "Start lead".
const (
	LeadManual = "manual" // only from Start lead
	LeadOnOpen = "open"   // when the project is opened in the app, or given a job
	LeadOnJob  = "job"    // when the project is given a job for its lead
)

// Limits on a project's fields.
const (
	maxNameRunes        = 60
	maxDescriptionBytes = 2000
)

var (
	slugPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	rolePattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	modelPattern = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]{1,64}$`)
	// Claude Code's modes; "manual" is the current name of "default" (both are taken).
	permissionModes = map[string]bool{"": true, "default": true, "manual": true, "auto": true, "acceptEdits": true, "plan": true, "bypassPermissions": true, "dontAsk": true}
)

// Project is a project as the API shows it.
type Project struct {
	ID             int64      `json:"id"`
	Slug           string     `json:"slug"`
	Name           string     `json:"name"`
	Dir            string     `json:"dir"`
	RootDir        string     `json:"root_dir"`
	Description    string     `json:"description"`
	DefaultRole    string     `json:"default_role"`
	DefaultEngine  string     `json:"default_engine"`
	DefaultModel   string     `json:"default_model"`
	DefaultIsolate *bool      `json:"default_isolate"` // nil: the role's default
	PermissionMode string     `json:"permission_mode"`
	LeadStart      string     `json:"lead_start"`
	Archived       bool       `json:"archived"`
	ArchivedAt     *time.Time `json:"archived_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Input is what the human sends to create or edit a project. Pointers are "leave as it is" on
// an edit; on a create, Name and Dir are required.
type Input struct {
	Name           *string `json:"name"`
	Slug           *string `json:"slug"` // create only; derived from the name when empty
	Dir            *string `json:"dir"`
	Description    *string `json:"description"`
	DefaultRole    *string `json:"default_role"`
	DefaultEngine  *string `json:"default_engine"`
	DefaultModel   *string `json:"default_model"`
	DefaultIsolate *int    `json:"default_isolate"` // -1 role default, 0 no, 1 yes
	PermissionMode *string `json:"permission_mode"`
	LeadStart      *string `json:"lead_start"`
	Archived       *bool   `json:"archived"`
}

// ValidationError is a refusal worded for the human.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// ErrNotFound is returned for a project id or slug that does not exist.
var ErrNotFound = errors.New("no such project")

// RoleChecker says whether a role exists (jobs.Roles.Has).
type RoleChecker func(role string) bool

// Options configure a Service.
type Options struct {
	Store *store.Store
	Hub   *hub.Hub // nil: no `project` events
	Roles RoleChecker
	Home  string // $HOME, refused as a project root (and its ancestors)
	Now   func() time.Time
}

// Service holds the projects. Lookups by directory are served from a cache refreshed on every
// write, because every job creation asks.
type Service struct {
	opts  Options
	mu    sync.Mutex
	cache []Project // live projects, refreshed on write
	ready bool
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Home == "" {
		opts.Home, _ = os.UserHomeDir()
	}
	return &Service{opts: opts}
}

// ---- Reading ------------------------------------------------------------------------------

// List returns the projects by name; archived ones only when asked.
func (s *Service) List(ctx context.Context, includeArchived bool) ([]Project, error) {
	records, err := s.opts.Store.ListProjects(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(records))
	for _, record := range records {
		out = append(out, fromRecord(record))
	}
	return out, nil
}

// Get returns a project by id, or by slug when ref is not a number ("p_3" also works).
func (s *Service) Get(ctx context.Context, ref string) (Project, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "p_")
	var record store.ProjectRecord
	var err error
	if id, parseErr := strconv.ParseInt(ref, 10, 64); parseErr == nil {
		record, err = s.opts.Store.GetProject(ctx, id)
	} else {
		record, err = s.opts.Store.GetProjectBySlug(ctx, ref)
	}
	if errors.Is(err, store.ErrNotFound) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return fromRecord(record), nil
}

// ByID returns a project by id.
func (s *Service) ByID(ctx context.Context, id int64) (Project, error) {
	return s.Get(ctx, strconv.FormatInt(id, 10))
}

// ForDir returns the live project whose root holds dir (after resolving dir's links), if any.
// A job given with that directory joins it.
func (s *Service) ForDir(ctx context.Context, dir string) (Project, bool) {
	if dir == "" {
		return Project{}, false
	}
	resolved := resolve(dir)
	for _, project := range s.live(ctx) {
		if within(resolved, project.RootDir) {
			return project, true
		}
	}
	return Project{}, false
}

func (s *Service) live(ctx context.Context) []Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		s.refreshLocked(ctx)
	}
	return s.cache
}

func (s *Service) refreshLocked(ctx context.Context) {
	projects, err := s.List(ctx, false)
	if err != nil {
		return // a failed read keeps the old cache and is retried on the next lookup
	}
	s.cache, s.ready = projects, true
}

func (s *Service) invalidate(ctx context.Context) {
	s.mu.Lock()
	s.ready = false
	s.refreshLocked(ctx)
	s.mu.Unlock()
}

// ---- Writing ------------------------------------------------------------------------------

// Create validates and stores a new project.
func (s *Service) Create(ctx context.Context, input Input) (Project, error) {
	if input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		return Project{}, invalid("a project needs a name")
	}
	if input.Dir == nil || strings.TrimSpace(*input.Dir) == "" {
		return Project{}, invalid("a project needs a directory: the repository's main checkout")
	}
	now := s.opts.Now()
	record := store.ProjectRecord{DefaultIsolate: -1, LeadStart: LeadManual, CreatedAt: now, UpdatedAt: now}
	slug := ""
	if input.Slug != nil {
		slug = strings.TrimSpace(*input.Slug)
	}
	if slug == "" {
		slug = Slugify(*input.Name)
	}
	if !slugPattern.MatchString(slug) {
		return Project{}, invalid("the short name %q must be lower-case letters, digits and '-', at most 32", slug)
	}
	record.Slug = slug
	if err := s.apply(ctx, &record, input); err != nil {
		return Project{}, err
	}
	id, err := s.opts.Store.InsertProject(ctx, record)
	if errors.Is(err, store.ErrConflict) {
		return Project{}, invalid("a project named %q already exists, or another live project uses %s", slug, record.Dir)
	}
	if err != nil {
		return Project{}, err
	}
	record.ID = id
	s.changed(ctx, id, "created")
	return fromRecord(record), nil
}

// Update applies an edit (and archiving or un-archiving) to a project.
func (s *Service) Update(ctx context.Context, id int64, input Input) (Project, error) {
	record, err := s.opts.Store.GetProject(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	if input.Slug != nil && strings.TrimSpace(*input.Slug) != record.Slug {
		return Project{}, invalid("a project's short name cannot change: it names its lead agent")
	}
	if err := s.apply(ctx, &record, input); err != nil {
		return Project{}, err
	}
	record.UpdatedAt = s.opts.Now()
	if err := s.opts.Store.UpdateProject(ctx, record); errors.Is(err, store.ErrConflict) {
		return Project{}, invalid("another live project already uses %s", record.Dir)
	} else if err != nil {
		return Project{}, err
	}
	s.changed(ctx, id, "updated")
	return fromRecord(record), nil
}

// apply validates every field the input sets onto record.
func (s *Service) apply(ctx context.Context, record *store.ProjectRecord, input Input) error {
	if input.Name != nil {
		name := strings.Join(strings.Fields(*input.Name), " ")
		if name == "" || len([]rune(name)) > maxNameRunes {
			return invalid("a project name is 1 to %d characters", maxNameRunes)
		}
		record.Name = name
	}
	if input.Description != nil {
		if len(*input.Description) > maxDescriptionBytes {
			return invalid("the description is at most %d bytes", maxDescriptionBytes)
		}
		record.Description = strings.TrimSpace(*input.Description)
	}
	if err := applyDefaults(record, input, s.opts.Roles); err != nil {
		return err
	}
	if input.Archived != nil {
		record.ArchivedAt = time.Time{}
		if *input.Archived {
			record.ArchivedAt = s.opts.Now()
		}
	}
	if input.Dir != nil || input.Archived != nil {
		dir := record.Dir
		if input.Dir != nil {
			dir = *input.Dir
		}
		return s.applyDir(ctx, record, dir)
	}
	return nil
}

// applyDefaults validates the job defaults and the lead policy.
func applyDefaults(record *store.ProjectRecord, input Input, roles RoleChecker) error {
	if input.DefaultRole != nil {
		role := strings.TrimSpace(*input.DefaultRole)
		if role != "" && (!rolePattern.MatchString(role) || (roles != nil && !roles(role))) {
			return invalid("unknown role %q", role)
		}
		record.DefaultRole = role
	}
	if input.DefaultEngine != nil {
		engine := strings.TrimSpace(*input.DefaultEngine)
		if engine != "" && engine != "claude" && engine != "codex" {
			return invalid("the engine is claude or codex, not %q", engine)
		}
		record.DefaultEngine = engine
	}
	if input.DefaultModel != nil {
		model := strings.TrimSpace(*input.DefaultModel)
		if model != "" && !modelPattern.MatchString(model) {
			return invalid("%q is not a model name", model)
		}
		record.DefaultModel = model
	}
	if input.DefaultIsolate != nil {
		if *input.DefaultIsolate < -1 || *input.DefaultIsolate > 1 {
			return invalid("default_isolate is -1 (the role's default), 0 or 1")
		}
		record.DefaultIsolate = *input.DefaultIsolate
	}
	if input.PermissionMode != nil {
		if !permissionModes[*input.PermissionMode] {
			return invalid("unknown permission mode %q", *input.PermissionMode)
		}
		record.PermissionMode = *input.PermissionMode
	}
	if input.LeadStart != nil {
		switch *input.LeadStart {
		case LeadManual, LeadOnOpen, LeadOnJob:
			record.LeadStart = *input.LeadStart
		default:
			return invalid("lead_start is manual, open or job")
		}
	}
	return nil
}

// applyDir checks the directory and that no other live project's root overlaps it (an
// archived project checks nothing: it claims no jobs).
func (s *Service) applyDir(ctx context.Context, record *store.ProjectRecord, dir string) error {
	cleaned, resolved, err := CheckDir(dir, s.opts.Home)
	if err != nil {
		return err
	}
	record.Dir, record.RootDir = cleaned, resolved
	if !record.ArchivedAt.IsZero() {
		return nil
	}
	others, err := s.opts.Store.ListProjects(ctx, false)
	if err != nil {
		return err
	}
	for _, other := range others {
		if other.ID == record.ID {
			continue
		}
		if within(resolved, other.RootDir) || within(other.RootDir, resolved) {
			return invalid("%s overlaps project %q (%s): a job's directory must name one project", cleaned, other.Name, other.Dir)
		}
	}
	return nil
}

func (s *Service) changed(ctx context.Context, id int64, change string) {
	s.invalidate(ctx)
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: "project", Data: map[string]any{"id": id, "change": change}})
	}
}

// ---- Directories ----------------------------------------------------------------------------

// CheckDir validates a project directory and returns it cleaned (with ~ expanded) and with its
// links resolved. It must be an existing directory, and not /, $HOME or an ancestor of $HOME.
func CheckDir(dir, home string) (cleaned, resolved string, err error) {
	dir = strings.TrimSpace(dir)
	if strings.HasPrefix(dir, "~/") || dir == "~" {
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		return "", "", invalid("the directory must be an absolute path, not %q", dir)
	}
	cleaned = filepath.Clean(dir)
	info, statErr := os.Stat(cleaned)
	if statErr != nil || !info.IsDir() {
		return "", "", invalid("%s is not a directory on this machine", cleaned)
	}
	resolved = resolve(cleaned)
	homeResolved := resolve(home)
	for _, refused := range []string{"/", home, homeResolved} {
		if refused == "" {
			continue
		}
		if resolved == refused || cleaned == refused || within(homeResolved, resolved) {
			return "", "", invalid("%s is too wide for a project (it would claim every job under it); pick the repository's directory", cleaned)
		}
	}
	return cleaned, resolved, nil
}

// resolve returns path with its links resolved, or path itself when that fails.
func resolve(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// within reports whether path is root or inside it.
func within(path, root string) bool {
	if root == "" {
		return false
	}
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// Slugify makes a short name from a project name: lower case, runs of anything else made '-',
// at most 32 characters ("project" when nothing is left).
func Slugify(name string) string {
	var out strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
			dash = false
		case !dash && out.Len() > 0:
			out.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(out.String(), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	if slug == "" {
		return "project"
	}
	return slug
}

func fromRecord(r store.ProjectRecord) Project {
	project := Project{
		ID: r.ID, Slug: r.Slug, Name: r.Name, Dir: r.Dir, RootDir: r.RootDir, Description: r.Description,
		DefaultRole: r.DefaultRole, DefaultEngine: r.DefaultEngine, DefaultModel: r.DefaultModel,
		PermissionMode: r.PermissionMode, LeadStart: r.LeadStart, Archived: !r.ArchivedAt.IsZero(),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.DefaultIsolate >= 0 {
		isolate := r.DefaultIsolate == 1
		project.DefaultIsolate = &isolate
	}
	if !r.ArchivedAt.IsZero() {
		at := r.ArchivedAt
		project.ArchivedAt = &at
	}
	return project
}

// Counts are a project's job counts for the board's switcher and the Projects view.
type Counts struct {
	NeedsYou int `json:"needs_you"`
	Running  int `json:"running"`
	Total    int `json:"total"`
}

// JobCounts returns job counts per project id (0 is Unfiled).
func (s *Service) JobCounts(ctx context.Context) (map[int64]Counts, error) {
	raw, err := s.opts.Store.CountJobsByProject(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Counts, len(raw))
	for id, c := range raw {
		out[id] = Counts{NeedsYou: c.NeedsYou, Running: c.Running, Total: c.Total}
	}
	return out, nil
}
