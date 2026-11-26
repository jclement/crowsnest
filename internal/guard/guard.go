// Package guard decides the PreToolUse guard hook (DESIGN.md 6.3, 8.7): a small set of shell
// commands no agent crowsnest starts may run unattended, refused outright with a reason the agent
// is told. It runs on every Bash call, so it decides locally from a rules file crowsnest writes
// when it starts the agent (guard.json beside settings.json), with no server round trip.
//
// The rules:
//
//   - protected-host: a command that could reach a protected host: it runs a network program
//     (ssh, curl, ansible, psql, …) with the host in its arguments, or writes the host as an
//     address (user@host, scheme://host, host:port, host:/path). The match is on the host name as
//     a whole word, case-insensitive (nyc1 matches server-nyc1 and nyc1.internal, not nyc10). A
//     bare mention, in a commit message or a note, is not refused.
//   - protected-branch: git commit, merge, cherry-pick, revert, am or rebase while a protected
//     branch is checked out, and git push onto one (named in the refspec, or the current branch
//     when none is named).
//   - force-push: git push --force (-f, --force-with-lease, a +refspec) onto a protected branch.
//   - herdr-server-stop: herdr server stop, and herdr session stop or delete.
//   - other-session: herdr or crowsnest aimed at a herdr session other than the agent's own
//     (--session X, --session=X).
//
// This is a seatbelt, not a sandbox: a script, an alias, eval or an interpreter one-liner gets
// past it. It exists so an agent that means well does not do one of these by accident, and so
// one that is told to is stopped and has to ask the human.
package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Rules are what the hook reads from its rules file.
type Rules struct {
	// Session is the agent's own herdr session; herdr and crowsnest aimed at any other are refused.
	Session string `json:"session"`
	// ProtectedHosts are host names no command may reach.
	ProtectedHosts []string `json:"protected_hosts"`
	// ProtectedBranches take no commit or push.
	ProtectedBranches []string `json:"protected_branches"`
}

