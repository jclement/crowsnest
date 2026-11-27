// The instruction files Claude reports loading, and the per-agent view that puts them beside
// crowsnest's own layers (DESIGN.md 21.3).
//
// Claude Code's InstructionsLoaded hook fires when a CLAUDE.md or .claude/rules/*.md file is
// loaded, at session start and lazily as the agent opens files. crowsnest records the path,
// the memory type, the reason and the size (from a stat: the file is never read), and serves
// only that list: the user's own files are theirs, and their text never leaves the machine
// through crowsnest. Skills and slash commands are not listed: no hook input carries them and
// Claude Code offers no cheap way to ask.

package instructions

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
)

// LoadedInput is the InstructionsLoaded hook's input, the fields crowsnest reads (the hooks
// reference also lists globs, trigger_file_path and parent_file_path).
type LoadedInput struct {
	SessionID  string `json:"session_id"`
	Cwd        string `json:"cwd"`
	FilePath   string `json:"file_path"`
	MemoryType string `json:"memory_type"` // User | Project | Local | Managed
	LoadReason string `json:"load_reason"` // session_start | nested_traversal | path_glob_match | include | compact
}

// Bounds on what a hook may store: these strings are shown in the browser.
const (
	maxPathRunes  = 1024
	maxLabelRunes = 32
	maxIDRunes    = 128
)

// RecordLoaded stores one loaded file for subject. The size comes from a stat (0 when the file
// cannot be stat'ed); a relative path is taken against the hook's cwd. An input without a path
// is ignored.
func (s *Service) RecordLoaded(ctx context.Context, subject string, input LoadedInput) error {
	path := input.FilePath
	if path == "" || !ValidSubject(subject) {
		return nil
	}
	if !filepath.IsAbs(path) && filepath.IsAbs(input.Cwd) {
		path = filepath.Join(input.Cwd, path)
	}
	path = filepath.Clean(path)
	var size int64
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		size = info.Size()
	}
	return s.opts.Store.RecordLoadedInstruction(ctx, store.LoadedInstruction{
		Subject: subject, SessionID: termtext.Cap(termtext.Clean(input.SessionID), maxIDRunes), At: s.opts.Now(),
		Path:       termtext.Cap(termtext.Clean(path), maxPathRunes),
		MemoryType: termtext.Cap(termtext.Clean(input.MemoryType), maxLabelRunes),
		LoadReason: termtext.Cap(termtext.Clean(input.LoadReason), maxLabelRunes),
		Size:       size,
	})
}

// LoadedFile is one file an agent loaded, by path only.
type LoadedFile struct {
	Path       string    `json:"path"`
	MemoryType string    `json:"memory_type"`
	LoadReason string    `json:"load_reason"`
	Size       int64     `json:"size"`
	At         time.Time `json:"at"`
}

// SkillsNote says why skills and slash commands are not listed.
const SkillsNote = "Skills and slash commands are not listed: Claude Code reports them to no hook, and has no cheap way to ask."

// AgentInstructions is GET /agents-instructions/{subject}: what crowsnest started the agent
// with, whether that has changed since, and the instruction files Claude loaded in its latest
// session.
type AgentInstructions struct {
	Subject    string          `json:"subject"`
	StartedAt  *time.Time      `json:"started_at"` // nil: no start recorded
	Hash       string          `json:"hash"`
	Layers     []ManifestLayer `json:"layers"`
	Changed    bool            `json:"changed"`
	Changes    []Change        `json:"changes"`
	Loaded     []LoadedFile    `json:"loaded"`
	SkillsNote string          `json:"skills_note"`
}

// Agent returns the view of one agent's instructions.
func (s *Service) Agent(ctx context.Context, subject string) (AgentInstructions, error) {
	if !ValidSubject(subject) {
		return AgentInstructions{}, failure(ErrInvalid, "An agent is j_<n>, orchestrator or lead:<slug>.")
	}
	view := AgentInstructions{Subject: subject, Layers: []ManifestLayer{}, Changes: []Change{}, Loaded: []LoadedFile{}, SkillsNote: SkillsNote}
	record, layers, err := s.Recorded(ctx, subject)
	switch {
	case err == nil:
		started := record.StartedAt
		view.StartedAt, view.Hash = &started, record.Hash
		if layers != nil {
			view.Layers = layers
		}
		if view.Changed, view.Changes, err = s.diff(ctx, layers); err != nil {
			return AgentInstructions{}, err
		}
	case !isNotFound(err):
		return AgentInstructions{}, err
	}
	if stale := s.standingStale.Load(); stale != nil && (*stale)(subject) {
		// crowsnest's own files for it changed (DESIGN.md 7.5): the same banner and Restart.
		view.Changed = true
		view.Changes = append(view.Changes, Change{Layer: "crowsnest's prompt file, hooks and settings", Scope: "crowsnest", NowSource: "crowsnest"})
	}
	loaded, err := s.opts.Store.ListLoadedInstructions(ctx, subject)
	if err != nil {
		return AgentInstructions{}, err
	}
	for _, file := range loaded {
		view.Loaded = append(view.Loaded, LoadedFile{Path: file.Path, MemoryType: file.MemoryType, LoadReason: file.LoadReason, Size: file.Size, At: file.At})
	}
	return view, nil
}
