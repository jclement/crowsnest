package guard

// Reading a shell command the way the risk classifier and the guard both need it (R5-C02, R5-C03,
// R5-S7): every simple command with its quotes removed, its environment prefix and wrappers
// (sudo, env, command, exec, nohup, …) set aside, `sh -c '…'` and `eval '…'` strings read as the
// commands they contain, and git's global options (-C, -c, --git-dir, --work-tree, …) resolved so
// that the subcommand is the real one whatever spelling was used. It is a reader, not a shell:
// nothing is expanded, and when it cannot read a command with confidence (an unterminated quote,
// nesting too deep) it says so, and both callers then treat the command as risky rather than
// harmless.

import (
	"path/filepath"
	"strings"
)

// Command is one simple command.
type Command struct {
	// Program is the base name of the program run ("git", "rm"), after wrappers; "" for a
	// segment that is only assignments.
	Program string
	// Args are its arguments, quotes removed.
	Args []string
	// Env are the VAR=value assignments in front of it (and given to env), by name.
	Env map[string]string
	// Wrappers are the wrapper programs it was run through, in order (sudo, env, …).
	Wrappers []string
	// Subshell is true when the command runs inside ( … ), so a cd in it does not outlast it.
	Subshell bool
	// Nested is true for a command read out of an `sh -c` or `eval` string.
	Nested bool
	// Scope numbers the shell the command runs in: 0 for the line itself, a new number for each
	// `sh -c` string, whose cd ends with it (R6-C04); Parent is the scope that started it. eval
	// runs in its caller's shell, so its commands keep the caller's scope.
	Scope, Parent int
}

// maxNesting bounds `sh -c "sh -c '…'"`: deeper than this is not read with confidence.
const maxNesting = 4

// Parse reads a command line into its simple commands, recursing into `sh -c` and `eval`
// strings. ok is false when the line cannot be read with confidence; the commands returned are
// then a best effort and must not be taken as the whole story.
func Parse(command string) (commands []Command, ok bool) {
	scopes := 0
	return parse(command, 0, 0, 0, &scopes)
}

func parse(command string, depth, scope, parent int, scopes *int) ([]Command, bool) {
	if depth > maxNesting {
		return nil, false
	}
	segments, ok := split(command)
	var out []Command
	for _, segment := range segments {
		cmd := commandOfSegment(segment.words)
		cmd.Subshell, cmd.Nested, cmd.Scope, cmd.Parent = segment.subshell, depth > 0, scope, parent
		if inner, isShell := shellString(cmd); isShell {
			if inner == "" {
				ok = false // a shell reading a script or stdin: what it runs cannot be seen
				out = append(out, cmd)
				continue
			}
			innerScope, innerParent := scope, parent
			if cmd.Program != "eval" {
				*scopes++
				innerScope, innerParent = *scopes, scope
			}
			nested, innerOK := parse(inner, depth+1, innerScope, innerParent, scopes)
			ok = ok && innerOK
			out = append(out, nested...)
			continue
		}
		out = append(out, cmd)
	}
	return out, ok
}

// shells are the programs whose -c string is another command line.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "ash": true, "fish": true}

// shellString returns the command line a shell or eval runs: the -c string of sh, bash, …, or
// eval's arguments joined. isShell is false for any other program; inner is "" when a shell runs
// something that cannot be read here (a script file, or its standard input).
func shellString(cmd Command) (inner string, isShell bool) {
	switch {
	case cmd.Program == "eval":
		return strings.Join(cmd.Args, " "), true
	case !shells[cmd.Program]:
		return "", false
	}
	for i, arg := range cmd.Args {
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			return "", true // a script file (or nothing past the options: stdin)
		}
		if !strings.HasPrefix(arg, "--") && strings.Contains(arg, "c") { // -c, -lc, -ec
			if i+1 < len(cmd.Args) {
				return cmd.Args[i+1], true
			}
			return "", true
		}
	}
	return "", true
}

// commandOfSegment sets aside a segment's assignments and wrappers.
func commandOfSegment(words []string) Command {
	cmd := Command{Env: map[string]string{}}
	for i := 0; i < len(words); i++ {
		word := words[i]
		if name, value, isAssign := assignment(word); isAssign {
			cmd.Env[name] = value
			continue
		}
		base := strings.ToLower(filepath.Base(word))
		if wrappers[base] {
			cmd.Wrappers = append(cmd.Wrappers, base)
			i = skipWrapperOptions(base, words, i)
			continue
		}
		cmd.Program, cmd.Args = base, words[i+1:]
		return cmd
	}
	return cmd
}

