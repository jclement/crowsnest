// An isolated copy of a commit for crowsnest's own check (DESIGN.md 6.15, R7-C01, R9-C6, R9-C7).
//
// The default check runs in the job's own worktree, which has the dependencies and caches a check
// needs, but which the job's agent (or anything it left running) can change while the check runs.
// A project can instead ask for its check to run in a temporary detached worktree of exactly the
// pinned commit: made here, with the protective configuration of git.go, every configured clean,
// smudge and process filter switched off (so no repository-chosen program runs while the files are
// written), Git LFS's smudge skipped, the repository's own attributes not read (attr.tree is the
// empty tree, so no eol, ident or working-tree-encoding conversion changes the bytes) and sparse
// checkout off, and removed afterwards. What the copy does not have, the check does not get:
// ignored files (node_modules, a venv, build caches), submodules (not initialised) and LFS content
// (the pointer files are checked out). The project's "prepare" command, run in the copy first, is
// where dependencies come from.
//
// What was checked is verified, not assumed (R9-C6): VerifyCheckout compares every tracked path
// of the commit's tree, byte for byte and mode for mode, with what is on disk in the copy, and
// crowsnest runs it after the checkout, after the prepare command and after the check. A prepare
// or check that rewrote a tracked file (code generation, a repair, another commit checked out)
// makes the run not evidence for the commit. Untracked files may appear (dependencies, build
// output): they are what prepare is for, and they are not part of the commit.
//
// The copy lives in a directory crowsnest owns (under its state directory) and names in the
// check's record before it is made, with a marker naming the repository, so a crash cannot leak it:
// start-up removes every copy no running check owns (RemoveIsolatedDir, R9-C7).

package landing

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // git's object ids, not a security hash
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// isolatedTimeout bounds making or removing the copy: a checkout of a large repository.
const isolatedTimeout = 5 * time.Minute

// isolatedMarker is the file in a copy's directory naming the repository it belongs to, so a
// copy a crash left behind can be removed from git's records too.
const isolatedMarker = "crowsnest-check-repo"

// Isolated is a temporary checkout of one commit. Remove takes it away again (and is safe to call
// more than once).
type Isolated struct {
	Dir    string
	repo   string
	parent string
}

