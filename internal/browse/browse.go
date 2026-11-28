// Package browse is the read-only directory browser behind the web app's folder picker
// (DESIGN.md 4.23, 10): one directory's entries at a time, the facts about a chosen folder, and
// the favourites the picker starts from.
//
// The rules that matter:
//   - Nothing here writes, and no file's contents are ever returned. Directory names, types and
//     modification times go out; the few metadata files read for facts (.git/HEAD, .git/config,
//     a mise.toml, Claude's trust list) are parsed here and only conclusions leave.
//   - Only paths under a root may be listed. The roots are the user's home, every --browse-root,
//     and every directory a project or the remembered list already uses. `/` is a root only when
//     --browse-root names it. A path is cleaned, its links resolved, and the resolved path checked
//     against the resolved roots, so `..` and a symlink cannot lead out; a link inside a root
//     that points outside every root is listed but marked, and cannot be entered.
//   - Every listing is bounded: a cap on entries ("N more"), a deadline (a slow or dead network
//     mount answers "took too long" rather than hanging a request), and a cap on listings in
//     flight, so reads stuck on a dead mount cannot pile up without limit.
//   - A permission refusal on one entry is a property of that entry, not a failure of the list.
package browse

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Defaults for Options.
const (
	// DefaultTimeout bounds one listing or facts call. Local disks answer in milliseconds; a
	// network mount that has not answered in this long is treated as unavailable.
	DefaultTimeout = 3 * time.Second
	// DefaultMaxEntries caps the entries of one listing; the rest are counted as "more". A picker
	// row is ~44 px on a phone, so 500 rows is far more than anyone scrolls through; the filter
	// box narrows it instead.
	DefaultMaxEntries = 500
	// DefaultMaxInFlight caps listings running at once. Each one that hangs on a dead mount holds
	// a slot until the kernel gives up, so this also caps the goroutines such a mount can strand.
	DefaultMaxInFlight = 4
	// maxPathBytes refuses absurd input before it reaches the filesystem.
	maxPathBytes = 4096
	// busyWait is how long a request waits for a free slot before answering busy.
	busyWait = time.Second
)

// Errors a listing or facts call returns; the HTTP layer maps each to its status.
var (
	ErrInvalid    = errors.New("not an absolute path")
	ErrOutside    = errors.New("outside the browsable roots")
	ErrNotFound   = errors.New("no such directory")
	ErrNotDir     = errors.New("not a directory")
	ErrUnreadable = errors.New("permission denied")
	ErrTimeout    = errors.New("the directory took too long to read")
	ErrBusy       = errors.New("too many listings in flight")
)

// Place kinds: where a root or favourite came from.
const (
	KindHome          = "home"
	KindRoot          = "root" // --browse-root
	KindProject       = "project"
	KindRemembered    = "remembered"
	KindRecent        = "recent" // a herdr workspace's directory
	KindProjectParent = "project-parent"
	KindFavourite     = "favourite" // ~/Developer, ~/Work
)

// Place is a directory with a label and where it came from: a root, a favourite or both.
type Place struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	// Git is set on favourites: the directory holds a .git.
	Git bool `json:"git,omitempty"`
	// Resolved is, for a project, the folder as it resolved when the project was made (its
	// RootDir): the root is used only while it still resolves there (R9-S11).
	Resolved string `json:"-"`
}

// Options configure a Service. Home is required.
type Options struct {
	// Home is the user's home directory: always a root, and what ~ expands to.
	Home string
	// Extra are the --browse-root directories ("/" allowed only here).
	Extra []string
	// Sources lists the other places, read on every call so a new project is browsable at once:
	// projects and remembered directories (which become roots), workspace directories and
	// projects' parents (favourites only).
	Sources func(ctx context.Context) []Place
	// CheckCommand is the check crowsnest would run in a directory ("" for none); Trusted
	// reports whether Claude trusts a directory (ok false: not knowable cheaply). Both optional,
	// and both run inside the facts call's deadline.
	CheckCommand func(dir string) string
	Trusted      func(dir string) (trusted, ok bool)
	FS           FS
	Timeout      time.Duration
	MaxEntries   int
	MaxInFlight  int
}

// Service answers listings and facts. Safe for concurrent use.
type Service struct {
	opts  Options
	slots chan struct{}
	// busyWait is busyWait unless a test shortens it.
	busyWait time.Duration
}

// New returns a Service with the defaults filled in.
func New(opts Options) *Service {
	if opts.FS == nil {
		opts.FS = OS{}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = DefaultMaxEntries
	}
	if opts.MaxInFlight <= 0 {
		opts.MaxInFlight = DefaultMaxInFlight
	}
	return &Service{opts: opts, slots: make(chan struct{}, opts.MaxInFlight), busyWait: busyWait}
}

// Home is the home directory ~ expands to.
func (s *Service) Home() string { return s.opts.Home }

// ---- Roots ----------------------------------------------------------------------------------

