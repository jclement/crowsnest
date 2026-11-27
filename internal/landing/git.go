// Package landing is the last mile of a job that ran in a git worktree (DESIGN.md 6.11): what it
// produced (commits ahead of its base, files changed, whether the tree is clean and the branch
// pushed), the diff a reviewer reads, in the order the change is best understood, and the three
// ways it can land: merged into the base branch locally, pushed with a pull request, or
// discarded. It only runs git and gh; it never forces anything. A dirty main checkout, a
// conflict, a branch that is not a fast-forward under --merge ff, a protected base branch or a
// failing push is a refusal with a sentence that says what to do, and leaves every tree as it
// was.
//
// internal/jobs decides when to call it and records the outcome on the job; this package knows
// nothing of jobs.
package landing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// gitTimeout bounds one git command; a merge or a status on a large repository is seconds.
	gitTimeout = 60 * time.Second
	// pushTimeout bounds a push or a pull request, which go over the network.
	pushTimeout = 3 * time.Minute
	// maxFileDiffBytes caps one file's diff as served; past it the diff is cut and says so.
	maxFileDiffBytes = 512 << 10
	// maxCommits and maxFiles bound what Inspect lists.
	maxCommits = 200
	maxFiles   = 2000
)

// Refusal is a land action crowsnest would not take, worded for the human: nothing was changed.
type Refusal struct {
	Message string
	Code    string // "" or CodeHeadMoved
}

func (r *Refusal) Error() string { return r.Message }

// CodeHeadMoved marks a refusal because the branch moved past the reviewed commit (R5-S4).
const CodeHeadMoved = "head_moved"

// IsRefusal reports whether err is a Refusal.
func IsRefusal(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal)
}

func refuse(format string, args ...any) error { return &Refusal{Message: fmt.Sprintf(format, args...)} }

// git runs git in dir with a quiet, non-interactive environment and returns its stdout.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	return gitTimed(ctx, gitTimeout, dir, args...)
}

func gitTimed(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed git subcommands; paths and refs are arguments
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		return stdout.String(), fmt.Errorf("git %s: %w: %s", args[0], err, firstLine(message))
	}
	return stdout.String(), nil
}

// gitEnv is the environment git runs with: never a prompt, never a pager, plain English output.
func gitEnv() []string {
	env := []string{}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") || strings.HasPrefix(entry, "GIT_INDEX_FILE=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C", "GIT_EDITOR=true", "GIT_MERGE_AUTOEDIT=no")
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(line)
}

