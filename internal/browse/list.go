// One directory's listing: the entries (directories first), the breadcrumb of its parents and
// what was left out (hidden entries, files, past the cap).

package browse

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// ListOptions choose what a listing includes.
type ListOptions struct {
	// Files includes files (for orientation; never their contents). Off, they are only counted.
	Files bool
	// Hidden includes entries whose name starts with a dot. Off, they are only counted.
	Hidden bool
}

// Listing is one directory as the picker shows it.
type Listing struct {
	// Path is the directory as navigated (cleaned, ~ expanded); Resolved has its links resolved.
	Path     string `json:"path"`
	Resolved string `json:"resolved"`
	// Label is Path with ~ for the home directory.
	Label string `json:"label"`
	// Root is the root the directory is under (its Path as configured).
	Root Place `json:"root"`
	// Parent is the directory one level up when that is browsable too, else "".
	Parent string `json:"parent"`
	// Crumbs are Path's ancestors from / down to Path itself.
	Crumbs  []Crumb `json:"crumbs"`
	Entries []Entry `json:"entries"`
	// More counts entries past the cap; HiddenCount and FileCount those left out by ListOptions.
	More        int `json:"more"`
	HiddenCount int `json:"hidden_count"`
	FileCount   int `json:"file_count"`
	// Partial is set when the deadline came while entries were being described: the names are
	// all there, some details (git, mtime) may be missing.
	Partial bool `json:"partial"`
	// Git is set when the directory itself holds a .git.
	Git bool `json:"git"`
}

// Crumb is one step of the breadcrumb.
type Crumb struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Browsable is false above the root: shown for orientation, not a link.
	Browsable bool `json:"browsable"`
}

// Entry is one directory entry.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Dir is set for a directory, or a link to one.
	Dir bool `json:"dir"`
	// Git is set for a directory holding a .git.
	Git     bool `json:"git"`
	Symlink bool `json:"symlink"`
	// Target is where a link resolves to; Outside is set when that is outside every root, and
	// such an entry cannot be entered or chosen.
	Target  string `json:"target,omitempty"`
	Outside bool   `json:"outside,omitempty"`
	Hidden  bool   `json:"hidden"`
	// Modified is the entry's modification time (a link's target's), zero when unknown.
	Modified time.Time `json:"mtime"`
	// Readable is false for a directory that cannot be listed or entered; Error says why.
	Readable bool   `json:"readable"`
	Error    string `json:"error,omitempty"`
}

// List returns one directory's listing. Errors are the package's Err values.
func (s *Service) List(ctx context.Context, path string, opts ListOptions) (Listing, error) {
	return bounded(ctx, s, func(deadline time.Time) (Listing, error) {
		cleaned, resolved, holder, err := s.admit(ctx, path)
		if err != nil {
			return Listing{}, err
		}
		info, err := s.opts.FS.Stat(resolved)
		if err != nil {
			return Listing{}, classify(err)
		}
		if !info.IsDir() {
			return Listing{}, ErrNotDir
		}
		dirents, err := s.opts.FS.ReadDir(resolved)
		if err != nil {
			return Listing{}, classify(err)
		}

		roots := s.roots(ctx)
		listing := Listing{
			Path: cleaned, Resolved: resolved, Label: s.label(cleaned), Root: holder.Place,
			Crumbs: s.crumbs(cleaned, roots), Entries: []Entry{}, Git: s.isGit(resolved),
		}
		// Only the first MaxReadDir names were read from a bigger directory (R7-S12).
		listing.Partial = len(dirents) >= MaxReadDir
		if parent := filepath.Dir(cleaned); parent != cleaned && s.browsable(parent, roots) {
			listing.Parent = parent
		}
		entries := s.entries(cleaned, dirents, opts, roots, deadline, &listing)
		sortEntries(entries)
		if len(entries) > s.opts.MaxEntries {
			listing.More = len(entries) - s.opts.MaxEntries
			entries = entries[:s.opts.MaxEntries]
		}
		for i := range entries {
			if time.Now().After(deadline) {
				listing.Partial = true
				break
			}
			s.describe(&entries[i])
		}
		listing.Entries = entries
		return listing, nil
	})
}