// root is an allowed tree: as configured and with its links resolved.
type root struct {
	Place
	resolved string
}

// roots returns the allowed trees. A root that cannot be resolved (gone, unreadable) is left
// out; `/` is accepted only from --browse-root.
func (s *Service) roots(ctx context.Context) []root {
	var places []Place
	if s.opts.Home != "" {
		places = append(places, Place{Path: s.opts.Home, Label: "Home", Kind: KindHome})
	}
	for _, extra := range s.opts.Extra {
		places = append(places, Place{Path: extra, Label: extra, Kind: KindRoot})
	}
	if s.opts.Sources != nil {
		for _, place := range s.opts.Sources(ctx) {
			if place.Kind == KindProject || place.Kind == KindRemembered {
				places = append(places, place)
			}
		}
	}
	var out []root
	seen := map[string]bool{}
	homeResolved := ""
	if s.opts.Home != "" {
		homeResolved, _ = s.opts.FS.EvalSymlinks(s.opts.Home)
	}
	for _, place := range places {
		cleaned, err := s.clean(place.Path)
		if err != nil {
			continue
		}
		if cleaned == "/" && place.Kind != KindRoot {
			continue
		}
		resolved, err := s.opts.FS.EvalSymlinks(cleaned)
		if err != nil || (resolved == "/" && place.Kind != KindRoot) || seen[resolved] {
			continue
		}
		if !s.safeSourceRoot(place, cleaned, resolved, homeResolved) {
			continue
		}
		seen[resolved] = true
		place.Path = cleaned
		out = append(out, root{Place: place, resolved: resolved})
	}
	return out
}

// safeSourceRoot applies the rules a root that comes from crowsnest's own records (a project, a
// remembered directory) must meet, beyond those of --browse-root (R7-S11):
//   - a remembered directory is stored resolved, precisely so that a link swapped in later
//     cannot widen it: it must still resolve to itself (an agent that replaces ~/Work/p with a
//     link to /etc does not make /etc a root);
//   - a project's folder must still resolve where it did when the project was made (R9-S11: an
//     agent that replaces its project's directory with a link to /etc does not make /etc a root);
//   - none may be the home directory's ancestor (a directory above home once approved does not
//     make every user's home browsable).
func (s *Service) safeSourceRoot(place Place, cleaned, resolved, homeResolved string) bool {
	if place.Kind != KindProject && place.Kind != KindRemembered {
		return true
	}
	if place.Kind == KindRemembered && resolved != cleaned {
		return false
	}
	if place.Kind == KindProject && place.Resolved != "" && resolved != place.Resolved {
		return false
	}
	return homeResolved == "" || resolved == homeResolved || !within(homeResolved, resolved)
}

// Roots lists the allowed trees as the picker shows them (their paths as configured), under the
// deadline and the in-flight cap like every read.
func (s *Service) Roots(ctx context.Context) ([]Place, error) {
	return bounded(ctx, s, func(time.Time) ([]Place, error) { return s.rootPlaces(ctx), nil })
}

func (s *Service) rootPlaces(ctx context.Context) []Place {
	roots := s.roots(ctx)
	out := make([]Place, len(roots))
	for i, r := range roots {
		out[i] = r.Place
		if out[i].Label == "" {
			out[i].Label = s.label(r.Path)
		}
	}
	return out
}

// rootOf is the root holding resolved, if any.
func rootOf(roots []root, resolved string) (root, bool) {
	for _, candidate := range roots {
		if within(resolved, candidate.resolved) {
			return candidate, true
		}
	}
	return root{}, false
}

