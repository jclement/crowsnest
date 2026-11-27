// Package schedule is scheduled work (DESIGN.md 7.8): markdown task files with `every:`
// frontmatter that the poke precheck turns into poke reasons when they are due, or that are
// started as crowsnest jobs (`as: job`). The file format is the front door's (claude-agent's
// frontdoor/tasks/*.md), so a task written for it runs here unchanged. Schedules made in the app
// (app.go) become the same Task, so the due rules, stamps and runs have one shape for both.
//
// The pieces: this file parses one task file; due.go decides, as a pure function of the task,
// its stamps and the time, whether a task is due and when it next will be; app.go validates,
// stores and materialises app schedules; service.go reads the task directories and the app
// schedules on every tick, keeps the stamps in kv, records the runs and creates the jobs.
//
// A task file:
//
//	---
//	every: 15m          # 15m, 60m, 4h, 1d, 3d, poke or change
//	after: 17:00        # optional: not before this local time of day
//	as: job             # optional: run as a crowsnest job instead of a poke reason
//	dir: ~/Work/thing   # as: job only: where the job runs (none: the orchestrator holds it)
//	role: builder       # as: job only: the job's role
//	project: crowsnest  # optional: a project's slug; a poke goes to that project's lead, a job joins it
//	---
//	# What to do
//	The body is the instruction: the orchestrator (or the job's agent) reads the file and does it.
package schedule

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CadenceKind is the shape of an `every:` value.
type CadenceKind string

// Cadence kinds.
const (
	CadenceInterval CadenceKind = "interval" // 15m, 60m, 4h: elapsed time since the last run
	CadenceDays     CadenceKind = "days"     // 1d, 3d: calendar days in local time
	CadencePoke     CadenceKind = "poke"     // rides along with any poke that has something else due
	CadenceChange   CadenceKind = "change"   // when the fleet or job picture changed since the last run
)

// Cadence is a parsed `every:` value.
type Cadence struct {
	Kind     CadenceKind
	Interval time.Duration // CadenceInterval
	Days     int           // CadenceDays
	Text     string        // as written, for display
}

// TimeOfDay is a local wall-clock time, minutes after midnight.
type TimeOfDay struct {
	Minutes int
	Text    string // HH:MM
}

// RunAs says how a due task is carried out.
type RunAs string

// Ways to run a task.
const (
	RunAsPoke RunAs = "poke" // a reason line in the next poke; the orchestrator reads the file
	RunAsJob  RunAs = "job"  // a crowsnest job, with the full supervisor
)

// Task is one parsed task file or app schedule.
type Task struct {
	Name   string     `json:"name"`   // the file name without .md
	Path   string     `json:"path"`   // absolute path of the file (an app poke task: its materialised copy)
	Origin string     `json:"origin"` // "dir" (the orchestrator's), "user" (~/.config/crowsnest) or "app"
	Every  Cadence    `json:"-"`
	After  *TimeOfDay `json:"-"`
	As     RunAs      `json:"as"`
	Dir    string     `json:"dir,omitempty"`
	Role   string     `json:"role,omitempty"`
	Engine string     `json:"engine,omitempty"` // as: job, app schedules only: claude or codex ("" the default)
	// Project is the `project:` slug a task file names ("" none). Load resolves it to ProjectID;
	// an app schedule carries ProjectID and Load fills Project from it, for display.
	Project   string `json:"project,omitempty"`
	ProjectID int64  `json:"project_id"`
	Title     string `json:"title"` // the body's first heading or line, for the UI
	Body      string `json:"-"`
	// Hash is the hex SHA-256 of a task file's bytes ("" for an app schedule): what the human
	// approves for a file that needs review (review.go, R5-S3).
	Hash string `json:"hash,omitempty"`
	// Raw is the file exactly as read (frontmatter, HTML and comments included): what the hash
	// covers, and what a review shows (R6-S7).
	Raw string `json:"-"`
}

// Target is who carries out a task.
type Target string