// entries turns dirents into entries with only what sorting needs (name, dir-ness, links
// resolved), counting what the options leave out. The costlier details come after the cap.
func (s *Service) entries(dir string, dirents []fs.DirEntry, opts ListOptions, roots []root, deadline time.Time, listing *Listing) []Entry {
	out := make([]Entry, 0, len(dirents))
	for _, dirent := range dirents {
		name := dirent.Name()
		entry := Entry{Name: name, Path: filepath.Join(dir, name), Hidden: strings.HasPrefix(name, "."), Readable: true}
		entry.Dir = dirent.IsDir()
		if dirent.Type()&fs.ModeSymlink != 0 {
			entry.Symlink = true
			if time.Now().Before(deadline) {
				s.followLink(&entry, roots)
			} else {
				listing.Partial = true
			}
		}
		if entry.Hidden && !opts.Hidden {
			listing.HiddenCount++
			continue
		}
		// A link out of the roots is listed in both modes (marked, and not enterable): whether it
		// points at a folder is not said (R7-S12), so it cannot be sorted by that.
		if !entry.Dir && !entry.Outside && !opts.Files {
			listing.FileCount++
			continue
		}
		out = append(out, entry)
	}
	return out
}

// followLink resolves a link entry: where it goes, whether that is a directory, and whether it
// stays inside the roots. A dangling link is a file with an error.
func (s *Service) followLink(entry *Entry, roots []root) {
	target, err := s.opts.FS.EvalSymlinks(entry.Path)
	if err != nil {
		entry.Error = "a link to nothing"
		entry.Readable = false
		return
	}
	if _, ok := rootOf(roots, target); !ok {
		// Outside every root (R7-S12): neither where it points nor whether that exists is said,
		// and nothing past the root is looked at.
		entry.Outside, entry.Dir = true, false
		return
	}
	entry.Target = target
	if info, err := s.opts.FS.Stat(target); err == nil {
		entry.Dir = info.IsDir()
	}
}

// describe fills the details of a kept entry: modification time, git, readability. A link out
// of the roots gets none of them: nothing is read past the root.
func (s *Service) describe(entry *Entry) {
	if entry.Outside {
		entry.Readable = false
		entry.Error = "a link out of the browsable folders"
		return
	}
	if entry.Error != "" {
		return
	}
	stat := s.opts.FS.Lstat
	if entry.Symlink {
		stat = s.opts.FS.Stat
	}
	if info, err := stat(entry.Path); err == nil {
		entry.Modified = info.ModTime().UTC()
	}
	if !entry.Dir {
		return
	}
	if err := s.opts.FS.Access(entry.Path); err != nil {
		entry.Readable = false
		entry.Error = "permission denied"
		return
	}
	entry.Git = s.isGit(entry.Path)
}

// crumbs are path's ancestors from / down, each marked browsable or not.
func (s *Service) crumbs(path string, roots []root) []Crumb {
	var chain []string
	for current := path; ; current = filepath.Dir(current) {
		chain = append(chain, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	out := make([]Crumb, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		name := filepath.Base(chain[i])
		if chain[i] == s.opts.Home {
			name = "~"
		}
		out = append(out, Crumb{Name: name, Path: chain[i], Browsable: s.browsable(chain[i], roots)})
	}
	return out
}

// browsable reports whether path resolves inside a root.
func (s *Service) browsable(path string, roots []root) bool {
	resolved, err := s.opts.FS.EvalSymlinks(path)
	if err != nil {
		return false
	}
	_, ok := rootOf(roots, resolved)
	return ok
}

// classify maps a filesystem error to the package's errors.
func classify(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case isPermission(err):
		return ErrUnreadable
	}
	return err
}
