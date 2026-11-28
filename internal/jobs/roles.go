// Roles (DESIGN.md 6.6): a role is a reusable prompt. Four are embedded (lead, builder,
// reviewer, tester); files in ~/.config/crowsnest/roles/<name>.md add roles or override these.
// The job protocol (templates/protocol.md) is appended to every role prompt when a job agent
// starts, so a custom role cannot forget it.

package jobs

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

//go:embed roles/*.md templates/protocol.md
var embedded embed.FS

// roleName is what a role file may be called (it becomes the role's name).
var roleName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// maxRoleBytes caps an override file.
const maxRoleBytes = 32 << 10

// Roles is a set of role prompts.
type Roles struct {
	prompts map[string]string
	custom  map[string]bool // came from the override directory
}

// RoleInfo is one entry of GET /roles.
type RoleInfo struct {
	Name    string `json:"name"`
	Summary string `json:"summary"` // the first line of the prompt after its heading
	Custom  bool   `json:"custom"`
}

// EmbeddedRoles returns the four built-in roles.
func EmbeddedRoles() *Roles {
	roles := &Roles{prompts: map[string]string{}, custom: map[string]bool{}}
	entries, _ := embedded.ReadDir("roles")
	for _, entry := range entries {
		body, _ := embedded.ReadFile("roles/" + entry.Name())
		roles.prompts[strings.TrimSuffix(entry.Name(), ".md")] = string(body)
	}
	return roles
}

// LoadRoles returns the embedded roles with the files in dir added or overriding. A missing
// directory is fine; an unreadable file or a bad name is skipped and reported in the error list.
func LoadRoles(dir string) (*Roles, []error) {
	roles := EmbeddedRoles()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return roles, nil
		}
		return roles, []error{fmt.Errorf("reading roles from %s: %w", dir, err)}
	}
	var problems []error
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".md")
		if !ok || entry.IsDir() {
			continue
		}
		if !roleName.MatchString(name) {
			problems = append(problems, fmt.Errorf("role file %s: the name must be lower-case letters, digits, - or _", entry.Name()))
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name())) //nolint:gosec // the user's own config directory
		if err != nil || len(body) > maxRoleBytes {
			problems = append(problems, fmt.Errorf("role file %s: unreadable or larger than %d bytes", entry.Name(), maxRoleBytes))
			continue
		}
		roles.prompts[name], roles.custom[name] = string(body), true
	}
	return roles, problems
}

// ValidRoleName reports whether name may name a role (a role file's name, or a role a job names).
func ValidRoleName(name string) bool { return roleName.MatchString(name) }

// Text is a role's own prompt, without the job protocol, and whether the role exists.
func (r *Roles) Text(name string) (string, bool) {
	text, ok := r.prompts[name]
	return text, ok
}

// FromFile reports whether the role's prompt came from the override directory (a file that
// adds a role or replaces a built-in one).
func (r *Roles) FromFile(name string) bool { return r.custom[name] }

// Has reports whether the role exists.
func (r *Roles) Has(name string) bool {
	_, ok := r.prompts[name]
	return ok
}

// Names lists the roles, built-ins first in their usual order.
func (r *Roles) Names() []string {
	order := map[string]int{"builder": 0, "lead": 1, "reviewer": 2, "tester": 3}
	names := make([]string, 0, len(r.prompts))
	for name := range r.prompts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		oi, iok := order[names[i]]
		oj, jok := order[names[j]]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		}
		return names[i] < names[j]
	})
	return names
}

// List describes every role.
func (r *Roles) List() []RoleInfo {
	out := []RoleInfo{}
	for _, name := range r.Names() {
		out = append(out, RoleInfo{Name: name, Summary: summaryOf(r.prompts[name]), Custom: r.custom[name]})
	}
	return out
}

func summaryOf(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return oneLine(line, 120)
		}
	}
	return ""
}

// Prompt is a role's prompt followed by the job protocol for jobID. mcp chooses the tool
// wording or the CLI wording; codex the lines that differ inside Codex's sandbox.
func (r *Roles) Prompt(role, jobID string, mcp, codex bool) (string, error) {
	base, ok := r.prompts[role]
	if !ok {
		return "", fmt.Errorf("unknown role %q", role)
	}
	protocol, err := ProtocolText(jobID, mcp, codex)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(base) + "\n\n" + protocol, nil
}

// ProtocolText renders the job protocol section. mcp chooses crowsnest's MCP tools over the
// `crowsnest job` commands; codex leaves out what a Codex sandbox cannot do (take a heavy slot:
// its files are outside the workspace, so `heavy` would wait for ever).
func ProtocolText(jobID string, mcp, codex bool) (string, error) {
	tmpl, err := template.ParseFS(embedded, "templates/protocol.md")
	if err != nil {
		return "", fmt.Errorf("parsing the protocol template: %w", err)
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, map[string]any{"JobID": jobID, "MCP": mcp, "Codex": codex}); err != nil {
		return "", fmt.Errorf("rendering the protocol: %w", err)
	}
	return out.String(), nil
}