// IsolatedCheckout makes a detached worktree of commit from the repository dir is in, inside
// parent (a new, empty directory crowsnest owns, already recorded with the check), and returns it.
// Every failure is an error: there is no fallback to the job's own checkout.
func IsolatedCheckout(ctx context.Context, dir, commit, parent string) (*Isolated, error) {
	if !isObjectID(commit) {
		return nil, fmt.Errorf("not a commit id: %q", commit)
	}
	repo, err := Root(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("finding the repository of %s: %w", dir, err)
	}
	if repo == "" {
		return nil, fmt.Errorf("%s is not in a git repository", dir)
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("making a directory for the isolated copy: %w", err)
	}
	if err := os.WriteFile(filepath.Join(parent, isolatedMarker), []byte(repo+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(parent)
		return nil, fmt.Errorf("marking the isolated copy's directory: %w", err)
	}
	copyDir := filepath.Join(parent, filepath.Base(repo))
	// The empty tree, in the repository's own object format, as the source of attributes: the
	// repository's .gitattributes then converts nothing the copy is given.
	empty, err := emptyTree(ctx, repo)
	if err != nil {
		_ = os.RemoveAll(parent)
		return nil, fmt.Errorf("preparing the isolated copy: %w", err)
	}
	config := append(filtersOff(ctx, repo), "attr.tree="+empty, "core.attributesFile=/dev/null", "core.sparseCheckout=false",
		"core.sparseCheckoutCone=false", "index.sparse=false", "core.autocrlf=false", "core.symlinks=true")
	call := gitCall{dir: repo, args: []string{"worktree", "add", "--detach", "--force", copyDir, commit},
		config: config, env: []string{"GIT_LFS_SKIP_SMUDGE=1"}, timeout: isolatedTimeout}
	iso := &Isolated{Dir: copyDir, repo: repo, parent: parent}
	if _, err := call.run(ctx); err != nil {
		iso.Remove(ctx)
		return nil, fmt.Errorf("checking out %s in an isolated copy: %w", shortID(commit), err)
	}
	return iso, nil
}

// Remove takes the copy away: git forgets the worktree and the directory goes, whatever the check
// left in it.
func (i *Isolated) Remove(ctx context.Context) {
	if i == nil || i.parent == "" {
		return
	}
	removeCopy(ctx, i.repo, i.Dir, i.parent)
	i.parent = ""
}

// RemoveIsolatedDir removes a copy's directory a crash left behind (parent as IsolatedCheckout was
// given it), telling its repository's git to forget the worktree when the marker names one.
func RemoveIsolatedDir(ctx context.Context, parent string) error {
	repo := ""
	if body, err := os.ReadFile(filepath.Join(parent, isolatedMarker)); err == nil { //nolint:gosec // crowsnest's own state directory
		repo = strings.TrimSpace(string(body))
	}
	copyDir := ""
	if repo != "" {
		copyDir = filepath.Join(parent, filepath.Base(repo))
	}
	removeCopy(ctx, repo, copyDir, parent)
	if _, err := os.Lstat(parent); err == nil {
		return fmt.Errorf("the isolated copy %s could not be removed", parent)
	}
	return nil
}

func removeCopy(ctx context.Context, repo, copyDir, parent string) {
	ctx = context.WithoutCancel(ctx)
	if repo != "" && copyDir != "" {
		_, _ = gitCall{dir: repo, args: []string{"worktree", "remove", "--force", "--force", copyDir}, timeout: isolatedTimeout}.run(ctx)
	}
	_ = os.RemoveAll(parent)
	if repo != "" {
		_, _ = gitCall{dir: repo, args: []string{"worktree", "prune"}}.run(ctx)
	}
}

// ErrNotTheCommit means a checkout's tracked files are not exactly a commit's tree.
var ErrNotTheCommit = errors.New("the checkout is not exactly the commit")

// maxNamedPaths is how many differing paths an error names.
const maxNamedPaths = 8

// VerifyCheckout checks that dir holds exactly commit's tracked tree (R9-C6): HEAD is commit, and
// every path in the commit's tree is on disk with the same bytes (hashed raw, no filter or
// conversion) and the same kind (a file, an executable file, a symbolic link with the same
// target). Submodule entries are not materialised in a copy and are skipped. It returns the
// commit's tree id; an error wraps ErrNotTheCommit and names what differs, or says what could not
// be read (fail closed).
func VerifyCheckout(ctx context.Context, dir, commit string) (string, error) {
	head, err := gitCall{dir: dir, args: []string{"rev-parse", "HEAD"}}.run(ctx)
	if err != nil {
		return "", fmt.Errorf("reading the copy's HEAD: %w", err)
	}
	if head = strings.TrimSpace(head); head != commit {
		return "", fmt.Errorf("%w: HEAD is %s, not %s", ErrNotTheCommit, shortID(head), shortID(commit))
	}
	tree, err := gitCall{dir: dir, args: []string{"rev-parse", commit + "^{tree}"}}.run(ctx)
	if err != nil {
		return "", fmt.Errorf("reading %s's tree: %w", shortID(commit), err)
	}
	newHash, err := objectHasher(ctx, dir)
	if err != nil {
		return "", err
	}
	// The whole tree, bounded by git's output cap: a listing cut short is an error, not a pass.
	listing, err := gitCall{dir: dir, args: []string{"ls-tree", "-r", "-z", "--full-tree", commit}}.run(ctx)
	if err != nil {
		return "", fmt.Errorf("listing %s's tree: %w", shortID(commit), err)
	}
	differ, err := compareTree(ctx, dir, listing, newHash)
	if err != nil {
		return "", err
	}
	if len(differ) > 0 {
		return "", fmt.Errorf("%w: %s", ErrNotTheCommit, strings.Join(differ, "; "))
	}
	return strings.TrimSpace(tree), nil
}

// objectHasher is the hash git names objects with in dir's repository (SHA-1 or SHA-256).
func objectHasher(ctx context.Context, dir string) (func() hash.Hash, error) {
	format, err := gitCall{dir: dir, args: []string{"rev-parse", "--show-object-format"}}.run(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the repository's object format: %w", err)
	}
	switch strings.TrimSpace(format) {
	case "sha1":
		return sha1.New, nil
	case "sha256":
		return sha256.New, nil
	}
	return nil, fmt.Errorf("an object format crowsnest does not know: %q", strings.TrimSpace(format))
}

// compareTree checks each entry of an ls-tree -z listing against dir, returning the differences
// (at most maxNamedPaths, then a count).
func compareTree(ctx context.Context, dir, listing string, newHash func() hash.Hash) ([]string, error) {
	var differ []string
	more := 0
	note := func(text string) {
		if len(differ) < maxNamedPaths {
			differ = append(differ, text)
		} else {
			more++
		}
	}
	for _, entry := range strings.Split(listing, "\x00") {
		if entry == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("reading the commit's tree: an entry crowsnest does not understand: %q", entry)
		}
		mode, kind, want := fields[0], fields[1], fields[2]
		if kind == "commit" {
			continue // a submodule: not materialised in a copy (documented)
		}
		if why := compareEntry(filepath.Join(dir, filepath.FromSlash(path)), mode, want, newHash); why != "" {
			note(path + " " + why)
		}
	}
	if more > 0 {
		differ = append(differ, fmt.Sprintf("and %d more", more))
	}
	return differ, nil
}

