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

// ReadDir implements FS.
func (OS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }

// Stat implements FS.
func (OS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

// Lstat implements FS.
func (OS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }

// EvalSymlinks implements FS.
func (OS) EvalSymlinks(name string) (string, error) { return filepath.EvalSymlinks(name) }

// ReadFile implements FS.
func (OS) ReadFile(name string, limit int64) ([]byte, error) {
	file, err := os.Open(name) //nolint:gosec // a metadata file inside an allowed root, parsed and never served
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