// Targets.
const (
	TargetOrchestrator Target = "orchestrator" // a poke to the orchestrator
	TargetLead         Target = "lead"         // a poke to the lead of the task's project
	TargetJob          Target = "job"          // a crowsnest job
)

// Target says who carries the task out: a job, a project's lead, or the orchestrator.
func (t Task) Target() Target {
	switch {
	case t.As == RunAsJob:
		return TargetJob
	case t.ProjectID != 0:
		return TargetLead
	}
	return TargetOrchestrator
}

// namePattern is the front door's task-name rule: a name is also a key and a reason line.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// rolePattern bounds a role named in a task (the jobs service validates it properly).
var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// slugPattern bounds a project slug named in a task (Load checks that the project exists).
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// maxTaskBytes bounds a task file: it is an instruction, not a document.
const maxTaskBytes = 64 << 10

// NameOf is the task name for a file path, or an error saying what is wrong with it.
func NameOf(path string) (string, error) {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".md")
	if name == base || !namePattern.MatchString(name) {
		return "", fmt.Errorf("task file names are lower-case letters, digits, '-' and '_' ending in .md, not %q", base)
	}
	return name, nil
}

// Parse reads one task file's contents. Every error says what to fix.
func Parse(path, origin string, content []byte) (Task, error) {
	name, err := NameOf(path)
	if err != nil {
		return Task{}, err
	}
	if len(content) > maxTaskBytes {
		return Task{}, fmt.Errorf("the file is %d bytes; a task is at most %d", len(content), maxTaskBytes)
	}
	front, body, err := splitFrontmatter(string(content))
	if err != nil {
		return Task{}, err
	}
	sum := sha256.Sum256(content)
	task := Task{Name: name, Path: path, Origin: origin, As: RunAsPoke, Body: body, Title: titleOf(body, name), Hash: hex.EncodeToString(sum[:]),
		Raw: strings.ToValidUTF8(string(content), "\uFFFD")}
	if err := task.apply(front); err != nil {
		return Task{}, err
	}
	return task, nil
}

// splitFrontmatter separates the `---` block from the body.
func splitFrontmatter(content string) (map[string]string, string, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", errors.New("the file must start with a --- frontmatter block that names every")
	}
	front := map[string]string{}
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return front, strings.TrimSpace(strings.Join(lines[i+2:], "")), nil
		}
		if err := frontLine(front, strings.TrimRight(line, "\r\n")); err != nil {
			return nil, "", err
		}
	}
	return nil, "", errors.New("the frontmatter block is not closed with ---")
}

