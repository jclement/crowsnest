// A diff at any time (DESIGN.md 6.21): what a checkout holds now against a base, while its agent
// is still working. Three comparisons, all between trees, so every file carries the blob ids of
// both sides and a content hash that changes exactly when the file's diff does:
//
//	committed:   base → HEAD           (the branch's commits)
//	uncommitted: HEAD → the work tree  (edits not yet committed, untracked files included)
//	all:         base → the work tree
//
// The work tree is read into a tree object through a temporary index (a copy of the real one,
// so unchanged files are not hashed again): `git add` into that index, then `git write-tree`. The
// checkout's own index, HEAD and refs are never touched and no lock the agent's git takes is held;
// the only trace is loose blob and tree objects, which git's gc collects in time.
//
// Every git call carries the protective configuration of git.go, and the clean, smudge and
// process filters the configuration defines are switched off by name (filtersOff), because
// `git add` would otherwise run a command the repository chose, every few seconds, unattended.
// Untracked files over a size, past a count, or that are not regular files are not added; they
// are listed as too large instead.

package landing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// changesTimeout bounds one git command of a live diff: it runs often and unattended.
	changesTimeout = 20 * time.Second
	// MaxUntrackedBytes is the largest untracked file a live diff reads; larger ones are listed
	// as too large, not hashed.
	MaxUntrackedBytes = 2 << 20
	// maxUntracked is how many untracked files a live diff adds; the rest are listed as skipped.
	maxUntracked = 1000
	// MaxChangedFiles bounds the files a comparison lists.
	MaxChangedFiles = 1500
	// MaxBlobBytes is the largest side of a file whose whole text is served (for highlighting and
	// expanding context in the browser).
	MaxBlobBytes = 256 << 10
	// maxIndexBytes bounds the copy of a checkout's index (R7-S2). git's index for the 1,500
	// files a diff lists is well under a megabyte; a repository of a few hundred thousand files
	// is still under this.
	maxIndexBytes = 256 << 20
	// NullBlob is git's id for "no file on this side".
	NullBlob = "0000000000000000000000000000000000000000"
)

// DiffFile is one file a comparison changed, with both sides' blobs.
type DiffFile struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
	Status  string `json:"status"` // added | modified | deleted | renamed | copied | type
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
	OldBlob string `json:"old_blob"`
	NewBlob string `json:"new_blob"`
	OldMode string `json:"old_mode,omitempty"`
	NewMode string `json:"new_mode,omitempty"`
	// TooLarge marks an untracked file the live diff did not read (over MaxUntrackedBytes, past
	// the count, or not a regular file); Note says which.
	TooLarge bool   `json:"too_large,omitempty"`
	Note     string `json:"note,omitempty"`
	// Hash identifies this file's diff: both blobs, both paths and both modes. Equal hashes are
	// the same change, whichever comparison produced them.
	Hash string `json:"hash"`
}

// Skipped is an untracked file the work-tree snapshot did not add.
type Skipped struct {
	Path string
	Size int64
	Why  string
	// Stamp is size and modification time: it stands in for a blob id in the file's hash, so a
	// viewed mark on it clears when the file changes.
	Stamp string
}

