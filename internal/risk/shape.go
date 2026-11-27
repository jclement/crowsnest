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

// commandShape returns why one simple command is not benign, or "" when it is.
func commandShape(cmd guard.Command) string {
	program, args := cmd.Program, cmd.Args
	switch {
	case program == "":
		return ""
	case program == "git":
		return gitShape(guard.ParseGit(cmd))
	case program == "sed" && hasAnyPrefix(args, "-i", "--in-place"):
		return "sed -i edits files in place"
	case program == "find" && hasAny(args, findWrites...):
		return "find with an action that deletes, runs or writes (" + firstOf(args, findWrites...) + ")"
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

// gitShape returns why a git command is not benign, or "". The dangerous and elevated rules of
// the table are matched against the plain spelling separately (Classify); this decides only
// whether the command may stay low.
func gitShape(g guard.Git) string {
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