// wrappers run the command that follows them.
var wrappers = map[string]bool{
	"sudo": true, "env": true, "nohup": true, "time": true, "exec": true, "command": true, "builtin": true, "nice": true,
	"doas": true, "xargs": true, "timeout": true, "caffeinate": true, "heavy": true, "stdbuf": true, "ionice": true,
}

// skipWrapperOptions steps over a wrapper's own options (sudo -u root, env -u NAME, timeout 10)
// and returns the index of the last word consumed.
func skipWrapperOptions(base string, words []string, i int) int {
	for i+1 < len(words) && strings.HasPrefix(words[i+1], "-") {
		i++
		if wrapperValued[words[i]] && i+1 < len(words) {
			i++
		}
	}
	if base == "timeout" && i+1 < len(words) {
		i++ // the duration
	}
	return i
}

// assignment reports whether word is VAR=value.
func assignment(word string) (name, value string, ok bool) {
	name, value, found := strings.Cut(word, "=")
	if !found || name == "" {
		return "", "", false
	}
	for i, r := range name {
		letter := r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
		digit := i > 0 && r >= '0' && r <= '9'
		if !letter && !digit {
			return "", "", false
		}
	}
	return name, value, true
}

// ---- Splitting -----------------------------------------------------------------------------

// segment is one simple command's words, and whether it sits inside ( … ).
type segment struct {
	words    []string
	subshell bool
}

// split cuts a command line into simple commands. It splits on unquoted ; & | newlines,
// parentheses and backticks and at $( (a command substitution is read as its own command),
// honours single and double quotes and backslashes, and expands nothing. ok is false for an
// unterminated quote or an unbalanced parenthesis.
func split(command string) ([]segment, bool) {
	s := splitter{}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		i = s.step(runes, i)
	}
	s.endSegment()
	return s.segments, s.quote == 0 && s.depth == 0 && len(s.resume) == 0
}

type splitter struct {
	segments []segment
	words    []string
	word     strings.Builder
	inWord   bool
	quote    rune
	// resume holds the quote to return to at each ) that closes a $( (inside double quotes,
	// the substitution's words are split like any command's).
	resume []rune
	// depth counts the ( … ) subshells open around the current position.
	depth int
}

func (s *splitter) endWord() {
	if s.inWord {
		s.words = append(s.words, s.word.String())
		s.word.Reset()
		s.inWord = false
	}
}

func (s *splitter) endSegment() {
	s.endWord()
	if len(s.words) > 0 {
		s.segments = append(s.segments, segment{words: s.words, subshell: s.depth > 0})
		s.words = nil
	}
}

// step consumes the rune at i (and any it implies) and returns the index of the last one used.
func (s *splitter) step(runes []rune, i int) int {
	r := runes[i]
	switch {
	case s.quote == '\'':
		if r == '\'' {
			s.quote = 0
		} else {
			s.word.WriteRune(r)
		}
	case s.quote == '"':
		return s.stepDouble(runes, i)
	case r == '\'' || r == '"':
		s.quote = r
		s.inWord = true
	case r == '\\' && i+1 < len(runes):
		i++
		if runes[i] != '\n' {
			s.word.WriteRune(runes[i])
			s.inWord = true
		}
	case r == '$' && i+1 < len(runes) && runes[i+1] == '(':
		s.endSegment()
		i++
		s.resume = append(s.resume, 0)
	case r == ')' && len(s.resume) > 0:
		s.endSegment()
		s.quote = s.resume[len(s.resume)-1]
		s.resume = s.resume[:len(s.resume)-1]
	case r == '(':
		s.endSegment()
		s.depth++
	case r == ')':
		s.endSegment()
		s.depth--
		if s.depth < 0 {
			s.depth = 0
			s.quote = -1 // unbalanced: reported as not confident
		}
	case r == '&' && s.inWord && (strings.HasSuffix(s.word.String(), ">") || strings.HasSuffix(s.word.String(), "<")),
		r == '&' && i+1 < len(runes) && runes[i+1] == '>':
		// Part of a redirection (2>&1, >&2, <&3, &>file), not a background or list separator.
		s.word.WriteRune(r)
		s.inWord = true
	case strings.ContainsRune(";&|\n`", r):
		s.endSegment()
	case r == '>' && s.inWord && strings.Trim(s.word.String(), "0123456789&>") != "":
		// An unquoted > ends the word before it (R6-S2): `x>>~/.zshrc` is the word x and the
		// redirection >>~/.zshrc, as the shell reads it. A descriptor number (2>) stays with it.
		s.endWord()
		s.word.WriteRune(r)
		s.inWord = true
	case r == ' ' || r == '\t' || r == '\r':
		s.endWord()
	default:
		s.word.WriteRune(r)
		s.inWord = true
	}
	return i
}

