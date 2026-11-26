// Finding a transcript. Claude Code writes each session to
// <config dir>/projects/<cwd with every non-alphanumeric character replaced by '->/<session>.jsonl
// (verified on 2.1.332), where the config dir is $CLAUDE_CONFIG_DIR or ~/.claude.

package transcript

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ErrNotFound means no transcript exists for the session (yet).
var ErrNotFound = errors.New("transcript not found")

// sessionIDPattern is what a Claude session id may look like before it is used in a path. Real
// ids are UUIDs; the pattern is looser (the demo and tests use readable ids) but cannot climb
// out of the projects directory.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ConfigDir is Claude Code's configuration directory: getenv("CLAUDE_CONFIG_DIR") when set,
// otherwise ~/.claude. getenv is os.Getenv in production; tests pass their own.
func ConfigDir(getenv func(string) string) (string, error) {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding the home directory for ~/.claude: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// ProjectDirName is the directory Claude Code files a working directory's sessions under: the
// path with every character that is not an ASCII letter or digit replaced by '-'.
func ProjectDirName(cwd string) string {
	out := []byte(cwd)
	for i, c := range out {
		if !isASCIIAlphanumeric(c) {
			out[i] = '-'
		}
	}
	return string(out)
}

// Path is where the transcript for (cwd, sessionID) should be.
func Path(configDir, cwd, sessionID string) string {
	return filepath.Join(configDir, "projects", ProjectDirName(cwd), sessionID+".jsonl")
}

// Locate returns the transcript file for a session. It looks where Path says first; if the file
// is not there (Claude shortens very long project names, and a pane's cwd can drift from the
// directory Claude started in) it looks for the session id under every project, since session
// ids are unique. ErrNotFound when neither finds it.
func Locate(configDir, cwd, sessionID string) (string, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return "", fmt.Errorf("session id %q: %w", sessionID, ErrNotFound)
	}
	if cwd != "" {
		path := Path(configDir, cwd, sessionID)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	matches, err := filepath.Glob(filepath.Join(configDir, "projects", "*", sessionID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("searching for session %s: %w", sessionID, err)
	}
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() {
			return match, nil
		}
	}
	return "", ErrNotFound
}

func isASCIIAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
