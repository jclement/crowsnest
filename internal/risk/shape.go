package risk

// Known-benign shapes (R5-S7, R5-C03). "Low" used to mean "matched no rule in the table", so any
// command the table did not foresee (an interpreter one-liner, find -delete, a git global option
// in front of reset) was low, and low is one tap from a lock screen. Now low means the call
// matched a shape on this list, read after quoting, environment prefixes, wrappers, `sh -c`
// strings and git's global options are taken apart (guard.Parse). Everything else is at least
// elevated: answerable in the app, never from a notification. The table in risk.go still raises
// a call to dangerous; this list can only keep one at low.

import (
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
	"mkdir": true, "touch": true, "cd": true, "pushd": true, "popd": true, "export": true, "ping": true, "gofmt": true,
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
	"fd":    {"-x", "-X", "--exec", "--exec-batch"},
	"rg":    {"--pre"},
	"sort":  {"-o", "--output", "--compress-program"},
	"tree":  {"-o"},
	"go":    {"-exec", "-toolexec", "-vettool", "-o", "-modfile", "-overlay"},
	"cargo": {"--config", "-Z"},
	"date":  {"-s", "--set"},
}

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

// commandShape returns why one simple command is not benign, or "" when it is.
func commandShape(cmd guard.Command) string {
	program, args := cmd.Program, cmd.Args
	if target := outputRedirect(args); target != "" {
		return "Writes " + target + " through a redirect"
	}
	if name := unsafeEnv(cmd.Env); name != "" {
		return "Sets " + name + " for the command, which can change what it runs"
	}
	if opt := optionHit(args, programWrites[program]); opt != "" {
		return program + " " + opt + " runs another program or writes a file"
	}
	switch {
	case program == "":
		return ""
	case program == "git":
		return gitShape(guard.ParseGit(cmd))
	case program == "sed" && hasAnyPrefix(args, "-i", "--in-place"):
		return "sed -i edits files in place"
	case program == "find" && hasAny(args, findWrites...):
		return "find with an action that deletes, runs or writes (" + firstOf(args, findWrites...) + ")"
	case program == "uniq" && len(operands(args)) > 1:
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
	case buildTools[program] != nil && len(args) > 0 && buildTools[program][args[0]]:
		return ""
	case (program == "npm" || program == "pnpm" || program == "yarn" || program == "bun" || program == "mise") && len(args) > 1 && args[0] == "run" && scriptName(args[1]):
		return ""
	case program == "mise" && len(args) > 0 && (args[0] == "tasks" || args[0] == "ls"):
		return ""
	}
	return "Runs " + program + ", which is not on crowsnest's list of harmless commands"
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
// whether the command may stay low.
func gitShape(g guard.Git) string {
	if opt := gitOption(g); opt != "" {
		return "git " + g.Sub + " " + opt + " writes a file or runs another program"
	}
	switch {
	case len(g.Config) > 0:
		return "git with -c " + g.Config[0] + ": configuration on the command line can change what git runs (an alias, a pager, a hook path)"
	case g.ExecPath:
		return "git with --exec-path: it runs other programs as git's own"
	case g.Sub == "":
		return "git with no subcommand crowsnest can read"
	case gitReadOnly[g.Sub]:
		return ""
	case g.Sub == "branch" && !hasAny(g.Args, "-d", "-D", "--delete", "-m", "-M", "--move", "-f", "--force", "-c", "-C", "--copy"):
		return ""
	case g.Sub == "tag" && (len(g.Args) == 0 || hasAny(g.Args, "-l", "--list")):
		return ""
	case g.Sub == "remote" && (len(g.Args) == 0 || hasAny(g.Args, "-v", "--verbose", "show", "get-url")):
		return ""
	case g.Sub == "config" && hasAny(g.Args, "--get", "--get-all", "--get-regexp", "--list", "-l"):
		return ""
	case g.Sub == "stash" && !hasAny(g.Args, "drop", "clear"):
		return ""
	case g.Sub == "switch" && !hasAny(g.Args, "-f", "--force", "--discard-changes", "-C", "--force-create"):
		return ""
	case g.Sub == "checkout" && hasAny(g.Args, "-b") && !hasAny(g.Args, "-f", "--force", "--"):
		return ""
	case g.Sub == "worktree" && len(g.Args) > 0 && g.Args[0] == "list":
		return ""
	}
	return "git " + g.Sub + " is not on crowsnest's list of harmless git commands"
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
