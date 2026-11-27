// The filesystem the browser reads through. Every read the package makes goes through FS, so the
// tests can stand in a slow mount (a ReadDir that never returns) or an unreadable directory
// without root, and so it is plain from one interface that nothing here ever writes.

package browse

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// FS is the read-only view of the disk the browser uses.
type FS interface {
	// ReadDir lists a directory's entries (names and types, no stat of each).
	ReadDir(name string) ([]fs.DirEntry, error)
	// Stat follows links; Lstat does not.
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	// EvalSymlinks resolves every link in name.
	EvalSymlinks(name string) (string, error)
	// ReadFile reads at most limit bytes of a small metadata file (.git/HEAD, a mise.toml).
	// Its bytes are only ever parsed for facts, never returned to a client.
	ReadFile(name string, limit int64) ([]byte, error)
	// Access is nil when the directory may be listed and entered (read and search permission).
	Access(name string) error
}

// OS is the real filesystem.
type OS struct{}

// MaxReadDir is the most entries one listing reads from a directory (R7-S12): a directory of a
// million names is read in batches up to this, not whole and sorted before the cap applies.
const MaxReadDir = 5000

// ReadDir implements FS: at most MaxReadDir entries, in the directory's own order.
func (OS) ReadDir(name string) ([]fs.DirEntry, error) {
	dir, err := os.Open(name) //nolint:gosec // a directory inside an allowed root; names only
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	var out []fs.DirEntry
	for len(out) < MaxReadDir {
		batch, err := dir.ReadDir(min(500, MaxReadDir-len(out)))
		out = append(out, batch...)
		if errors.Is(err, io.EOF) || len(batch) == 0 {
			break
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Stat implements FS.
func (OS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

// Lstat implements FS.
func (OS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }

// EvalSymlinks implements FS.
func (OS) EvalSymlinks(name string) (string, error) { return filepath.EvalSymlinks(name) }

// ReadFile implements FS. It is the one way a metadata file is read (R7-S10): opened without
// following a link in its last part (a .git/HEAD linked to a file elsewhere is not read) and
// without blocking (a FIFO planted as .git/HEAD would otherwise hold open(2), and a slot, for
// good), then checked to be a regular file and read through a limit.
func (OS) ReadFile(name string, limit int64) ([]byte, error) {
	return ReadMetadata(name, limit, false)
}

// ReadMetadata reads at most limit bytes of a small regular file without blocking on a FIFO or a
// device. followLink allows a link as the last part (a dotfiles-managed ~/.claude.json).
func ReadMetadata(name string, limit int64, followLink bool) ([]byte, error) {
	flags := os.O_RDONLY | unix.O_NONBLOCK
	if !followLink {
		flags |= unix.O_NOFOLLOW
	}
	file, err := os.OpenFile(name, flags, 0) //nolint:gosec // a metadata file inside an allowed root, parsed and never served
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	return io.ReadAll(io.LimitReader(file, limit))
}

// Access implements FS.
func (OS) Access(name string) error {
	if err := unix.Access(name, unix.R_OK|unix.X_OK); err != nil {
		return &fs.PathError{Op: "access", Path: name, Err: err}
	}
	return nil
}

// isPermission reports whether err is a permission refusal (EACCES or EPERM).
func isPermission(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM)
}
