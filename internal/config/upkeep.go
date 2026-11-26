// The instance's name, Remote Control, and the orchestrator's upkeep settings (DESIGN.md 3, 7.9
// and 7.10). Kept in their own file and loader step so the option table's other rows stay as
// they were.

package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Option names for this file's settings.
const (
	optName             = "name"
	optNoRemoteControl  = "no-remote-control"
	optJobRemoteControl = "job-remote-control"
	optCompactAt        = "compact-at"
	optFreshAt          = "fresh-at"
)

// DefaultCompactAt is the orchestrator's context size, in tokens, past which it is compacted.
const DefaultCompactAt = 150_000

// FallbackName is the instance name when the hostname gives nothing usable.
const FallbackName = "crowsnest"

func init() {
	options = append(options,
		option{name: optName, env: []string{"CROWSNEST_NAME"}, usage: "this instance's name, shown in the header, the page title, notices and Remote Control (default: the short hostname)"},
		option{name: optNoRemoteControl, env: []string{"CROWSNEST_NO_REMOTE_CONTROL"}, kind: kindBool, def: "false", usage: "start the orchestrator without Claude Code's Remote Control"},
		option{name: optJobRemoteControl, env: []string{"CROWSNEST_JOB_REMOTE_CONTROL"}, kind: kindBool, def: "false", usage: "start job agents with Remote Control too, named \"<name> <job id> <title>\""},
		option{name: optCompactAt, env: []string{"CROWSNEST_COMPACT_AT"}, kind: kindInt, def: fmt.Sprint(DefaultCompactAt), usage: "compact the orchestrator once its context passes this many tokens; 0 disables"},
		option{name: optFreshAt, env: []string{"CROWSNEST_FRESH_AT"}, usage: "HH:MM: start the orchestrator on a new conversation once a day after this local time, with a hand-over (default off)"},
	)
}

// namePattern is what an instance name may hold: it goes into a page title, notification
// titles and Remote Control's session name, so it stays short and plain.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// NameFromHostname derives the default name: the hostname lower-cased up to its first dot, with
// anything outside the safe set replaced by '-', or FallbackName when nothing is left.
func NameFromHostname(host string) string {
	host, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(host)), ".")
	var out strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
	}
	name := strings.Trim(out.String(), "-_")
	if len(name) > 32 {
		name = strings.TrimRight(name[:32], "-_")
	}
	if !namePattern.MatchString(name) {
		return FallbackName
	}
	return name
}

// RemoteControlName is the orchestrator's Remote Control session name: "<name> crowsnest", plus
// the herdr session when it is not the default one.
func (c Config) RemoteControlName() string {
	if c.Session == DefaultSession {
		return c.Name + " crowsnest"
	}
	return c.Name + " crowsnest " + c.Session
}

func (l *loader) loadUpkeep(cfg *Config) error {
	cfg.Name = l.str(optName)
	if cfg.Name == "" {
		cfg.Name = NameFromHostname(l.hostname())
	} else if !namePattern.MatchString(cfg.Name) {
		return fmt.Errorf("--name %q must be 1 to 32 letters, digits, '-' or '_', starting with a letter or digit", cfg.Name)
	}
	var err error
	if cfg.NoRemoteControl, err = l.boolean(optNoRemoteControl); err != nil {
		return err
	}
	if cfg.JobRemoteControl, err = l.boolean(optJobRemoteControl); err != nil {
		return err
	}
	if cfg.CompactAt, err = l.integer(optCompactAt); err != nil {
		return err
	}
	if cfg.CompactAt < 0 || (cfg.CompactAt > 0 && cfg.CompactAt < 20_000) {
		return fmt.Errorf("--compact-at %d: give 0 (off) or a size of at least 20000 tokens", cfg.CompactAt)
	}
	cfg.FreshAt = -1
	if raw := l.str(optFreshAt); raw != "" {
		minutes, ok := parseHHMM(raw)
		if !ok {
			return fmt.Errorf("--fresh-at %q is not a time of day; use HH:MM, such as 04:00", raw)
		}
		cfg.FreshAt = minutes
	}
	return nil
}

// parseHHMM parses H:MM or HH:MM on a 24-hour clock into minutes after midnight.
func parseHHMM(raw string) (int, bool) {
	var hour, minute int
	if n, err := fmt.Sscanf(strings.TrimSpace(raw), "%d:%d", &hour, &minute); err != nil || n != 2 {
		return 0, false
	}
	hh, mm, _ := strings.Cut(strings.TrimSpace(raw), ":")
	if len(hh) < 1 || len(hh) > 2 || len(mm) != 2 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}
