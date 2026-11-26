// The land actions: merge a job's branch into its base in the repository's main checkout, push
// it and open a pull request, and delete the branch afterwards. Each checks everything it can
// before it changes anything, and a git failure part-way (a conflict) is undone (merge --abort)
// before the refusal is returned.

package landing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Merge modes (config.MergeCommit, config.MergeFF).
const (
	ModeMerge = "merge"
	ModeFF    = "ff"
)

// MergeInput is what Merge needs.
type MergeInput struct {
	MainDir   string   // the repository's main checkout, where the base branch is checked out
	WorkDir   string   // the job's worktree, which must be clean (its uncommitted work would be lost)
	Branch    string   // the job's branch
	Base      string   // the branch to merge into
	Mode      string   // ModeMerge or ModeFF
	Message   string   // the merge commit's message (ModeMerge)
	Protected []string // branches Land never merges into or pushes to
}

// MergeResult is what a merge did.
type MergeResult struct {
	Commit      string `json:"commit"` // the base branch's new commit
	FastForward bool   `json:"fast_forward"`
}

// Merge merges in.Branch into in.Base in the main checkout. It refuses a protected base, a main
// checkout on another branch or with uncommitted changes to tracked files, a dirty job worktree,
// a branch that is not a fast-forward under ModeFF, and a conflict (aborted, so the base is as it
// was).
func Merge(ctx context.Context, in MergeInput) (MergeResult, error) {
	if err := checkMerge(ctx, in); err != nil {
		return MergeResult{}, err
	}
	before, _ := Head(ctx, in.MainDir)
	args := []string{"merge", "--ff-only", in.Branch}
	if in.Mode != ModeFF {
		message := in.Message
		if message == "" {
			message = "Merge " + in.Branch
		}
		args = []string{"merge", "--no-ff", "--no-edit", "-m", message, in.Branch}
	}
	if _, err := git(ctx, in.MainDir, args...); err != nil {
		return MergeResult{}, mergeFailed(ctx, in, err)
	}
	after, err := Head(ctx, in.MainDir)
	if err != nil {
		return MergeResult{}, fmt.Errorf("reading the merged commit: %w", err)
	}
	result := MergeResult{Commit: after}
	if in.Mode == ModeFF {
		result.FastForward = true
	} else if parents, err := git(ctx, in.MainDir, "rev-list", "--parents", "-n", "1", after); err == nil {
		result.FastForward = len(strings.Fields(parents)) == 2 && after != before
	}
	return result, nil
}

func checkMerge(ctx context.Context, in MergeInput) error {
	if in.Base == "" {
		return refuse("the job has no base branch recorded, so crowsnest does not know where to merge it; open a pull request instead")
	}
	if slices.Contains(in.Protected, in.Base) {
		return refuse("%s is a protected branch: Land never merges into it. Open a pull request instead", in.Base)
	}
	current, err := CurrentBranch(ctx, in.MainDir)
	if err != nil {
		return fmt.Errorf("reading the main checkout's branch: %w", err)
	}
	if current != in.Base {
		if current == "" {
			current = "a detached HEAD"
		}
		return refuse("the main checkout (%s) is on %s, not %s; check out %s there first, or open a pull request", in.MainDir, current, in.Base, in.Base)
	}
	if dirty, err := trackedChanges(ctx, in.MainDir); err != nil {
		return err
	} else if len(dirty) > 0 {
		return refuse("the main checkout (%s) has uncommitted changes (%s); commit or stash them there first. Nothing was merged", in.MainDir, listSome(dirty, 5))
	}
	if in.WorkDir != "" {
		if dirty, err := trackedOrUntracked(ctx, in.WorkDir); err == nil && len(dirty) > 0 {
			return refuse("the job's worktree has uncommitted work (%s): it would be lost when the worktree is removed. Ask the agent to commit it, or discard the job", listSome(dirty, 5))
		}
	}
	if _, err := git(ctx, in.MainDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+in.Branch); err != nil {
		return refuse("the branch %s does not exist in the main checkout's repository", in.Branch)
	}
	return nil
}

