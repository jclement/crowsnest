// Package instructions owns the instruction layers crowsnest injects into the agents it starts
// (DESIGN.md 21.3). Crowsnest never edits the user's AGENTS.md, CLAUDE.md or skills: it adds
// layered markdown through the prompt file it already writes for every agent. The layers, in
// order, are global (every agent), role (the four built-in roles, editable here and resettable
// to their built-in text), project, and job (the brief, which travels as brief.md and is only
// listed in the manifest, never inlined).
//
// Where a role's text comes from, strongest first: a file in ~/.config/crowsnest/roles/<name>.md
// (shown read-only, "from file"), then the text saved in the app, then the built-in. The file
// wins because it is the older, explicit mechanism: someone who keeps roles in a dotfiles repo
// must not have them silently shadowed by an edit made on a phone.
//
// The store keeps the human's text and its revisions (internal/store/instructions.go); this
// package validates, layers, composes, and records what each agent was started with.
package instructions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/store"
)

// Scopes of a layer, as instruction_layers.scope stores them (plus job, which is never stored).
const (
	ScopeGlobal  = "global"
	ScopeRole    = "role"
	ScopeProject = "project"
	ScopeJob     = "job"
)

// Sources of a layer's text.
const (
	SourceNone    = "none"    // nothing set: the layer adds nothing
	SourceBuiltin = "builtin" // a role's text embedded in the binary
	SourceDB      = "db"      // saved in the app
	SourceFile    = "file"    // ~/.config/crowsnest/roles/<name>.md
	SourceBrief   = "brief"   // the job layer: brief.md
)

// MaxBodyBytes caps one layer's text, the same cap as a role override file.
const MaxBodyBytes = 32 << 10

// DefaultKeepRevisions is how many revisions of each layer are kept.
const DefaultKeepRevisions = 20

// EventType is the SSE event published when a layer changes: {at}.
const EventType = "instructions"

// Error kinds the HTTP layer maps to statuses.
var (
	ErrInvalid  = errors.New("invalid")   // 400
	ErrNotFound = errors.New("not found") // 404
	ErrReadOnly = errors.New("read-only") // 409: the role comes from a file
)

// userError carries a message meant for the human alongside the kind it wraps.
type userError struct {
	kind    error
	message string
}

func (e *userError) Error() string { return e.message }
func (e *userError) Unwrap() error { return e.kind }

func failure(kind error, format string, args ...any) error {
	return &userError{kind: kind, message: fmt.Sprintf(format, args...)}
}

// Options configure a Service. Store is required.
type Options struct {
	Store *store.Store
	// RolesDir is ~/.config/crowsnest/roles, the override files jobs.LoadRoles reads. It is
	// re-read on every listing and composition, so an edited file applies at the next start.
	RolesDir string
	// Hub, when set, receives an "instructions" event on every change.
	Hub    *hub.Hub
	Logger *slog.Logger
	Now    func() time.Time
	// KeepRevisions is how many revisions of each layer are kept; 0 means DefaultKeepRevisions.
	KeepRevisions int
}

// Service is the instruction layers. Build it with New.
type Service struct {
	opts Options
	// standingStale reports whether a standing agent's running Claude was started with other
	// crowsnest files (its prompt template, hooks, settings, MCP config) than a start would
	// write now (orch freshness.go); set once the agents exist.
	standingStale atomic.Pointer[func(subject string) bool]
}

// SetStandingStale wires the check of a standing agent's own files ("orchestrator",
// "lead:<slug>") into the "instructions changed since start" view.
func (s *Service) SetStandingStale(stale func(subject string) bool) { s.standingStale.Store(&stale) }

// New returns a Service with the defaults filled in.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.KeepRevisions <= 0 {
		opts.KeepRevisions = DefaultKeepRevisions
	}
	return &Service{opts: opts}
}

// ---- Resolving one layer -------------------------------------------------------------------