// within reports whether path is dir or inside it. Both are clean absolute paths.
func within(path, dir string) bool {
	if path == dir || dir == "/" {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// clean expands a leading ~, refuses a relative or absurd path and returns it cleaned.
func (s *Service) clean(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > maxPathBytes || strings.ContainsRune(path, 0) {
		return "", ErrInvalid
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if s.opts.Home == "" {
			return "", ErrInvalid
		}
		path = filepath.Join(s.opts.Home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		return "", ErrInvalid
	}
	return filepath.Clean(path), nil
}

// admit cleans path, resolves it and checks it against the roots. It returns the cleaned path,
// the resolved one and its root. A path that does not exist is ErrNotFound only when its
// nearest existing ancestor is inside a root (so the answer says nothing about the rest of the
// disk); otherwise ErrOutside.
func (s *Service) admit(ctx context.Context, path string) (cleaned, resolved string, holder root, err error) {
	cleaned, err = s.clean(path)
	if err != nil {
		return "", "", root{}, err
	}
	roots := s.roots(ctx)
	resolved, err = s.opts.FS.EvalSymlinks(cleaned)
	if err != nil {
		if isPermission(err) {
			return "", "", root{}, ErrOutside
		}
		// ENOTDIR (/etc/passwd/x) is answered as a missing path (R7-S12): otherwise a 500 here
		// against a 403 for a missing outside path tells whether an outside path is a file.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			for parent := filepath.Dir(cleaned); ; parent = filepath.Dir(parent) {
				if parentResolved, parentErr := s.opts.FS.EvalSymlinks(parent); parentErr == nil {
					if _, ok := rootOf(roots, parentResolved); ok {
						return "", "", root{}, ErrNotFound
					}
					return "", "", root{}, ErrOutside
				}
				if filepath.Dir(parent) == parent {
					return "", "", root{}, ErrOutside
				}
			}
		}
		return "", "", root{}, err
	}
	holder, ok := rootOf(roots, resolved)
	if !ok {
		return "", "", root{}, ErrOutside
	}
	return cleaned, resolved, holder, nil
}

// ---- Running a call under its bounds ---------------------------------------------------------

// bounded runs fn on its own goroutine with the service's deadline and an in-flight slot. When
// the deadline passes first the caller gets ErrTimeout and fn keeps its slot until it returns:
// a read stuck in the kernel cannot be cancelled, only waited out.
func bounded[T any](ctx context.Context, s *Service, fn func(deadline time.Time) (T, error)) (T, error) {
	var zero T
	wait := time.NewTimer(s.busyWait)
	defer wait.Stop()
	select {
	case s.slots <- struct{}{}:
	case <-wait.C:
		return zero, ErrBusy
	case <-ctx.Done():
		return zero, ctx.Err()
	}
	deadline := time.Now().Add(s.opts.Timeout)
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-s.slots }()
		value, err := fn(deadline)
		done <- result{value, err}
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case got := <-done:
		return got.value, got.err
	case <-timer.C:
		return zero, ErrTimeout
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// ---- Places: favourites and roots in one bounded call (R7-C10) -------------------------------

// Places is what the picker starts from: the favourites and the roots, both read inside one
// bounded call, so a mount that gets stuck between them cannot strand a request outside the
// deadline and the in-flight cap.
type Places struct {
	Favourites []Place
	Roots      []Place
}

// Places reads the favourites and the roots under one deadline and one slot.
func (s *Service) Places(ctx context.Context) (Places, error) {
	return bounded(ctx, s, func(time.Time) (Places, error) {
		return Places{Favourites: s.favourites(ctx), Roots: s.rootPlaces(ctx)}, nil
	})
}

// ---- Favourites -----------------------------------------------------------------------------

// Favourites are the places the picker offers before anything is typed: home, ~/Developer and
// ~/Work when they exist, the roots, projects' parents, and remembered and recent directories.
// Only places inside a root that exist are listed, each once.
func (s *Service) Favourites(ctx context.Context) ([]Place, error) {
	return bounded(ctx, s, func(time.Time) ([]Place, error) { return s.favourites(ctx), nil })
}

func (s *Service) favourites(ctx context.Context) []Place {
	var candidates []Place
	if s.opts.Home != "" {
		candidates = append(candidates, Place{Path: s.opts.Home, Label: "Home", Kind: KindHome})
		for _, name := range []string{"Developer", "Work"} {
			candidates = append(candidates, Place{Path: filepath.Join(s.opts.Home, name), Label: "~/" + name, Kind: KindFavourite})
		}
	}
	for _, extra := range s.opts.Extra {
		candidates = append(candidates, Place{Path: extra, Label: extra, Kind: KindRoot})
	}
	if s.opts.Sources != nil {
		candidates = append(candidates, s.opts.Sources(ctx)...)
	}
	roots := s.roots(ctx)
	out := []Place{}
	seen := map[string]bool{}
	for _, place := range candidates {
		cleaned, err := s.clean(place.Path)
		if err != nil || seen[cleaned] {
			continue
		}
		resolved, err := s.opts.FS.EvalSymlinks(cleaned)
		if err != nil {
			continue
		}
		if info, statErr := s.opts.FS.Stat(resolved); statErr != nil || !info.IsDir() {
			continue
		}
		if _, ok := rootOf(roots, resolved); !ok {
			continue
		}
		seen[cleaned] = true
		place.Path = cleaned
		if place.Label == "" {
			place.Label = s.label(cleaned)
		}
		place.Git = s.isGit(cleaned)
		out = append(out, place)
	}
	return out
}

// label is a path for display: ~ for the home directory.
func (s *Service) label(path string) string {
	if s.opts.Home != "" {
		if path == s.opts.Home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(path, s.opts.Home+string(filepath.Separator)); ok {
			return "~/" + rest
		}
	}
	return path
}

// isGit reports whether dir holds a .git (a directory, or a worktree's file).
func (s *Service) isGit(dir string) bool {
	_, err := s.opts.FS.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// sortEntries puts directories (and links out of the roots, whose kind is not said) before
// files, each by name without case.
func sortEntries(entries []Entry) {
	slices.SortStableFunc(entries, func(a, b Entry) int {
		aDir, bDir := a.Dir || a.Outside, b.Dir || b.Outside
		if aDir != bDir {
			if aDir {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Name, b.Name))
	})
}