// mergeFailed turns a failed merge into a refusal, aborting a merge left in progress.
func mergeFailed(ctx context.Context, in MergeInput, err error) error {
	if _, statErr := git(ctx, in.MainDir, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); statErr == nil {
		conflicts, _ := git(ctx, in.MainDir, "diff", "--name-only", "--diff-filter=U")
		_, _ = git(ctx, in.MainDir, "merge", "--abort")
		files := strings.Fields(conflicts)
		return refuse("merging %s into %s conflicts in %s; the merge was aborted and %s is unchanged. Have the agent merge %s into its branch and resolve it, or open a pull request",
			in.Branch, in.Base, listSome(files, 5), in.Base, in.Base)
	}
	if in.Mode == ModeFF {
		return refuse("%s has moved on since the job started, so %s is not a fast-forward and --merge is ff; nothing was merged. Have the agent rebase, or use a merge commit", in.Base, in.Branch)
	}
	return refuse("git refused the merge: %v; %s is unchanged", err, in.Base)
}

func trackedChanges(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return nil, fmt.Errorf("reading the status of %s: %w", dir, err)
	}
	return porcelainPaths(out), nil
}

func trackedOrUntracked(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	return porcelainPaths(out), nil
}

func porcelainPaths(out string) []string {
	var paths []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(line) > 3 {
			paths = append(paths, strings.TrimSpace(line[3:]))
		}
	}
	return paths
}

func listSome(items []string, limit int) string {
	if len(items) == 0 {
		return "unknown files"
	}
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:limit], ", ") + fmt.Sprintf(" and %d more", len(items)-limit)
}

// ---- Push and pull request ----------------------------------------------------------------

// PushInput is what Push needs.
type PushInput struct {
	WorkDir   string
	Branch    string
	Base      string // the pull request's base
	Title     string
	Body      string // markdown
	Protected []string
	// GH is the gh binary ("" to look it up on PATH); tests point it at a fake.
	GH string
}

// PushResult is what a push did.
type PushResult struct {
	Remote    string `json:"remote"`     // the remote's name
	RemoteURL string `json:"remote_url"` // its URL, credentials stripped
	PRURL     string `json:"pr_url,omitempty"`
	// Note says why no pull request was opened ("" when one was), in words for the card.
	Note string `json:"note,omitempty"`
}

// Push pushes the job's branch to its remote and, for a GitHub remote with gh installed and
// signed in, opens a pull request (never assigning reviewers). Any other host is pushed only,
// and the result says so. A protected branch is never pushed to.
func Push(ctx context.Context, in PushInput) (PushResult, error) {
	if in.Branch == "" {
		return PushResult{}, refuse("the job has no branch to push")
	}
	if slices.Contains(in.Protected, in.Branch) {
		return PushResult{}, refuse("%s is a protected branch: Land never pushes to it", in.Branch)
	}
	remote, err := pushRemote(ctx, in.WorkDir, in.Branch)
	if err != nil {
		return PushResult{}, err
	}
	// The configured URL as written (get-url would apply insteadOf rewrites): it says which host
	// the pull request belongs to.
	url, _ := git(ctx, in.WorkDir, "config", "--get", "remote."+remote+".url")
	result := PushResult{Remote: remote, RemoteURL: StripCredentials(strings.TrimSpace(url))}
	if _, err := gitTimed(ctx, pushTimeout, in.WorkDir, "push", "--set-upstream", remote, "refs/heads/"+in.Branch+":refs/heads/"+in.Branch); err != nil {
		return PushResult{}, refuse("pushing %s to %s failed: %v", in.Branch, remote, err)
	}
	if !IsGitHub(result.RemoteURL) {
		result.Note = "pushed; " + hostOf(result.RemoteURL) + " is not GitHub, so open the pull request there"
		return result, nil
	}
	gh, note := findGH(ctx, in.GH)
	if gh == "" {
		result.Note = "pushed; " + note
		return result, nil
	}
	prURL, err := createPR(ctx, gh, in)
	if err != nil {
		result.Note = "pushed, but the pull request could not be opened: " + firstLine(err.Error())
		return result, nil
	}
	result.PRURL = prURL
	return result, nil
}

