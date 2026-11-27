// The facts about one chosen folder, in one call, so the picker can say "git repo on GitHub,
// branch main, has a check task, AGENTS.md present" before the choice is made. Git's state is
// read from its metadata files (HEAD, config, the origin's HEAD), not by running git: it is
// cheaper, cannot be steered by a repository's own config (no hooks, no fsmonitor, no aliases
// run), and stays inside the deadline like every other read.

package browse

import (
	"bufio"
	"bytes"
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Remote hosts a repository's origin can be on.
const (
	HostGitHub    = "github"
	HostAzure     = "azure"
	HostGitLab    = "gitlab"
	HostBitbucket = "bitbucket"
	HostOther     = "other"
)

// metadataLimit caps a git metadata file read for facts. HEAD is one line and a config a few
// hundred bytes; anything bigger is not worth parsing.
const metadataLimit = 64 << 10

// Facts describe a folder for the picker.
type Facts struct {
	Path     string `json:"path"`
	Resolved string `json:"resolved"`
	Label    string `json:"label"`
	// Name is the folder's base name: the picker's project name suggestion starts from it.
	Name string `json:"name"`
	// Git is set when the folder holds a .git; Worktree when that .git is a linked worktree's
	// file. GitTop is the nearest enclosing repository when the folder is inside one but not its
	// top ("" otherwise).
	Git      bool   `json:"git"`
	Worktree bool   `json:"worktree"`
	GitTop   string `json:"git_top,omitempty"`
	// Branch is the checked-out branch ("" when detached); DefaultBranch the base branch guess
	// (the origin's HEAD, else main or master when it exists).
	Branch        string `json:"branch,omitempty"`
	Detached      bool   `json:"detached,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	// Remote is the origin's URL with any credentials removed; Host classifies it.
	Remote string `json:"remote,omitempty"`
	Host   string `json:"host,omitempty"`
	// CheckCommand is what crowsnest would run to check work here ("" for none).
	CheckCommand string `json:"check_command,omitempty"`
	AgentsMD     bool   `json:"agents_md"`
	ClaudeMD     bool   `json:"claude_md"`
	// ClaudeTrusted is nil when it could not be told.
	ClaudeTrusted *bool `json:"claude_trusted,omitempty"`
	// IsHome is set for the home directory, IsFilesystemRoot for /: both refused as projects.
	IsHome           bool `json:"is_home"`
	IsFilesystemRoot bool `json:"is_filesystem_root"`
}

// Facts describes the folder at path, which must be a browsable directory.
func (s *Service) Facts(ctx context.Context, path string) (Facts, error) {
	return bounded(ctx, s, func(time.Time) (Facts, error) {
		cleaned, resolved, holder, err := s.admit(ctx, path)
		if err != nil {
			return Facts{}, err
		}
		info, err := s.opts.FS.Stat(resolved)
		if err != nil {
			return Facts{}, classify(err)
		}
		if !info.IsDir() {
			return Facts{}, ErrNotDir
		}
		facts := Facts{Path: cleaned, Resolved: resolved, Label: s.label(cleaned), Name: filepath.Base(cleaned)}
		facts.IsFilesystemRoot = resolved == "/"
		if s.opts.Home != "" {
			homeResolved, homeErr := s.opts.FS.EvalSymlinks(s.opts.Home)
			facts.IsHome = cleaned == s.opts.Home || (homeErr == nil && resolved == homeResolved)
		}
		s.gitFacts(resolved, holder.resolved, &facts)
		facts.AgentsMD = s.isFile(filepath.Join(resolved, "AGENTS.md"))
		facts.ClaudeMD = s.isFile(filepath.Join(resolved, "CLAUDE.md")) || s.isFile(filepath.Join(resolved, ".claude", "CLAUDE.md"))
		if s.opts.CheckCommand != nil {
			facts.CheckCommand = s.opts.CheckCommand(resolved)
		}
		if s.opts.Trusted != nil {
			if trusted, ok := s.opts.Trusted(resolved); ok {
				facts.ClaudeTrusted = &trusted
			}
		}
		return facts, nil
	})
}

func (s *Service) isFile(path string) bool {
	info, err := s.opts.FS.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// gitFacts fills the repository facts of dir, or GitTop when dir is inside a repository below
// its root (nothing above the root is looked at).
func (s *Service) gitFacts(dir, rootDir string, facts *Facts) {
	gitDir, worktree, ok := s.gitDir(dir)
	if !ok {
		for current := dir; current != rootDir && within(current, rootDir); {
			current = filepath.Dir(current)
			if s.isGit(current) {
				facts.GitTop = current
				return
			}
		}
		return
	}
	facts.Git, facts.Worktree = true, worktree
	commonDir := gitDir
	if body, err := s.opts.FS.ReadFile(filepath.Join(gitDir, "commondir"), metadataLimit); err == nil {
		commonDir = joinIfRelative(gitDir, strings.TrimSpace(string(body)))
	}
	if body, err := s.opts.FS.ReadFile(filepath.Join(gitDir, "HEAD"), metadataLimit); err == nil {
		head := strings.TrimSpace(string(body))
		if ref, isRef := strings.CutPrefix(head, "ref: "); isRef {
			facts.Branch = strings.TrimPrefix(ref, "refs/heads/")
		} else if head != "" {
			facts.Detached = true
		}
	}
	facts.DefaultBranch = s.defaultBranch(commonDir, facts.Branch)
	if body, err := s.opts.FS.ReadFile(filepath.Join(commonDir, "config"), metadataLimit); err == nil {
		facts.Remote = cleanRemote(originURL(body))
		facts.Host = HostOf(facts.Remote)
	}
}

// gitDir finds dir's git directory: dir/.git itself, or the directory a worktree's .git file
// names ("gitdir: …").
func (s *Service) gitDir(dir string) (gitDir string, worktree, ok bool) {
	dotGit := filepath.Join(dir, ".git")
	info, err := s.opts.FS.Stat(dotGit)
	if err != nil {
		return "", false, false
	}
	if info.IsDir() {
		return dotGit, false, true
	}
	body, err := s.opts.FS.ReadFile(dotGit, metadataLimit)
	if err != nil {
		return "", false, false
	}
	target, found := strings.CutPrefix(strings.TrimSpace(string(body)), "gitdir:")
	if !found {
		return "", false, false
	}
	return joinIfRelative(dir, strings.TrimSpace(target)), true, true
}

// defaultBranch guesses the base branch: the origin's HEAD, else main, else master, else the
// current branch.
func (s *Service) defaultBranch(commonDir, current string) string {
	if body, err := s.opts.FS.ReadFile(filepath.Join(commonDir, "refs", "remotes", "origin", "HEAD"), metadataLimit); err == nil {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(string(body)), "ref: refs/remotes/origin/"); ok && ref != "" {
			return ref
		}
	}
	packed, _ := s.opts.FS.ReadFile(filepath.Join(commonDir, "packed-refs"), metadataLimit)
	for _, name := range []string{"main", "master"} {
		if _, err := s.opts.FS.Stat(filepath.Join(commonDir, "refs", "heads", name)); err == nil {
			return name
		}
		if bytes.Contains(packed, []byte(" refs/heads/"+name+"\n")) {
			return name
		}
	}
	return current
}

// originURL reads the origin's url from a git config, else the first remote's.
func originURL(config []byte) string {
	var section, first, origin string
	scanner := bufio.NewScanner(bytes.NewReader(config))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Join(strings.Fields(strings.Trim(line, "[]")), " "))
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || !strings.HasPrefix(section, "remote ") || strings.ToLower(strings.TrimSpace(key)) != "url" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if first == "" {
			first = value
		}
		if section == `remote "origin"` {
			origin = value
		}
	}
	if origin != "" {
		return origin
	}
	return first
}

// cleanRemote removes credentials from a remote URL (https://user:token@host/…).
func cleanRemote(remote string) string {
	if !strings.Contains(remote, "://") {
		return remote
	}
	parsed, err := url.Parse(remote)
	if err != nil {
		return ""
	}
	if parsed.User != nil {
		parsed.User = nil
	}
	return parsed.String()
}

// HostOf classifies a remote URL's host: github, azure (Azure DevOps), gitlab, bitbucket,
// other, or "" for none.
func HostOf(remote string) string {
	if remote == "" {
		return ""
	}
	host := remote
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		host = parsed.Hostname()
	} else if at := strings.Index(remote, "@"); at >= 0 {
		// scp-like: git@github.com:org/repo.git
		host = remote[at+1:]
		if colon := strings.Index(host, ":"); colon >= 0 {
			host = host[:colon]
		}
	}
	host = strings.ToLower(host)
	switch {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		return HostGitHub
	case host == "dev.azure.com" || host == "ssh.dev.azure.com" || strings.HasSuffix(host, ".visualstudio.com"):
		return HostAzure
	case host == "gitlab.com":
		return HostGitLab
	case host == "bitbucket.org":
		return HostBitbucket
	}
	return HostOther
}

func joinIfRelative(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}
