// Files an agent names (`ask` with attachments, `done` with attachments) are read by the server,
// which runs as the same user but may be reachable from agents that should not read everything
// that user can. So an agent can attach only a file inside its own working directory or its job
// directory: never an arbitrary path through crowsnest (DESIGN.md 8.6).
//
// The check is os.Root, not string prefixes: the file is opened relative to a handle on the root
// directory, and the kernel walk refuses `..` above it and any symbolic link whose target leaves
// it (os.Root follows links only while they stay inside). So a link planted inside the working
// directory that points at ~/.ssh/id_ed25519 is refused, and so is a path whose directory is
// swapped for such a link between a check and the open: there is no separate check to race.

package attach

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutside refuses a file that is not inside any of the caller's roots.
var ErrOutside = errors.New("outside the agent's directories")

// openConfined opens path for reading if it lies inside one of roots, and returns the file and
// its size. path must be absolute (the agent-side client makes a relative path absolute against
// its own working directory before sending it). Only regular files are accepted.
func openConfined(path string, roots []string, maxBytes int64) (*os.File, int64, error) {
	if !filepath.IsAbs(path) {
		return nil, 0, fmt.Errorf("%s is not an absolute path", path)
	}
	if hasDotDot(path) {
		return nil, 0, fmt.Errorf("%s: %w (a path with .. is refused)", path, ErrOutside)
	}
	for _, root := range roots {
		for _, rel := range relativeTo(root, path) {
			file, err := openInRoot(root, rel)
			if errors.Is(err, errNotThisRoot) {
				continue
			}
			if err != nil {
				return nil, 0, fmt.Errorf("%s: %w", path, err)
			}
			info, err := file.Stat()
			if err != nil {
				_ = file.Close()
				return nil, 0, fmt.Errorf("%s: %w", path, err)
			}
			if !info.Mode().IsRegular() {
				_ = file.Close()
				return nil, 0, fmt.Errorf("%s is not a regular file", path)
			}
			if info.Size() > maxBytes {
				_ = file.Close()
				return nil, 0, fmt.Errorf("%s: %w", path, &TooLargeError{Limit: maxBytes})
			}
			return file, info.Size(), nil
		}
	}
	return nil, 0, fmt.Errorf("%s: %w", path, ErrOutside)
}

// errNotThisRoot means the path is not under this root (try the next).
var errNotThisRoot = errors.New("not under this root")

// openInRoot opens rel under root through os.Root. An escape (a link out, ..) is errNotThisRoot,
// so another root that does hold the file still gets its turn; a missing file is reported as
// such, because that is what the agent needs to hear.
func openInRoot(root, rel string) (*os.File, error) {
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, errNotThisRoot
	}
	defer func() { _ = handle.Close() }()
	file, err := handle.Open(rel)
	switch {
	case err == nil:
		return file, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, errors.New("no such file")
	case errors.Is(err, fs.ErrPermission):
		return nil, errors.New("not readable")
	}
	return nil, errNotThisRoot // os.Root's "path escapes from parent" and the like
}

// relativeTo returns the ways path can be written relative to root: as written, and with both
// resolved (an agent may write /tmp/x where the root was recorded as /private/tmp/x, or the
// reverse). Paths outside root give nothing.
func relativeTo(root, path string) []string {
	var out []string
	add := func(base, target string) {
		rel, err := filepath.Rel(base, target)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return
		}
		for _, seen := range out {
			if seen == rel {
				return
			}
		}
		out = append(out, rel)
	}
	add(filepath.Clean(root), filepath.Clean(path))
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	resolvedPath, pathErr := filepath.EvalSymlinks(path)
	if rootErr == nil && pathErr == nil {
		add(resolvedRoot, resolvedPath)
	}
	if rootErr == nil {
		// The file's own directory may be the link (resolve it, keep the name), or the root may
		// be one: try the path unresolved against the resolved root too.
		if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			add(resolvedRoot, filepath.Join(dir, filepath.Base(path)))
		}
	}
	return out
}

// hasDotDot reports a `..` element in the path as written. Cleaning would remove it, but an
// agent that writes one is reaching out of where it said it was, and the refusal says so.
func hasDotDot(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}