// frontLine parses one `key: value  # comment` line into front.
func frontLine(front map[string]string, line string) error {
	if hash := strings.Index(line, "#"); hash >= 0 {
		line = line[:hash]
	}
	if strings.TrimSpace(line) == "" {
		return nil
	}
	key, value, found := strings.Cut(line, ":")
	key = strings.ToLower(strings.TrimSpace(key))
	if !found || key == "" {
		return fmt.Errorf("frontmatter line %q is not key: value", strings.TrimSpace(line))
	}
	if _, seen := front[key]; seen {
		return fmt.Errorf("frontmatter names %s twice", key)
	}
	front[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	return nil
}

// apply validates the frontmatter keys into the task. Unknown keys are refused: a misspelt
// `afer:` silently ignored would run a daily task at midnight.
func (t *Task) apply(front map[string]string) error {
	for key := range front {
		switch key {
		case "every", "after", "as", "dir", "role", "project":
		default:
			return fmt.Errorf("unknown frontmatter key %q; a task takes every, after, as, dir, role and project", key)
		}
	}
	if slug := front["project"]; slug != "" {
		if !slugPattern.MatchString(slug) {
			return fmt.Errorf("project: %q is not a project slug (lower-case letters, digits and -)", slug)
		}
		t.Project = slug
	}
	every, err := ParseCadence(front["every"])
	if err != nil {
		return err
	}
	t.Every = every
	if raw := front["after"]; raw != "" {
		after, err := ParseTimeOfDay(raw)
		if err != nil {
			return err
		}
		t.After = &after
	}
	return t.applyRunAs(front)
}

func (t *Task) applyRunAs(front map[string]string) error {
	switch front["as"] {
	case "", "poke":
	case "job":
		t.As = RunAsJob
	default:
		return fmt.Errorf("as: %q is not poke or job", front["as"])
	}
	t.Dir, t.Role = front["dir"], front["role"]
	if t.As != RunAsJob && (t.Dir != "" || t.Role != "") {
		return errors.New("dir: and role: apply only to a task with as: job")
	}
	if t.Role != "" && !rolePattern.MatchString(t.Role) {
		return fmt.Errorf("role: %q is not a role name", t.Role)
	}
	if t.As == RunAsJob && (t.Every.Kind == CadencePoke || t.Every.Kind == CadenceChange) {
		return fmt.Errorf("every: %s rides on pokes, so it cannot be as: job; give it a time (15m, 1d)", t.Every.Text)
	}
	if t.As == RunAsJob && t.Body == "" {
		return errors.New("an as: job task needs a body: it is the job's brief")
	}
	return nil
}

// cadencePattern is a number and a unit: m(inutes), h(ours) or d(ays).
var cadencePattern = regexp.MustCompile(`^([0-9]{1,4})([mhd])$`)

// minInterval keeps a task from asking for a turn more often than the poker can sensibly give.
const minInterval = 5 * time.Minute

// ParseCadence parses an `every:` value.
func ParseCadence(raw string) (Cadence, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "":
		return Cadence{}, errors.New("every: is missing; want 15m, 60m, 4h, 1d, poke or change")
	case "poke":
		return Cadence{Kind: CadencePoke, Text: raw}, nil
	case "change":
		return Cadence{Kind: CadenceChange, Text: raw}, nil
	}
	match := cadencePattern.FindStringSubmatch(raw)
	if match == nil {
		return Cadence{}, fmt.Errorf("every: %q is not understood; want 15m, 60m, 4h, 1d, 3d, poke or change", raw)
	}
	count, _ := strconv.Atoi(match[1])
	if count == 0 {
		return Cadence{}, fmt.Errorf("every: %q must be more than zero", raw)
	}
	switch match[2] {
	case "d":
		return Cadence{Kind: CadenceDays, Days: count, Text: raw}, nil
	case "h":
		return Cadence{Kind: CadenceInterval, Interval: time.Duration(count) * time.Hour, Text: raw}, nil
	}
	interval := time.Duration(count) * time.Minute
	if interval < minInterval {
		return Cadence{}, fmt.Errorf("every: %q is too often; the shortest is %s", raw, minInterval)
	}
	return Cadence{Kind: CadenceInterval, Interval: interval, Text: raw}, nil
}

// timePattern is HH:MM on a 24-hour clock (H:MM accepted).
var timePattern = regexp.MustCompile(`^([0-9]{1,2}):([0-9]{2})$`)

// ParseTimeOfDay parses an `after:` value.
func ParseTimeOfDay(raw string) (TimeOfDay, error) {
	match := timePattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return TimeOfDay{}, fmt.Errorf("after: %q is not HH:MM", raw)
	}
	hour, _ := strconv.Atoi(match[1])
	minute, _ := strconv.Atoi(match[2])
	if hour > 23 || minute > 59 {
		return TimeOfDay{}, fmt.Errorf("after: %q is not a time of day", raw)
	}
	return TimeOfDay{Minutes: hour*60 + minute, Text: fmt.Sprintf("%02d:%02d", hour, minute)}, nil
}

// titleOf is the body's first non-blank line without heading marks, or the name.
func titleOf(body, name string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" {
			if runes := []rune(line); len(runes) > 120 {
				return string(runes[:119]) + "…"
			}
			return line
		}
	}
	return name
}

// Schedule describes the cadence in words: "every 15m", "every 1d after 17:00".
func (t Task) Schedule() string {
	text := "every " + t.Every.Text
	switch t.Every.Kind {
	case CadencePoke:
		text = "with every poke"
	case CadenceChange:
		text = "when the fleet or jobs change"
	}
	if t.After != nil {
		text += " after " + t.After.Text
	}
	return text
}
