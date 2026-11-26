// The guard hook's and the approvals' settings (DESIGN.md 3, 8.7). In their own file and loader
// step, as upkeep's and the attachments' are.

package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Option names for the guard.
const (
	optProtectedHost      = "protected-host"
	optProtectedBranch    = "protected-branch"
	optProtectedHostsFile = "protected-hosts-file"
)

// DefaultProtectedHostsFile is claude-agent's list of production hosts, read when it exists.
const DefaultProtectedHostsFile = "~/Developer/claude-agent/config/protected-hosts"

func init() {
	options = append(options,
		option{name: optProtectedHost, env: []string{"CROWSNEST_PROTECTED_HOSTS"}, kind: kindList, usage: "a production host no agent may reach unattended: the guard refuses commands that would, and approvals naming it are dangerous (repeatable)"},
		option{name: optProtectedHostsFile, env: []string{"CROWSNEST_PROTECTED_HOSTS_FILE"}, def: DefaultProtectedHostsFile, usage: "file of protected hosts, one per line, # comments; read when it exists ('' for none)"},
		option{name: optProtectedBranch, env: []string{"CROWSNEST_PROTECTED_BRANCHES"}, kind: kindList, usage: "a branch agents may not commit or push onto, nor force-push (repeatable; default none)"},
	)
}

func (l *loader) loadGuard(cfg *Config) error {
	for _, host := range l.list(optProtectedHost) {
		if host = strings.TrimSpace(host); host != "" {
			cfg.ProtectedHosts = append(cfg.ProtectedHosts, host)
		}
	}
	for _, branch := range l.list(optProtectedBranch) {
		if branch = strings.TrimSpace(branch); branch != "" {
			cfg.ProtectedBranches = append(cfg.ProtectedBranches, branch)
		}
	}
	cfg.ProtectedHostsFile = expandTilde(strings.TrimSpace(l.str(optProtectedHostsFile)))
	return nil
}

// expandTilde turns a leading ~/ into the home directory.
func expandTilde(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return path
}