// Verdict is the hook's decision: Deny with the rule and the reason the agent is told.
type Verdict struct {
	Deny   bool   `json:"deny"`
	Rule   string `json:"rule,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Rule names.
const (
	RuleProtectedHost   = "protected-host"
	RuleProtectedBranch = "protected-branch"
	RuleForcePush       = "force-push"
	RuleHerdrStop       = "herdr-server-stop"
	RuleOtherSession    = "other-session"
)

// tail is appended to every refusal: the agent is told what to do instead.
const tail = " This is refused by crowsnest's guard and cannot be approved from here. If it really is needed, " +
	"stop and ask the human (the ask tool), saying exactly what you want to run and why."

// BranchOf reports the branch checked out in a directory ("" when unknown). Check calls it only
// for a git command that needs it, with the directory a `git -C` named, else "".
type BranchOf func(dir string) string

// Check decides one shell command.
func Check(rules Rules, command string, branchOf BranchOf) Verdict {
	for _, segment := range Segments(command) {
		if verdict := checkSegment(rules, segment, branchOf); verdict.Deny {
			return verdict
		}
	}
	if host := addressedHost(rules.ProtectedHosts, command); host != "" {
		return Verdict{Deny: true, Rule: RuleProtectedHost, Reason: fmt.Sprintf("This command addresses %s, a protected (production) host.", host) + tail}
	}
	return Verdict{}
}

func checkSegment(rules Rules, words []string, branchOf BranchOf) Verdict {
	program, args := commandOf(words)
	if program == "" {
		return Verdict{}
	}
	switch program {
	case "herdr":
		if verdict := checkHerdr(rules, args); verdict.Deny {
			return verdict
		}
	case "crowsnest":
		if session, ok := sessionFlag(args); ok && session != rules.Session {
			return Verdict{Deny: true, Rule: RuleOtherSession, Reason: fmt.Sprintf("crowsnest is aimed at the herdr session %q, not this agent's own (%q).", session, rules.Session) + tail}
		}
	case "git":
		if verdict := checkGit(rules, args, branchOf); verdict.Deny {
			return verdict
		}
	}
	if networkPrograms[program] {
		for _, arg := range args {
			if host := HostNamed(rules.ProtectedHosts, arg); host != "" {
				return Verdict{Deny: true, Rule: RuleProtectedHost, Reason: fmt.Sprintf("%s would reach %s, a protected (production) host.", program, host) + tail}
			}
		}
	}
	return Verdict{}
}

func checkHerdr(rules Rules, args []string) Verdict {
	positional := positionals(args, map[string]bool{"--session": true, "--machine": true, "--config": true})
	if len(positional) >= 2 && positional[0] == "server" && positional[1] == "stop" {
		return Verdict{Deny: true, Rule: RuleHerdrStop, Reason: "herdr server stop would stop every agent in the session, this one included." + tail}
	}
	if len(positional) >= 2 && positional[0] == "session" && (positional[1] == "stop" || positional[1] == "delete" || positional[1] == "rm" || positional[1] == "kill") {
		return Verdict{Deny: true, Rule: RuleHerdrStop, Reason: "herdr session " + positional[1] + " stops or deletes a whole herdr session." + tail}
	}
	if session, ok := sessionFlag(args); ok && session != rules.Session {
		return Verdict{Deny: true, Rule: RuleOtherSession, Reason: fmt.Sprintf("herdr is aimed at the session %q, not this agent's own (%q).", session, rules.Session) + tail}
	}
	return Verdict{}
}

// gitWritesHere are the git subcommands that add commits to the checked-out branch.
var gitWritesHere = map[string]bool{"commit": true, "merge": true, "cherry-pick": true, "revert": true, "am": true, "rebase": true}

func checkGit(rules Rules, args []string, branchOf BranchOf) Verdict {
	if len(rules.ProtectedBranches) == 0 {
		return Verdict{}
	}
	dir, sub, rest := gitSubcommand(args)
	current := func() string {
		if branchOf == nil {
			return ""
		}
		return branchOf(dir)
	}
	switch {
	case gitWritesHere[sub]:
		if branch := current(); branch != "" && contains(rules.ProtectedBranches, branch) {
			return Verdict{Deny: true, Rule: RuleProtectedBranch, Reason: fmt.Sprintf("git %s while %s is checked out: %s is a protected branch and takes no direct commits.", sub, branch, branch) + tail}
		}
	case sub == "push":
		force := false
		var refspecs []string
		positional := 0
		for _, arg := range rest {
			switch {
			case arg == "--force" || arg == "-f" || strings.HasPrefix(arg, "--force-with-lease") || arg == "--force-if-includes":
				force = true
			case strings.HasPrefix(arg, "-") && len(arg) > 1 && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "f"):
				force = true // combined short flags, -uf
			case strings.HasPrefix(arg, "-"):
			default:
				positional++
				if positional > 1 { // the first is the remote
					refspecs = append(refspecs, arg)
				}
			}
		}
		var targets []string
		for _, spec := range refspecs {
			if strings.HasPrefix(spec, "+") {
				force = true
				spec = spec[1:]
			}
			destination := spec
			if _, after, ok := strings.Cut(spec, ":"); ok {
				destination = after
			}
			targets = append(targets, strings.TrimPrefix(destination, "refs/heads/"))
		}
		if len(refspecs) == 0 {
			targets = append(targets, current())
		}
		for _, target := range targets {
			if target != "" && contains(rules.ProtectedBranches, target) {
				if force {
					return Verdict{Deny: true, Rule: RuleForcePush, Reason: fmt.Sprintf("A force push onto %s, a protected branch, rewrites its history.", target) + tail}
				}
				return Verdict{Deny: true, Rule: RuleProtectedBranch, Reason: fmt.Sprintf("A push onto %s: it is a protected branch, reached only through a pull request.", target) + tail}
			}
		}
	}
	return Verdict{}
}

// gitSubcommand skips git's global options (-C dir, -c key=value, --git-dir=…) and returns the
// directory a -C named, the subcommand and its arguments.
func gitSubcommand(args []string) (dir, sub string, rest []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-C" && i+1 < len(args):
			dir = filepath.Join(dir, args[i+1])
			i++
		case arg == "-c" && i+1 < len(args):
			i++
		case strings.HasPrefix(arg, "-"):
		default:
			return dir, arg, args[i+1:]
		}
	}
	return dir, "", nil
}

// networkPrograms reach other machines; a protected host in their arguments is refused.
var networkPrograms = map[string]bool{
	"ssh": true, "scp": true, "sftp": true, "rsync": true, "mosh": true, "curl": true, "wget": true, "http": true, "https": true,
	"xh": true, "ansible": true, "ansible-playbook": true, "ansible-console": true, "psql": true, "pg_dump": true, "pg_restore": true,
	"mysql": true, "mysqldump": true, "mongo": true, "mongosh": true, "redis-cli": true, "nc": true, "ncat": true, "netcat": true,
	"telnet": true, "ftp": true, "lftp": true, "kubectl": true, "helm": true, "docker": true, "podman": true, "ping": true,
	"sqlcmd": true, "ldapsearch": true, "socat": true,
}

// HostNamed returns the first protected host named in text as a whole word ("" for none): not
// preceded or followed by a letter or digit, so nyc1 matches server-nyc1, user@nyc1 and
// nyc1.internal but not nyc10.
func HostNamed(hosts []string, text string) string {
	lower := strings.ToLower(text)
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		for start := 0; ; {
			index := strings.Index(lower[start:], host)
			if index < 0 {
				break
			}
			at := start + index
			end := at + len(host)
			if (at == 0 || !isAlnum(lower[at-1])) && (end == len(lower) || !isAlnum(lower[end])) {
				return host
			}
			start = at + 1
		}
	}
	return ""
}

func isAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// addressedHost finds a protected host written as an address anywhere in the command:
// user@host, scheme://host, host:port or host:/path.
func addressedHost(hosts []string, command string) string {
	lower := strings.ToLower(command)
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		pattern := `(@|://)[a-z0-9.-]*` + regexp.QuoteMeta(host) + `([^a-z0-9]|$)|(^|[^a-z0-9])` + regexp.QuoteMeta(host) + `(\.[a-z0-9.-]+)?:(\d|/)`
		if regexp.MustCompile(pattern).MatchString(lower) {
			return host
		}
	}
	return ""
}