// resolved is one layer as it applies right now.
type resolved struct {
	Source    string
	Text      string // the text in effect ("" for none)
	Saved     string // the app's saved text, even when a file wins over it
	Builtin   string // a role's built-in text
	Revision  int    // the saved revision (0 when nothing is saved)
	UpdatedAt time.Time
	FilePath  string
	// FileHash is the hex SHA-256 of a role file's text; FileSeen says the human marked that
	// exact text read (R5-S14). A file nobody has looked at, or one changed since, is shown loudly.
	FileHash string
	FileSeen bool
}

// roleSet reads the roles as a job start would see them: the embedded ones with the override
// directory on top. Problems with single files were logged at startup by the jobs wiring.
func (s *Service) roleSet() *jobs.Roles {
	roles, _ := jobs.LoadRoles(s.opts.RolesDir)
	return roles
}

// resolve finds the text in effect for one layer. A role that exists nowhere is ErrNotFound.
func (s *Service) resolve(ctx context.Context, roles *jobs.Roles, scope, key string) (resolved, error) {
	var out resolved
	saved, err := s.opts.Store.GetInstructionLayer(ctx, scope, key)
	switch {
	case err == nil:
		out.Saved, out.Revision, out.UpdatedAt = saved.BodyMD, saved.Revision, saved.UpdatedAt
	case !errors.Is(err, store.ErrNotFound):
		return resolved{}, err
	}
	if scope != ScopeRole {
		out.Source, out.Text = SourceNone, ""
		if err == nil {
			out.Source, out.Text = SourceDB, out.Saved
		}
		return out, nil
	}
	out.Builtin, _ = jobs.EmbeddedRoles().Text(key)
	text, exists := roles.Text(key)
	switch {
	case roles.FromFile(key):
		out.Source, out.Text, out.FilePath = SourceFile, text, filepath.Join(s.opts.RolesDir, key+".md")
		out.FileHash = fileHash(text)
		if seen, err := s.opts.Store.GetKV(ctx, fileSeenKey(key)); err == nil && seen == out.FileHash {
			out.FileSeen = true
		}
	case err == nil:
		out.Source, out.Text = SourceDB, out.Saved
	case exists:
		out.Source, out.Text = SourceBuiltin, text
	default:
		return resolved{}, failure(ErrNotFound, "There is no role %q.", key)
	}
	return out, nil
}

// ---- Role files the human has read (R5-S14) -----------------------------------------------

// fileSeenKey is the kv row holding the hash of a role file's text the human marked read.
func fileSeenKey(role string) string { return "instructions.file-seen." + role }

func fileHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ErrFileChanged is returned when a role file changed after the human looked at it.
var ErrFileChanged = errors.New("the role file changed since you looked at it")

// MarkFileSeen records that the human read role's file as it is now. hash names the text they
// were shown; a file that has changed since is refused, so they read the new text first. A role
// file outranks the app's text, and any agent that can write ~/.config/crowsnest/roles can change
// it: without this, an edit there reached every later agent with nothing to say so.
func (s *Service) MarkFileSeen(ctx context.Context, role, hash string) error {
	layer, err := s.resolve(ctx, s.roleSet(), ScopeRole, role)
	if err != nil {
		return err
	}
	if layer.Source != SourceFile {
		return failure(ErrNotFound, "Role %q does not come from a file.", role)
	}
	if hash == "" || hash != layer.FileHash {
		return ErrFileChanged
	}
	if err := s.opts.Store.SetKV(ctx, fileSeenKey(role), hash); err != nil {
		return err
	}
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: EventType, Data: map[string]any{"at": s.opts.Now().UTC()}})
	}
	return nil
}

// ---- Validation ----------------------------------------------------------------------------

