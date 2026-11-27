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

// BranchOf reports the branch checked out in a directory ("" when unknown). dir is relative to
// the hook's working directory ("" for that directory itself) or absolute. Check calls it only
// for a git command that needs it.
type BranchOf func(dir string) string

// Check decides one shell command. The commands are read in order (Parse), and a walk follows
// what earlier ones change for later ones (R5-C02): a cd moves the directory the next git command
// runs in, and a branch switch changes the branch the next commit lands on. Where that cannot be
// followed (a cd to a variable, a cd inside a subshell, a repository chosen with --git-dir or
// GIT_DIR, a git alias defined on the command line), a commit or push is refused while protected
// branches are configured: the agent can run it plainly instead.
func Check(rules Rules, command string, branchOf BranchOf) Verdict {
	commands, _ := Parse(command)
	walk := walker{rules: rules, branchOf: branchOf, dirKnown: true, switched: map[string]switchTarget{}}
	for _, cmd := range commands {
		if verdict := walk.check(cmd); verdict.Deny {
			return verdict
		}
	}
	if host := addressedHost(rules.ProtectedHosts, command); host != "" {
		return Verdict{Deny: true, Rule: RuleProtectedHost, Reason: fmt.Sprintf("This command addresses %s, a protected (production) host.", host) + tail}
	}
	return Verdict{}
}

// walker carries what earlier commands of one line changed.
type walker struct {
	rules    Rules
	branchOf BranchOf
	// dir is where the next command runs, relative to the hook's directory ("" for it) or
	// absolute; dirKnown is false once a cd could not be followed.
	dir      string
	dirKnown bool
	// switched is the branch a checkout or switch earlier in the line moved a directory to.
	switched map[string]switchTarget
}

// switchTarget is a branch an earlier command switched to. created is true for -b/-c, which
// makes the branch certain; a plain `checkout name` might name a file, so the branch checked out
// before it is weighed too. unknown is true for a switch whose target cannot be told.
type switchTarget struct {
	branch  string
	created bool
	unknown bool
}

func (w *walker) check(cmd Command) Verdict {
	switch cmd.Program {
	case "":
		return Verdict{}
	case "cd", "pushd", "popd":
		w.changeDir(cmd)
		return Verdict{}
	case "herdr":
		if verdict := checkHerdr(w.rules, cmd.Args); verdict.Deny {
			return verdict
		}
	case "crowsnest":
		if session, ok := sessionFlag(cmd.Args); ok && session != w.rules.Session {
			return Verdict{Deny: true, Rule: RuleOtherSession, Reason: fmt.Sprintf("crowsnest is aimed at the herdr session %q, not this agent's own (%q).", session, w.rules.Session) + tail}
		}
	case "git":
		if verdict := w.checkGit(ParseGit(cmd)); verdict.Deny {
			return verdict
		}
	}
	if networkPrograms[cmd.Program] {
		for _, arg := range cmd.Args {
			if host := HostNamed(w.rules.ProtectedHosts, arg); host != "" {
				return Verdict{Deny: true, Rule: RuleProtectedHost, Reason: fmt.Sprintf("%s would reach %s, a protected (production) host.", cmd.Program, host) + tail}
			}
		}
	}
	return Verdict{}
}

// changeDir follows a cd: a plain path moves the directory; anything else (no argument, -, a
// variable, ~, popd, a cd inside a subshell, which ends with it) makes it unknown.
func (w *walker) changeDir(cmd Command) {
	args := positionals(cmd.Args, nil)
	if cmd.Subshell || cmd.Program == "popd" || len(args) != 1 || strings.ContainsAny(args[0], "$~`*?") || args[0] == "-" {
		w.dirKnown = false
		return
	}
	w.dir = joinDir(w.dir, args[0])
}

func joinDir(base, dir string) string {
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(base, dir)
}

// gitWritesHere are the git subcommands that add commits to the checked-out branch.
var gitWritesHere = map[string]bool{"commit": true, "merge": true, "cherry-pick": true, "revert": true, "am": true, "rebase": true, "pull": true}