// commandOf returns a segment's program (its base name) and arguments, skipping variable
// assignments and the wrappers that run another command (sudo, env, nohup, …).
func commandOf(words []string) (string, []string) {
	for i := 0; i < len(words); i++ {
		word := words[i]
		if strings.Contains(word, "=") && !strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "=") {
			continue // VAR=value
		}
		base := strings.ToLower(filepath.Base(word))
		switch base {
		case "sudo", "env", "nohup", "time", "exec", "command", "builtin", "nice", "doas", "xargs", "timeout", "caffeinate":
			// Skip the wrapper's own options too (sudo -u root, env -u NAME: a short guess).
			for i+1 < len(words) && strings.HasPrefix(words[i+1], "-") {
				i++
				if wrapperValued[words[i]] && i+1 < len(words) {
					i++
				}
			}
			if base == "timeout" && i+1 < len(words) {
				i++
			}
			continue
		}
		return base, words[i+1:]
	}
	return "", nil
}

// wrapperValued are wrapper options that take a value (sudo -u root, env -u NAME).
var wrapperValued = map[string]bool{"-u": true, "-g": true, "-p": true, "-C": true, "-h": true, "-U": true, "-r": true, "-t": true, "-n": true}

// sessionFlag finds --session X or --session=X.
func sessionFlag(args []string) (string, bool) {
	for i, arg := range args {
		if arg == "--session" && i+1 < len(args) {
			return args[i+1], true
		}
		if value, ok := strings.CutPrefix(arg, "--session="); ok {
			return value, true
		}
	}
	return "", false
}

// positionals drops flags (and the values of the flags in valued) from args.
func positionals(args []string, valued map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if valued[args[i]] {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// ---- The rules file ----------------------------------------------------------------

// Load reads a rules file.
func Load(path string) (Rules, error) {
	body, err := os.ReadFile(path) //nolint:gosec // crowsnest names its own rules file on the hook's command line
	if err != nil {
		return Rules{}, err
	}
	var rules Rules
	if err := json.Unmarshal(body, &rules); err != nil {
		return Rules{}, fmt.Errorf("guard rules %s: %w", path, err)
	}
	return rules, nil
}

// ReadHostsFile reads a protected-hosts file: one host per line, # comments. A missing file is
// no hosts, not an error.
func ReadHostsFile(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path) //nolint:gosec // a configured path
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, line := range strings.Split(string(body), "\n") {
		if before, _, found := strings.Cut(line, "#"); found {
			line = before
		}
		if line = strings.TrimSpace(line); line != "" {
			hosts = append(hosts, line)
		}
	}
	return hosts, nil
}