// CurrentBranch is the branch checked out in dir ("" on a detached HEAD).
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		if _, headErr := git(ctx, dir, "rev-parse", "--verify", "HEAD"); headErr == nil {
			return "", nil // detached
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Head is dir's HEAD commit.
func Head(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(ctx context.Context, dir string) bool {
	out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// ---- What a job produced -------------------------------------------------------------------

// Commit is one commit on the job's branch.
type Commit struct {
	Hash    string    `json:"hash"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	At      time.Time `json:"at"`
}

// FileChange is one file the branch changed.
type FileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"` // a rename's or copy's source
	Status  string `json:"status"`             // added | modified | deleted | renamed | copied | type
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
	// Group is where the file falls in the reading order (schema, server, api, client, ui,
	// tests, docs, config, churn), and Churn marks a file best shown as one collapsed row
	// (lockfiles, generated code, snapshots).
	Group string `json:"group"`
	Churn bool   `json:"churn"`
}

// Produced is what a job's branch holds against its base.
type Produced struct {
	Branch      string       `json:"branch"`
	Base        string       `json:"base"`      // the base branch ("" when unknown)
	DiffBase    string       `json:"diff_base"` // the commit the diff is taken from (the merge base)
	Head        string       `json:"head"`      // the branch's commit
	Ahead       int          `json:"ahead"`     // commits on the branch that the base lacks
	Behind      int          `json:"behind"`    // commits on the base the branch lacks
	Commits     []Commit     `json:"commits"`
	Files       []FileChange `json:"files"`
	Added       int          `json:"added"`
	Deleted     int          `json:"deleted"`
	Clean       bool         `json:"clean"`
	Dirty       []string     `json:"dirty"` // `git status --porcelain` lines, capped
	Upstream    string       `json:"upstream,omitempty"`
	Pushed      bool         `json:"pushed"` // the upstream holds this exact commit
	InspectedAt time.Time    `json:"inspected_at"`
}

// Inspect reads what the branch checked out in workDir holds against base (a branch name; when
// it does not resolve, baseCommit, the commit the worktree started from).
func Inspect(ctx context.Context, workDir, base, baseCommit string) (Produced, error) {
	produced := Produced{Base: base, Commits: []Commit{}, Files: []FileChange{}, Dirty: []string{}, InspectedAt: time.Now().UTC()}
	branch, err := CurrentBranch(ctx, workDir)
	if err != nil {
		return produced, fmt.Errorf("reading the worktree's branch: %w", err)
	}
	produced.Branch = branch
	if produced.Head, err = Head(ctx, workDir); err != nil {
		return produced, fmt.Errorf("reading the worktree's commit: %w", err)
	}
	produced.DiffBase = diffBase(ctx, workDir, base, baseCommit)
	if produced.DiffBase == "" {
		return produced, fmt.Errorf("neither the base branch %q nor the starting commit can be found in the worktree", base)
	}
	if err := readCounts(ctx, workDir, base, &produced); err != nil {
		return produced, err
	}
	if produced.Commits, err = commits(ctx, workDir, produced.DiffBase); err != nil {
		return produced, err
	}
	if produced.Files, err = changedFiles(ctx, workDir, produced.DiffBase); err != nil {
		return produced, err
	}
	for _, file := range produced.Files {
		produced.Added += file.Added
		produced.Deleted += file.Deleted
	}
	if err := readStatus(ctx, workDir, &produced); err != nil {
		return produced, err
	}
	readUpstream(ctx, workDir, &produced)
	return produced, nil
}

// diffBase is the merge base of HEAD and base, else baseCommit when it exists.
func diffBase(ctx context.Context, dir, base, baseCommit string) string {
	if base != "" {
		if out, err := git(ctx, dir, "merge-base", "HEAD", "refs/heads/"+base); err == nil {
			return strings.TrimSpace(out)
		}
	}
	if baseCommit != "" {
		if _, err := git(ctx, dir, "cat-file", "-e", baseCommit+"^{commit}"); err == nil {
			return baseCommit
		}
	}
	return ""
}

func readCounts(ctx context.Context, dir, base string, produced *Produced) error {
	against := produced.DiffBase
	if base != "" {
		if _, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+base); err == nil {
			against = "refs/heads/" + base
		}
	}
	out, err := git(ctx, dir, "rev-list", "--left-right", "--count", against+"...HEAD")
	if err != nil {
		return fmt.Errorf("counting commits ahead of %s: %w", base, err)
	}
	fields := strings.Fields(out)
	if len(fields) == 2 {
		produced.Behind, _ = strconv.Atoi(fields[0])
		produced.Ahead, _ = strconv.Atoi(fields[1])
	}
	return nil
}

func commits(ctx context.Context, dir, from string) ([]Commit, error) {
	out, err := git(ctx, dir, "log", "--no-color", "--max-count="+strconv.Itoa(maxCommits), "--format=%H%x1f%s%x1f%an%x1f%ct", from+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("listing the branch's commits: %w", err)
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

// changedFiles reads the name-status and numstat of from..HEAD (committed work only: the
// uncommitted part is reported as the dirty list, not as the change).
func changedFiles(ctx context.Context, dir, from string) ([]FileChange, error) {
	statusOut, err := git(ctx, dir, "diff", "--no-color", "--name-status", "-M", "-z", from, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("listing changed files: %w", err)
	}
	numOut, err := git(ctx, dir, "diff", "--no-color", "--numstat", "-M", "-z", from, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("counting changed lines: %w", err)
	}
	files := parseNameStatus(statusOut)
	counts := parseNumstat(numOut)
	for i := range files {
		if count, ok := counts[files[i].Path]; ok {
			files[i].Added, files[i].Deleted, files[i].Binary = count.added, count.deleted, count.binary
		}
		files[i].Group, files[i].Churn = Classify(files[i].Path)
	}
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	return files, nil
}

// parseNameStatus reads `git diff --name-status -z`: a status field then one path (two for a
// rename or copy), each NUL-terminated.
func parseNameStatus(out string) []FileChange {
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	files := []FileChange{}
	for i := 0; i < len(fields); i++ {
		code := fields[i]
		if code == "" {
			continue
		}
		file := FileChange{Status: statusWord(code[0])}
		if code[0] == 'R' || code[0] == 'C' {
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

func statusWord(code byte) string {
	switch code {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type"
	default:
		return "modified"
	}
}

type lineCount struct {
	added, deleted int
	binary         bool
}

// parseNumstat reads `git diff --numstat -z`: "added\tdeleted\tpath\0", or for a rename
// "added\tdeleted\t\0old\0new\0". Binary files count "-\t-".
func parseNumstat(out string) map[string]lineCount {
	counts := map[string]lineCount{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		count := lineCount{binary: parts[0] == "-" && parts[1] == "-"}
		count.added, _ = strconv.Atoi(parts[0])
		count.deleted, _ = strconv.Atoi(parts[1])
		path := parts[2]
		if path == "" && i+2 < len(fields) { // a rename: the new path is the second name
			path = fields[i+2]
			i += 2
		}
		counts[path] = count
	}
	return counts
}

func readStatus(ctx context.Context, dir string, produced *Produced) error {
	out, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return fmt.Errorf("reading the worktree's status: %w", err)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(produced.Dirty) < 50 {
			produced.Dirty = append(produced.Dirty, line)
		}
	}
	produced.Clean = len(produced.Dirty) == 0
	return nil
}

func readUpstream(ctx context.Context, dir string, produced *Produced) {
	out, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return
	}
	produced.Upstream = strings.TrimSpace(out)
	remoteHead, err := git(ctx, dir, "rev-parse", "@{upstream}")
	produced.Pushed = err == nil && strings.TrimSpace(remoteHead) == produced.Head
}

// FileDiff is one file's diff from..HEAD as unified text, cut at maxFileDiffBytes (cut says so).
// A binary file returns "" with binary true. oldPath is a rename's source, so the diff shows the
// rename rather than an added file.
func FileDiff(ctx context.Context, workDir, from, path, oldPath string) (diff string, binary, cut bool, err error) {
	args := []string{"diff", "--no-color", "--no-ext-diff", "-M", from, "HEAD", "--", path}
	if oldPath != "" && oldPath != path {
		args = append(args, oldPath)
	}
	out, err := git(ctx, workDir, args...)
	if err != nil {
		return "", false, false, fmt.Errorf("reading the diff of %s: %w", path, err)
	}
	if strings.Contains(out, "\nBinary files ") || strings.HasPrefix(out, "Binary files ") || strings.Contains(out, "GIT binary patch") {
		return "", true, false, nil
	}
	if len(out) > maxFileDiffBytes {
		return out[:maxFileDiffBytes], false, true, nil
	}
	return out, false, false, nil
}

// ---- What acceptance reads (DESIGN.md 6.15) ------------------------------------------------

// Snapshot is the part of a worktree acceptance needs: its commit, whether the tree is clean,
// and how far it is ahead of and behind its base.
type Snapshot struct {
	Head   string
	Clean  bool
	Dirty  []string // `git status --porcelain` lines, capped
	Ahead  int
	Behind int
	// Based says whether the base could be found (Ahead and Behind mean nothing otherwise).
	Based bool
}

// Snap reads a Snapshot of workDir. base and baseCommit are as for Inspect; both empty skips
// the counts (a job that did not run in a worktree of its own).
func Snap(ctx context.Context, workDir, base, baseCommit string) (Snapshot, error) {
	snapshot := Snapshot{Dirty: []string{}}
	var err error
	if snapshot.Head, err = Head(ctx, workDir); err != nil {
		return snapshot, fmt.Errorf("reading the worktree's commit: %w", err)
	}
	produced := Produced{Dirty: []string{}}
	if err := readStatus(ctx, workDir, &produced); err != nil {
		return snapshot, err
	}
	snapshot.Clean, snapshot.Dirty = produced.Clean, produced.Dirty
	if base == "" && baseCommit == "" {
		return snapshot, nil
	}
	produced.DiffBase = diffBase(ctx, workDir, base, baseCommit)
	if produced.DiffBase == "" {
		return snapshot, nil
	}
	if err := readCounts(ctx, workDir, base, &produced); err != nil {
		return snapshot, err
	}
	snapshot.Ahead, snapshot.Behind, snapshot.Based = produced.Ahead, produced.Behind, true
	return snapshot, nil
}
