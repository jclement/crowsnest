// The careful update of an agent's own state file (R9-C8, R9-S8). ~/.claude.json is Claude
// Code's whole state (hundreds of KB: every folder's answers, MCP approvals, onboarding), which
// every running Claude rewrites, so "read, change one key, rename over it" can silently lose a
// write a Claude made in between. The update here:
//
//   - takes the lock Claude Code itself takes for that file: a `<file>.lock` directory made with
//     mkdir (proper-lockfile, as Claude Code 2.1.332's saveConfigWithLock uses it; a lock older
//     than lockStale is abandoned, as proper-lockfile treats it). A Claude that saves while
//     crowsnest holds it waits; crowsnest waits for a Claude that holds it, and gives up loudly
//     after lockWait. Codex keeps no lock on config.toml, so there the lock only orders crowsnest's
//     own writers (and any other tool that honours the same convention);
//   - reads the file, computes the change, writes it to a temporary file beside it, then reads
//     the file again just before the rename and renames only if the bytes are unchanged, else
//     starts over (a writer that ignores the lock now loses nothing unless it writes inside that
//     last instant, between the comparison and the rename, which no file API can close);
//   - gives up with an error, writing nothing, when the file keeps changing;
//   - writes through a symbolic link to its target (a stow or chezmoi dotfile stays a link) and
//     keeps the target's mode;
//   - refuses anything that is not a regular file of a sane size, without blocking on a FIFO.

package agenttrust

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Timings of the lock (variables only so tests can shorten them).
var (
	// lockStale matches proper-lockfile's default: a lock directory untouched this long belongs
	// to a process that died holding it (Claude refreshes a held lock's mtime well inside it).
	lockStale = 10 * time.Second
	// lockWait is how long crowsnest waits for a live holder before giving up.
	lockWait = 15 * time.Second
)

const (
	// lockPoll is the wait between two tries at the lock.
	lockPoll = 50 * time.Millisecond
	// changeRetries is how many times an update starts over because the file changed under it.
	changeRetries = 5
	// configLimit caps an agent's state file read (~/.claude.json is about 1 MB on a busy machine).
	configLimit = 64 << 20
)

// errChangedUnderneath is an update that kept finding the file changed between its read and its
// rename.
var errChangedUnderneath = errors.New("the file kept changing while crowsnest tried to update it, so nothing was written; try again in a moment")

// writers orders crowsnest's own updates to one file within this process: the lock directory
// orders them across processes.
var writers sync.Mutex

// transform computes a file's new content from its current content; changed false writes nothing.
type transform func(original []byte) (updated []byte, changed bool, err error)

// updateFile applies change to the file at path under its lock, as described above. It reports
// whether it wrote. A missing file reads as empty and is created 0600.
func updateFile(path string, change transform) (bool, error) {
	writers.Lock()
	defer writers.Unlock()
	release, err := acquireLock(path + ".lock")
	if err != nil {
		return false, err
	}
	defer release()
	target, err := resolveTarget(path)
	if err != nil {
		return false, err
	}
	for range changeRetries {
		original, err := readConfig(target)
		if err != nil {
			return false, err
		}
		updated, changed, err := change(original)
		if err != nil || !changed {
			return false, err
		}
		wrote, err := replaceIfUnchanged(target, original, updated)
		if err != nil || wrote {
			return wrote, err
		}
	}
	return false, errChangedUnderneath
}

// replaceIfUnchanged writes updated beside target and renames it over target only if target still
// holds original. It reports false (and leaves target alone) when target changed meanwhile.
func replaceIfUnchanged(target string, original, updated []byte) (bool, error) {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".crowsnest-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(updated); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	again, err := readConfig(target)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(again, original) {
		return false, nil
	}
	return true, os.Rename(tmp.Name(), target)
}

// resolveTarget is the file an update writes: path itself, or what a symbolic link at path points
// to (so the link is kept). A missing file is written at path.
func resolveTarget(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		if _, lerr := os.Lstat(path); lerr == nil {
			return "", fmt.Errorf("%s is a link to a file that does not exist; fix the link first", path)
		}
		return path, nil
	}
	return resolved, err
}

// ReadConfig reads an agent's state file the safe way (bounded, no FIFO wait, through a link at
// path to its target): for a caller that only needs to look, such as "does Claude trust this
// folder", which runs on every project view (R9-S8). A missing file is empty.
func ReadConfig(path string) ([]byte, error) {
	target, err := resolveTarget(path)
	if err != nil {
		return nil, err
	}
	return readConfig(target)
}

// readConfig reads an agent's state file; a missing one is empty.
func readConfig(path string) ([]byte, error) {
	data, err := readSmall(path, configLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// acquireLock takes the lock directory at lockPath, waiting for a live holder up to lockWait and
// taking over one older than lockStale. The returned function releases it.
func acquireLock(lockPath string) (func(), error) {
	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("taking the lock %s: %w", lockPath, err)
		}
		if info, statErr := os.Lstat(lockPath); statErr == nil && info.IsDir() && time.Since(info.ModTime()) > lockStale {
			// A holder that died: proper-lockfile takes such a lock over the same way.
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another program has held %s for over %s (a running agent saving its config?), so nothing was written; try again in a moment", lockPath, lockWait)
		}
		time.Sleep(lockPoll)
	}
}