func (s *splitter) stepDouble(runes []rune, i int) int {
	r := runes[i]
	switch {
	case r == '"':
		s.quote = 0
	case r == '\\' && i+1 < len(runes) && strings.ContainsRune("\"\\$`\n", runes[i+1]):
		i++
		s.word.WriteRune(runes[i])
	case r == '$' && i+1 < len(runes) && runes[i+1] == '(':
		s.endSegment()
		i++
		s.resume = append(s.resume, '"')
		s.quote = 0
	case r == '`':
		s.endSegment()
	default:
		s.word.WriteRune(r)
	}
	return i
}

// ---- Git -----------------------------------------------------------------------------------

// Git is a git command with its global options resolved.
type Git struct {
	// Dir is the directory the -C options name, joined in order ("" for none).
	Dir string
	// Repo is true when the repository is chosen some other way than the working directory and
	// -C: --git-dir, --work-tree, or GIT_DIR / GIT_WORK_TREE in the environment. Which branch a
	// write lands on cannot then be told from the directory.
	Repo bool
	// Config are the -c key=value options (and --config-env), which can change what any
	// subcommand runs (an alias, a pager, a hook path).
	Config []string
	// ExecPath is true when --exec-path points git at other programs.
	ExecPath bool
	// Sub is the subcommand, Args what follows it.
	Sub  string
	Args []string
}

// gitValued are git's global options that take the next word as their value.
var gitValued = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true, "--super-prefix": true, "--config-env": true, "--exec-path": true, "--list-cmds": true, "--attr-source": true}

// ParseGit resolves a git command's global options. cmd.Program must be "git".
func ParseGit(cmd Command) Git {
	g := Git{Repo: cmd.Env["GIT_DIR"] != "" || cmd.Env["GIT_WORK_TREE"] != "" || cmd.Env["GIT_COMMON_DIR"] != ""}
	args := cmd.Args
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, joined := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "-") {
			g.Sub, g.Args = arg, args[i+1:]
			return g
		}
		if !joined && gitValued[arg] && i+1 < len(args) {
			name, value = arg, args[i+1]
			i++
		} else if strings.HasPrefix(arg, "-C") && len(arg) > 2 && !strings.HasPrefix(arg, "--") {
			name, value = "-C", arg[2:] // -Cdir
		} else if strings.HasPrefix(arg, "-c") && len(arg) > 2 && !strings.HasPrefix(arg, "--") {
			name, value = "-c", arg[2:]
		}
		g.global(name, value)
	}
	return g
}

func (g *Git) global(name, value string) {
	switch name {
	case "-C":
		if filepath.IsAbs(value) {
			g.Dir = value
		} else {
			g.Dir = filepath.Join(g.Dir, value)
		}
	case "-c", "--config-env":
		g.Config = append(g.Config, value)
	case "--git-dir", "--work-tree":
		g.Repo = true
	case "--exec-path":
		g.ExecPath = value != ""
	}
}

// Text is the git command written plainly, global options dropped: "git push --force origin x".
// The risk table's patterns are matched against it, so a spelling with -C or quotes reads the
// same as the plain one.
func (g Git) Text() string {
	return strings.TrimSpace("git " + g.Sub + " " + strings.Join(g.Args, " "))
}

// Text is the command written plainly: its program and arguments, wrappers and environment
// dropped. The risk table's patterns are matched against it as well as the raw line.
func (c Command) Text() string {
	if c.Program == "git" {
		return ParseGit(c).Text()
	}
	return strings.TrimSpace(c.Program + " " + strings.Join(c.Args, " "))
}