func (w *walker) checkGit(g Git) Verdict {
	if len(w.rules.ProtectedBranches) == 0 {
		return Verdict{}
	}
	dir := joinDir(w.dir, g.Dir)
	for _, config := range g.Config {
		if strings.HasPrefix(strings.ToLower(config), "alias.") {
			return w.uncertain("git with an alias defined on the command line (-c " + config + ")")
		}
	}
	switch {
	case g.Sub == "checkout" || g.Sub == "switch":
		w.switched[dir] = parseSwitch(g)
	case gitWritesHere[g.Sub]:
		branches, certain := w.branchesIn(g, dir)
		if !certain {
			return w.uncertain("git " + g.Sub)
		}
		for _, branch := range branches {
			if contains(w.rules.ProtectedBranches, branch) {
				return Verdict{Deny: true, Rule: RuleProtectedBranch, Reason: fmt.Sprintf("git %s while %s is checked out: %s is a protected branch and takes no direct commits.", g.Sub, branch, branch) + tail}
			}
		}
	case g.Sub == "push":
		return w.checkPush(g, dir)
	}
	return Verdict{}
}

// branchesIn lists the branches a write in dir could land on: the one checked out, or the one an
// earlier switch moved to (and, for a switch that may have named a file, the one before it too).
// certain is false when that cannot be told.
func (w *walker) branchesIn(g Git, dir string) (branches []string, certain bool) {
	if g.Repo || !w.dirKnown {
		return nil, false
	}
	target, switched := w.switched[dir]
	switch {
	case switched && target.unknown:
		return nil, false
	case switched && target.created:
		return []string{target.branch}, true
	case switched:
		branches = append(branches, target.branch)
	}
	if w.branchOf != nil {
		if current := w.branchOf(dir); current != "" {
			branches = append(branches, current)
		}
	}
	return branches, true
}

// parseSwitch reads `git checkout|switch …`: the branch it moves to, if it can be told.
func parseSwitch(g Git) switchTarget {
	var positional []string
	for i := 0; i < len(g.Args); i++ {
		arg := g.Args[i]
		switch {
		case arg == "--":
			return switchTarget{} // checkout -- paths: no branch change
		case arg == "-b" || arg == "-B" || arg == "-c" || arg == "-C" || arg == "--orphan":
			if i+1 < len(g.Args) {
				return switchTarget{branch: g.Args[i+1], created: true}
			}
			return switchTarget{unknown: true}
		case arg == "-":
			return switchTarget{unknown: true}
		case strings.HasPrefix(arg, "-"):
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) == 0 {
		return switchTarget{}
	}
	return switchTarget{branch: positional[0]}
}

func (w *walker) checkPush(g Git, dir string) Verdict {
	force := false
	var refspecs []string
	positional := 0
	for _, arg := range g.Args {
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
		branches, certain := w.branchesIn(g, dir)
		if !certain {
			return w.uncertain("git push")
		}
		targets = branches
	}
	for _, target := range targets {
		if target != "" && contains(w.rules.ProtectedBranches, target) {
			if force {
				return Verdict{Deny: true, Rule: RuleForcePush, Reason: fmt.Sprintf("A force push onto %s, a protected branch, rewrites its history.", target) + tail}
			}
			return Verdict{Deny: true, Rule: RuleProtectedBranch, Reason: fmt.Sprintf("A push onto %s: it is a protected branch, reached only through a pull request.", target) + tail}
		}
	}
	return Verdict{}
}

// uncertain refuses a write whose branch cannot be told while protected branches are configured.
func (w *walker) uncertain(what string) Verdict {
	return Verdict{Deny: true, Rule: RuleProtectedBranch, Reason: what + ": crowsnest cannot tell which branch this lands on (an earlier cd, " +
		"a branch switch, --git-dir or GIT_DIR, or an inline alias hides it), and some branches here are protected. " +
		"Run it on its own, in the repository's directory, with plain git." + tail}
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
