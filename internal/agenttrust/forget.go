// Forgetting the records a test made. The real-Claude and real-Codex suites run the agents with
// the user's own login, which lives beside their state (Claude's in the keychain under the default
// config directory, Codex's in ~/.codex/auth.json, whose refresh token is single-use, so it cannot
// be copied to a scratch home without risking the user's own login). So those agents record
// their folders in the user's own files: Claude a `projects` entry for every folder it runs in,
// Codex a trust table when its prompt is answered. A suite removes exactly the entries for its own
// scratch folders at teardown, with the same careful update as a trust write. Nothing else in the
// files is touched, and nothing outside the one scratch folder named.

package agenttrust

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// minForgetDepth is the fewest path elements a forgotten folder may have: a scratch folder is
// /tmp/cnfr-… or deeper, and a slip that names / or /Users never reaches the files (home and its
// ancestors are refused as well).
const minForgetDepth = 2

// ForgetUnder removes, from both agents' files, every folder record at or under root (a test's
// own scratch folder, absolute), and returns how many entries each lost. home is refused, as is
// any ancestor of home and any shallow path.
func ForgetUnder(paths Paths, root, home string) (claude, codex int, err error) {
	root = filepath.Clean(root)
	if err := forgettable(root, home); err != nil {
		return 0, 0, err
	}
	if paths.ClaudeConfig != "" {
		if _, err := updateFile(paths.ClaudeConfig, func(original []byte) ([]byte, bool, error) {
			updated, removed, err := claudeWithout(original, root)
			claude = removed
			return updated, removed > 0, err
		}); err != nil {
			return claude, 0, fmt.Errorf("Claude's config: %w", err)
		}
	}
	if paths.CodexConfig != "" {
		if _, err := updateFile(paths.CodexConfig, func(original []byte) ([]byte, bool, error) {
			updated, removed, err := codexWithout(original, root)
			codex = removed
			return updated, removed > 0, err
		}); err != nil {
			return claude, codex, fmt.Errorf("Codex's config: %w", err)
		}
	}
	return claude, codex, nil
}

func forgettable(root, home string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("%s is not an absolute path", root)
	}
	if strings.Count(root, string(filepath.Separator)) < minForgetDepth {
		return fmt.Errorf("%s is too shallow to be a test's scratch folder", root)
	}
	if home != "" && (root == filepath.Clean(home) || isUnder(filepath.Clean(home), root)) {
		return fmt.Errorf("%s is the home folder or above it", root)
	}
	return nil
}

// isUnder reports whether path is root or inside it.
func isUnder(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// claudeWithout drops every projects entry at or under root, keeping every other value as raw JSON.
func claudeWithout(original []byte, root string) ([]byte, int, error) {
	if len(bytes.TrimSpace(original)) == 0 {
		return original, 0, nil
	}
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(original, &top); err != nil || top == nil {
		return nil, 0, errors.New("the config is not a JSON object, so it was left alone")
	}
	raw, ok := top["projects"]
	if !ok || string(raw) == "null" {
		return original, 0, nil
	}
	projects := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &projects); err != nil {
		return nil, 0, errors.New("the config's projects value is not an object, so it was left alone")
	}
	removed := 0
	for dir := range projects {
		if isUnder(filepath.Clean(dir), root) {
			delete(projects, dir)
			removed++
		}
	}
	if removed == 0 {
		return original, 0, nil
	}
	encoded, err := marshal(projects, "")
	if err != nil {
		return nil, 0, err
	}
	top["projects"] = encoded
	out, err := marshal(top, "  ")
	if err != nil {
		return nil, 0, err
	}
	return append(out, '\n'), removed, nil
}

// codexWithout cuts each `[projects."<dir>"]` table at or under root out of config.toml as text
// (the table header through the line before the next table), so every other line stays as it
// was; the result must parse and still hold every other project.
func codexWithout(original []byte, root string) ([]byte, int, error) {
	var before codexConfig
	if err := toml.Unmarshal(original, &before); err != nil {
		return nil, 0, fmt.Errorf("config.toml does not parse, so it was left alone: %w", err)
	}
	lines := strings.SplitAfter(string(original), "\n")
	var kept []string
	removed, cutting := 0, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			cutting = false
			if dir, ok := projectHeader(trimmed); ok && isUnder(filepath.Clean(dir), root) {
				cutting = true
				removed++
				kept = trimBlankTail(kept)
				continue
			}
		}
		if !cutting {
			kept = append(kept, line)
		}
	}
	if removed == 0 {
		return original, 0, nil
	}
	updated := []byte(strings.Join(kept, ""))
	var after codexConfig
	if err := toml.Unmarshal(updated, &after); err != nil {
		return nil, 0, fmt.Errorf("removing the scratch folders from config.toml would not parse, so it was left alone: %w", err)
	}
	for dir := range before.Projects {
		_, still := after.Projects[dir]
		if isUnder(filepath.Clean(dir), root) == still {
			return nil, 0, fmt.Errorf("config.toml would not read back as expected for %s, so it was left alone", dir)
		}
	}
	return updated, removed, nil
}

// projectHeader parses `[projects."<dir>"]` (the form Codex and crowsnest write) into dir.
func projectHeader(line string) (string, bool) {
	inner, ok := strings.CutPrefix(line, "[projects.")
	if !ok || !strings.HasSuffix(inner, "]") {
		return "", false
	}
	dir, err := strconv.Unquote(strings.TrimSuffix(inner, "]"))
	return dir, err == nil
}

// trimBlankTail drops the blank lines that separated the removed table from the one before.
func trimBlankTail(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = slices.Delete(lines, len(lines)-1, len(lines))
	}
	if len(lines) > 0 {
		lines = append(lines, "\n")
	}
	return lines
}

// ForgetScratch is ForgetUnder over the user's own agent files (from $HOME, $CLAUDE_CONFIG_DIR and
// $CODEX_HOME): what a real-agent test calls at teardown for its one scratch folder. It reports
// what it removed, for the test's log.
func ForgetScratch(dir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	claude, codex, err := ForgetUnder(DefaultPaths(home, os.Getenv), dir, home)
	return fmt.Sprintf("removed %d Claude and %d Codex folder record(s) under %s", claude, codex, dir), err
}