// pushRemote is the branch's configured remote, else origin, else the only remote.
func pushRemote(ctx context.Context, dir, branch string) (string, error) {
	if out, err := git(ctx, dir, "config", "--get", "branch."+branch+".remote"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out), nil
	}
	out, err := git(ctx, dir, "remote")
	if err != nil {
		return "", fmt.Errorf("listing remotes: %w", err)
	}
	remotes := strings.Fields(out)
	switch {
	case slices.Contains(remotes, "origin"):
		return "origin", nil
	case len(remotes) == 1:
		return remotes[0], nil
	case len(remotes) == 0:
		return "", refuse("the repository has no remote to push to; merge locally instead")
	default:
		return "", refuse("the repository has several remotes and none is origin (%s); push by hand", strings.Join(remotes, ", "))
	}
}

// findGH returns gh's path when it is installed and signed in, else "" and why not.
func findGH(ctx context.Context, configured string) (string, string) {
	gh := configured
	if gh == "" {
		path, err := exec.LookPath("gh")
		if err != nil {
			return "", "gh is not installed, so open the pull request on GitHub"
		}
		gh = path
	}
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "auth", "status") //nolint:gosec // gh found on PATH or configured
	cmd.Env = gitEnv()
	if err := cmd.Run(); err != nil {
		return "", "gh is not signed in (gh auth login), so open the pull request on GitHub"
	}
	return gh, ""
}

func createPR(ctx context.Context, gh string, in PushInput) (string, error) {
	bodyFile, err := os.CreateTemp("", "crowsnest-pr-*.md")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(bodyFile.Name()) }()
	if _, err := bodyFile.WriteString(in.Body); err != nil {
		_ = bodyFile.Close()
		return "", err
	}
	_ = bodyFile.Close()
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	args := []string{"pr", "create", "--head", in.Branch, "--title", in.Title, "--body-file", bodyFile.Name()}
	if in.Base != "" {
		args = append(args, "--base", in.Base)
	}
	cmd := exec.CommandContext(ctx, gh, args...) //nolint:gosec // fixed subcommand; values are arguments
	cmd.Dir, cmd.Env = in.WorkDir, gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %w: %s", err, firstLine(strings.TrimSpace(string(out))))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "https://") {
			return strings.TrimSpace(line), nil
		}
	}
	return "", errors.New("gh did not print the pull request's address")
}

// IsGitHub reports whether a remote URL is on github.com.
func IsGitHub(url string) bool { return hostOf(url) == "github.com" }

// hostOf is a remote URL's host: https://host/…, ssh://git@host/…, or git@host:owner/repo.
func hostOf(url string) string {
	rest := url
	if _, after, ok := strings.Cut(rest, "://"); ok {
		rest = after
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	host, _, _ := strings.Cut(rest, "/")
	host, _, _ = strings.Cut(host, ":")
	if host == "" {
		return "the remote"
	}
	return strings.ToLower(host)
}

// StripCredentials removes a user:password@ from an https URL, so it can be shown and stored.
func StripCredentials(url string) string {
	scheme, rest, ok := strings.Cut(url, "://")
	if !ok {
		return url
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 && strings.Contains(rest[:at], ":") {
		rest = rest[at+1:]
	}
	return scheme + "://" + rest
}

// ---- After landing ------------------------------------------------------------------------

// DeleteBranch deletes branch in the repository at mainDir. force deletes an unmerged branch (a
// discarded job's); without it git refuses one that is not merged, which is right after a merge.
func DeleteBranch(ctx context.Context, mainDir, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	if _, err := git(ctx, mainDir, "branch", flag, branch); err != nil {
		return fmt.Errorf("deleting the branch %s: %w", branch, err)
	}
	return nil
}

// MainCheckout is the repository's main work tree for any directory inside it (or one of its
// worktrees): the first entry `git worktree list` gives.
func MainCheckout(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("listing the repository's worktrees: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			return filepath.Clean(path), nil
		}
	}
	return "", errors.New("git listed no worktrees")
}