// compareEntry says how the file at path differs from a tree entry ("" when it does not).
func compareEntry(path, mode, want string, newHash func() hash.Hash) string {
	info, err := os.Lstat(path)
	if err != nil {
		return "is missing"
	}
	if mode == "120000" {
		if info.Mode()&os.ModeSymlink == 0 {
			return "is not the symbolic link the commit has"
		}
		target, err := os.Readlink(path)
		if err != nil || blobID(newHash, []byte(target)) != want {
			return "points somewhere else"
		}
		return ""
	}
	if !info.Mode().IsRegular() {
		return "is not a regular file"
	}
	if executable := info.Mode()&0o111 != 0; executable != (mode == "100755") {
		return "has another executable bit"
	}
	got, err := fileBlobID(path, info.Size(), newHash)
	if err != nil {
		return "could not be read: " + err.Error()
	}
	if got != want {
		return "has other content"
	}
	return ""
}

// blobID is git's id of a blob holding body.
func blobID(newHash func() hash.Hash, body []byte) string {
	h := newHash()
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(body))
	_, _ = h.Write(body)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// fileBlobID is git's id of a blob holding the file at path (size bytes), read raw.
func fileBlobID(path string, size int64, newHash func() hash.Hash) (string, error) {
	file, err := os.Open(path) //nolint:gosec // a path inside crowsnest's own copy
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	h := newHash()
	_, _ = io.WriteString(h, "blob "+strconv.FormatInt(size, 10)+"\x00")
	n, err := io.Copy(h, bufio.NewReaderSize(file, 1<<16))
	if err != nil {
		return "", err
	}
	if n != size {
		return "", fmt.Errorf("changed while it was read")
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// TreeFingerprint is a content fingerprint of dir's checkout now: its tracked files as they are
// and its untracked files that are not ignored, as one git tree id (WorkTree's snapshot, which
// leaves the checkout's own index alone). Two equal fingerprints mean the same content.
func TreeFingerprint(ctx context.Context, dir string) (string, error) {
	tree, _, err := WorkTree(ctx, dir)
	return tree, err
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}