// checkTarget validates a scope and key for a write: global takes no key, a role must be an
// existing role, a project an existing project's id. It returns the role set it read.
func (s *Service) checkTarget(ctx context.Context, scope, key string) (*jobs.Roles, error) {
	roles := s.roleSet()
	switch scope {
	case ScopeGlobal:
		if key != "" {
			return nil, failure(ErrInvalid, "The global layer has no key.")
		}
	case ScopeRole:
		if !jobs.ValidRoleName(key) {
			return nil, failure(ErrInvalid, "A role name is lower-case letters, digits, - or _ (at most 32).")
		}
		if !roles.Has(key) {
			return nil, failure(ErrNotFound, "There is no role %q.", key)
		}
	case ScopeProject:
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != key {
			return nil, failure(ErrInvalid, "A project layer's key is the project's id, a positive number.")
		}
		if _, err := s.opts.Store.GetProject(ctx, id); errors.Is(err, store.ErrNotFound) {
			return nil, failure(ErrNotFound, "There is no project %d.", id)
		} else if err != nil {
			return nil, err
		}
	default:
		return nil, failure(ErrInvalid, "The scope is global, role or project.")
	}
	return roles, nil
}

// ---- Changing layers -----------------------------------------------------------------------

// Save makes body the layer's text, as a new revision. A role that comes from a file refuses
// (ErrReadOnly): the file would win anyway. An empty global or project layer is cleared (a
// revision marked deleted); an empty role is refused, since a role with no text is never meant:
// Reset brings back the built-in.
func (s *Service) Save(ctx context.Context, scope, key, body string) error {
	roles, err := s.checkTarget(ctx, scope, key)
	if err != nil {
		return err
	}
	if len(body) > MaxBodyBytes {
		return failure(ErrInvalid, "The text is longer than %d KB; keep standing instructions short and point to files for detail.", MaxBodyBytes>>10)
	}
	if scope == ScopeRole {
		if roles.FromFile(key) {
			return failure(ErrReadOnly, "The %s role comes from %s, which wins over text saved here; edit or remove that file.",
				key, filepath.Join(s.opts.RolesDir, key+".md"))
		}
		if strings.TrimSpace(body) == "" {
			return failure(ErrInvalid, "A role needs some text; use Reset to go back to the built-in.")
		}
	}
	deleted := strings.TrimSpace(body) == ""
	if deleted {
		body = ""
	}
	return s.write(ctx, scope, key, body, deleted)
}

// Reset removes a role's saved text so its built-in (or its file) applies again. Nothing saved
// is not an error: there is nothing to reset, and no revision is written.
func (s *Service) Reset(ctx context.Context, role string) error {
	if _, err := s.checkTarget(ctx, ScopeRole, role); err != nil {
		return err
	}
	if _, err := s.opts.Store.GetInstructionLayer(ctx, ScopeRole, role); errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	return s.write(ctx, ScopeRole, role, "", true)
}

// Restore makes an old revision current again, as a new revision (history only grows). A
// revision that was a reset or a clear resets or clears again.
func (s *Service) Restore(ctx context.Context, scope, key string, revision int) error {
	if _, err := s.checkTarget(ctx, scope, key); err != nil {
		return err
	}
	old, err := s.opts.Store.GetInstructionRevision(ctx, scope, key, revision)
	if errors.Is(err, store.ErrNotFound) {
		return failure(ErrNotFound, "Revision %d of that layer is not kept (the newest %d are).", revision, s.opts.KeepRevisions)
	}
	if err != nil {
		return err
	}
	if old.Deleted {
		if scope == ScopeRole {
			return s.Reset(ctx, key)
		}
		return s.Save(ctx, scope, key, "")
	}
	return s.Save(ctx, scope, key, old.BodyMD)
}

// write records the revision and tells the browsers.
func (s *Service) write(ctx context.Context, scope, key, body string, deleted bool) error {
	if _, err := s.opts.Store.SaveInstructionLayer(ctx, scope, key, body, deleted, s.opts.Now(), s.opts.KeepRevisions); err != nil {
		return err
	}
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: EventType, Data: map[string]any{"at": s.opts.Now().UTC()}})
	}
	return nil
}

func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