// filePattern refuses a path that is not a plain relative path inside the checkout.
var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@{}^~+-]{0,199}$`)

// ValidRef reports whether ref is safe to hand to git as a revision: no option, no whitespace,
// no `..` range.
func ValidRef(ref string) bool {
	return refPattern.MatchString(ref) && !strings.Contains(ref, "..") && !strings.HasSuffix(ref, ".lock")
}

// gitCall is one git command of a live diff.
type gitCall struct {
	dir     string
	args    []string
	env     []string // added to gitEnv()
	config  []string // added to protectiveConfig
	stdin   io.Reader
	timeout time.Duration
}

func (c gitCall) run(ctx context.Context) (string, error) {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = changesTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := make([]string, 0, 2*len(c.config)+len(c.args)+16)
	for _, config := range c.config {
		args = append(args, "-c", config)
	}
	args = append(args, protectedArgs(c.dir, c.args)...)
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed git subcommands; paths and refs are arguments
	cmd.Env = append(gitEnv(), c.env...)
	cmd.Stdin = c.stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		return stdout.String(), fmt.Errorf("git %s: %w: %s", c.args[0], err, firstLine(message))
	}
	return stdout.String(), nil
}

// filtersOff is the configuration that switches off every clean, smudge and process filter the
// repository's configuration (any level) defines, by name. Reading the configuration runs nothing.
func filtersOff(ctx context.Context, dir string) []string {
	out, err := gitCall{dir: dir, args: []string{"config", "-z", "--get-regexp", `^filter\.`}}.run(ctx)
	if err != nil {
		return nil // no filters (git exits 1 when nothing matches)
	}
	names := map[string]bool{}
	for _, entry := range strings.Split(out, "\x00") {
		key, _, _ := strings.Cut(entry, "\n")
		key = strings.TrimPrefix(key, "filter.")
		dot := strings.LastIndexByte(key, '.')
		if dot <= 0 {
			continue
		}
		names[key[:dot]] = true
	}
	config := []string{}
	for name := range names {
		for _, field := range []string{"clean", "smudge", "process"} {
			config = append(config, "filter."+name+"."+field+"=")
		}
		config = append(config, "filter."+name+".required=false")
	}
	return config
}

// Root is the top of the work tree that contains dir.
func Root(ctx context.Context, dir string) (string, error) {
	out, err := gitCall{dir: dir, args: []string{"rev-parse", "--show-toplevel"}}.run(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ResolveCommit is the commit ref names in dir ("" when it does not resolve to one).
func ResolveCommit(ctx context.Context, dir, ref string) string {
	if ref == "" {
		return ""
	}
	out, err := gitCall{dir: dir, args: []string{"rev-parse", "--verify", "--quiet", "--end-of-options", ref + "^{commit}"}}.run(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// MergeBase is the merge base of HEAD and ref in dir ("" when there is none).
func MergeBase(ctx context.Context, dir, ref string) string {
	commit := ResolveCommit(ctx, dir, ref)
	if commit == "" {
		return ""
	}
	out, err := gitCall{dir: dir, args: []string{"merge-base", "HEAD", commit}}.run(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// CommitAt is the commit HEAD was at, on its first-parent line, at the moment at ("" when HEAD
// is younger than at's oldest ancestor: no commit is that old).
func CommitAt(ctx context.Context, dir string, at time.Time) string {
	out, err := gitCall{dir: dir, args: []string{"rev-list", "-1", "--first-parent", "--before=" + strconv.FormatInt(at.Unix(), 10), "HEAD"}}.run(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Upstream is the upstream of dir's current branch ("" when it has none).
func Upstream(ctx context.Context, dir string) string {
	out, err := gitCall{dir: dir, args: []string{"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"}}.run(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// DefaultBranch is the remote's default branch as origin/HEAD names it ("" when unknown).
func DefaultBranch(ctx context.Context, dir string) string {
	out, err := gitCall{dir: dir, args: []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"}}.run(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HeadTree is HEAD's commit in dir, or "" in a repository with no commit yet.
func HeadTree(ctx context.Context, dir string) string {
	return ResolveCommit(ctx, dir, "HEAD")
}

// emptyTree is git's empty tree for SHA-1 repositories, the "before" of a repository with no
// commit; written on demand so it exists.
func emptyTree(ctx context.Context, dir string) (string, error) {
	out, err := gitCall{dir: dir, args: []string{"hash-object", "-t", "tree", "-w", "--stdin"}, stdin: strings.NewReader("")}.run(ctx)
	return strings.TrimSpace(out), err
}

// WorkTree reads dir's work tree (tracked files as they are now, and untracked files that are not
// ignored) into a tree object and returns its id, with the untracked files it left out.
func WorkTree(ctx context.Context, dir string) (string, []Skipped, error) {
	indexOut, err := gitCall{dir: dir, args: []string{"rev-parse", "--git-path", "index"}}.run(ctx)
	if err != nil {
		return "", nil, err
	}
	realIndex := strings.TrimSpace(indexOut)
	if !filepath.IsAbs(realIndex) {
		realIndex = filepath.Join(dir, realIndex)
	}
	tmp, err := os.CreateTemp("", "crowsnest-index-*")
	if err != nil {
		return "", nil, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath); _ = os.Remove(tmpPath + ".lock") }()
	copied := copyIndex(realIndex, tmp) == nil
	if err := tmp.Close(); err != nil {
		return "", nil, err
	}
	off := filtersOff(ctx, dir)
	call := func(args ...string) gitCall {
		return gitCall{dir: dir, args: args, env: []string{"GIT_INDEX_FILE=" + tmpPath}, config: off}
	}
	if !copied {
		_ = os.Remove(tmpPath) // git wants no file, not an empty one
		if HeadTree(ctx, dir) != "" {
			if _, err := call("read-tree", "HEAD").run(ctx); err != nil {
				return "", nil, err
			}
		}
	}
	if _, err := call("add", "--update", "--ignore-errors").run(ctx); err != nil && !strings.Contains(err.Error(), "exit status 1") {
		return "", nil, err
	}
	others, err := call("ls-files", "-z", "--others", "--exclude-standard").run(ctx)
	if err != nil {
		return "", nil, err
	}
	var addable []string
	var skipped []Skipped
	for _, name := range strings.Split(others, "\x00") {
		if name == "" {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(dir, filepath.FromSlash(name)))
		switch {
		case statErr != nil:
			continue // gone since the listing
		case info.Mode()&os.ModeSymlink != 0:
			addable = appendCapped(addable, &skipped, name, info) // a link is its target's name, small
		case !info.Mode().IsRegular():
			skipped = append(skipped, skip(name, info, "not a regular file"))
		case info.Size() > MaxUntrackedBytes:
			skipped = append(skipped, skip(name, info, fmt.Sprintf("untracked, %s: too large to read", humanBytes(info.Size()))))
		default:
			addable = appendCapped(addable, &skipped, name, info)
		}
	}
	if len(addable) > 0 {
		var list bytes.Buffer
		for _, name := range addable {
			list.WriteString(name)
			list.WriteByte(0)
		}
		addNew := call("add", "--ignore-errors", "--pathspec-from-file=-", "--pathspec-file-nul")
		addNew.stdin = &list
		if _, err := addNew.run(ctx); err != nil && !strings.Contains(err.Error(), "exit status 1") {
			return "", nil, err
		}
	}
	out, err := call("write-tree").run(ctx)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(out), skipped, nil
}

// copyIndex copies the checkout's index into tmp (R7-S2). The index is a file the agent controls:
// a link to /dev/zero would copy until the disk is full, and a FIFO would block open(2) forever
// while the subject's reading holds its lock. So it must be a regular file, not a link, opened
// without blocking, no larger than maxIndexBytes, and the copy reads at most that much. Any refusal
// is an error, and the caller builds the temporary index from HEAD instead (slower, trusts nothing).
func copyIndex(path string, tmp *os.File) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxIndexBytes {
		return fmt.Errorf("the index %s is not a regular file of at most %s", path, humanBytes(maxIndexBytes))
	}
	source, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // the checkout's own index, read only
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	opened, err := source.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return fmt.Errorf("the index %s changed while it was opened", path)
	}
	written, err := io.Copy(tmp, io.LimitReader(source, maxIndexBytes+1))
	if err != nil {
		return err
	}
	if written > maxIndexBytes {
		return fmt.Errorf("the index %s grew past %s", path, humanBytes(maxIndexBytes))
	}
	return nil
}

func appendCapped(addable []string, skipped *[]Skipped, name string, info os.FileInfo) []string {
	if len(addable) >= maxUntracked {
		*skipped = append(*skipped, skip(name, info, fmt.Sprintf("untracked: past the first %d new files, not read", maxUntracked)))
		return addable
	}
	return append(addable, name)
}

func skip(name string, info os.FileInfo, why string) Skipped {
	return Skipped{Path: name, Size: info.Size(), Why: why, Stamp: fmt.Sprintf("stamp:%d:%d", info.Size(), info.ModTime().UnixNano())}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// Compare lists the files that differ between two tree-ish ids (a commit or a tree) in dir, with
// renames found (-M), line counts, binary files marked and each file's hash. from "" is the empty
// tree (a repository's first commit).
func Compare(ctx context.Context, dir, from, to string) ([]DiffFile, bool, error) {
	if from == "" {
		empty, err := emptyTree(ctx, dir)
		if err != nil {
			return nil, false, err
		}
		from = empty
	}
	raw, err := gitCall{dir: dir, args: []string{"diff", "--no-color", "--raw", "--no-abbrev", "-z", "-M", from, to}}.run(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("listing changed files: %w", err)
	}
	num, err := gitCall{dir: dir, args: []string{"diff", "--no-color", "--numstat", "-z", "-M", from, to}}.run(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("counting changed lines: %w", err)
	}
	files := parseRaw(raw)
	counts := parseNumstat(num)
	for i := range files {
		if count, ok := counts[files[i].Path]; ok {
			files[i].Added, files[i].Deleted, files[i].Binary = count.added, count.deleted, count.binary
		}
		files[i].Hash = FileHash(files[i])
	}
	truncated := len(files) > MaxChangedFiles
	if truncated {
		files = files[:MaxChangedFiles]
	}
	return files, truncated, nil
}

// SkippedFiles turns the work tree's skipped files into rows of a comparison against the work
// tree (added, too large), leaving out any the comparison already lists.
func SkippedFiles(skipped []Skipped, listed []DiffFile) []DiffFile {
	have := map[string]bool{}
	for _, file := range listed {
		have[file.Path] = true
	}
	out := []DiffFile{}
	for _, entry := range skipped {
		if have[entry.Path] {
			continue
		}
		file := DiffFile{Path: entry.Path, Status: "added", Binary: true, TooLarge: true, Note: entry.Why, OldBlob: NullBlob, NewBlob: entry.Stamp}
		file.Hash = FileHash(file)
		out = append(out, file)
	}
	return out
}

// GroupRank is a reading-order group's place (schema first, churn last).
func GroupRank(group string) int {
	for i, name := range groups {
		if name == group {
			return i
		}
	}
	return len(groups)
}

// FileHash is a file's content hash: the same diff, the same hash.
func FileHash(file DiffFile) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{file.OldBlob, file.NewBlob, file.OldPath, file.Path, file.OldMode, file.NewMode}, "\x00")))
	return hex.EncodeToString(sum[:12])
}

// parseRaw reads `git diff --raw -z --no-abbrev`: ":oldmode newmode oldsha newsha STATUS\0path\0"
// (two paths for a rename or copy).
func parseRaw(out string) []DiffFile {
	fields := strings.Split(out, "\x00")
	files := []DiffFile{}
	for i := 0; i < len(fields); i++ {
		head := fields[i]
		if !strings.HasPrefix(head, ":") {
			continue
		}
		parts := strings.Fields(strings.TrimPrefix(head, ":"))
		if len(parts) < 5 || parts[4] == "" {
			continue
		}
		file := DiffFile{OldMode: parts[0], NewMode: parts[1], OldBlob: parts[2], NewBlob: parts[3], Status: statusWord(parts[4][0])}
		if file.OldMode == file.NewMode {
			file.OldMode, file.NewMode = "", "" // only a mode change is worth carrying
		}
		if parts[4][0] == 'R' || parts[4][0] == 'C' {
			if i+2 >= len(fields) {
				break
			}
			file.OldPath, file.Path = fields[i+1], fields[i+2]
			i += 2
		} else {
			if i+1 >= len(fields) {
				break
			}
			file.Path = fields[i+1]
			i++
		}
		files = append(files, file)
	}
	return files
}

// PatchOptions shape one file's unified diff.
type PatchOptions struct {
	// Context is the number of unchanged lines around each change (3 when 0; up to 100000, the
	// whole file).
	Context int
	// IgnoreWhitespace hides changes in whitespace only (-w).
	IgnoreWhitespace bool
}

func (o PatchOptions) args() []string {
	context := o.Context
	if context <= 0 {
		context = 3
	}
	if context > 100000 {
		context = 100000
	}
	args := []string{"-U" + strconv.Itoa(context)}
	if o.IgnoreWhitespace {
		args = append(args, "-w")
	}
	return args
}

// Patch is one file's unified diff between two tree-ish ids, cut at maxFileDiffBytes (cut says
// so); a binary file returns "" and binary true.
func Patch(ctx context.Context, dir, from, to, path, oldPath string, opts PatchOptions) (diff string, binary, cut bool, err error) {
	if from == "" {
		if from, err = emptyTree(ctx, dir); err != nil {
			return "", false, false, err
		}
	}
	args := append([]string{"diff", "--no-color", "-M"}, opts.args()...)
	args = append(args, from, to, "--", path)
	if oldPath != "" && oldPath != path {
		args = append(args, oldPath)
	}
	out, err := gitCall{dir: dir, args: args}.run(ctx)
	if err != nil {
		return "", false, false, fmt.Errorf("reading the diff of %s: %w", path, err)
	}
	return trimPatch(out)
}

// BlobPatch is the unified diff from one blob to another (what changed in a file since a version
// of it was viewed), labelled with path.
func BlobPatch(ctx context.Context, dir, fromBlob, toBlob, path string, opts PatchOptions) (diff string, binary, cut bool, err error) {
	args := append([]string{"diff", "--no-color"}, opts.args()...)
	args = append(args, fromBlob, toBlob)
	out, err := gitCall{dir: dir, args: args}.run(ctx)
	if err != nil {
		return "", false, false, fmt.Errorf("reading what changed in %s: %w", path, err)
	}
	// git labels a blob diff with the blob ids; name the file instead.
	out = strings.Replace(out, "a/"+fromBlob, "a/"+path, 1)
	out = strings.Replace(out, "b/"+toBlob, "b/"+path, 1)
	return trimPatch(out)
}

func trimPatch(out string) (string, bool, bool, error) {
	if strings.Contains(out, "\nBinary files ") || strings.HasPrefix(out, "Binary files ") || strings.Contains(out, "GIT binary patch") {
		return "", true, false, nil
	}
	if len(out) > maxFileDiffBytes {
		cut := out[:maxFileDiffBytes]
		if index := strings.LastIndexByte(cut, '\n'); index > 0 {
			cut = cut[:index+1]
		}
		return cut, false, true, nil
	}
	return out, false, false, nil
}

// HasBlob reports whether dir's object store still holds blob.
func HasBlob(ctx context.Context, dir, blob string) bool {
	if blob == "" || blob == NullBlob || !isObjectID(blob) {
		return false
	}
	_, err := gitCall{dir: dir, args: []string{"cat-file", "-e", blob + "^{blob}"}}.run(ctx)
	return err == nil
}

// ErrBlobTooLarge is Blob's answer for a blob over its limit.
var ErrBlobTooLarge = errors.New("the file is too large to show whole")

// Blob is a blob's text, at most max bytes (ErrBlobTooLarge past it); "" for the null blob.
func Blob(ctx context.Context, dir, blob string, maxBytes int) (string, error) {
	if blob == "" || blob == NullBlob {
		return "", nil
	}
	if !isObjectID(blob) {
		return "", fmt.Errorf("not a blob id: %q", blob)
	}
	sizeOut, err := gitCall{dir: dir, args: []string{"cat-file", "-s", blob}}.run(ctx)
	if err != nil {
		return "", err
	}
	size, _ := strconv.Atoi(strings.TrimSpace(sizeOut))
	if size > maxBytes {
		return "", ErrBlobTooLarge
	}
	return gitCall{dir: dir, args: []string{"cat-file", "blob", blob}}.run(ctx)
}

var objectID = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

func isObjectID(id string) bool { return objectID.MatchString(id) }

// CommitsBetween lists the commits in from..to (newest first, at most maxCommits).
func CommitsBetween(ctx context.Context, dir, from, to string) ([]Commit, error) {
	if from == "" || from == to {
		return []Commit{}, nil
	}
	return commitsRange(ctx, dir, from+".."+to)
}

func commitsRange(ctx context.Context, dir, rangeSpec string) ([]Commit, error) {
	out, err := gitCall{dir: dir, args: []string{"log", "--no-color", "--max-count=" + strconv.Itoa(maxCommits), "--format=%H%x1f%s%x1f%an%x1f%ct", rangeSpec}}.run(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing commits: %w", err)
	}
	list := []Commit{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(line, "\x1f")
		if len(parts) != 4 {
			continue
		}
		seconds, _ := strconv.ParseInt(parts[3], 10, 64)
		list = append(list, Commit{Hash: parts[0], Subject: parts[1], Author: parts[2], At: time.Unix(seconds, 0).UTC()})
	}
	return list, nil
}
