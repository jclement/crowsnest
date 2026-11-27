// Settings that can be changed in the app (DESIGN.md 3, "Settings in the app"), and where every
// value came from.
//
// Precedence, decided deliberately: a flag, then an environment variable, then a value saved in
// the app (the database), then the config file, then the default. Whoever starts the process
// with a flag or a variable is being explicit about this run, so it wins and the app shows the
// setting read-only ("set by the environment: change it there"). The app's value beats the file
// because the file is the quiet, rarely-touched layer: someone who changes a setting in the app
// expects it to take, and a file they may have forgotten should not silently undo it.
//
// Only the rows in editable are ever read from the database. Authentication, the bind, the
// session name, the data directory and every other security-sensitive option are not in it and
// so can never be changed from a browser, whatever the database holds.

package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Option names added with the guided first run.
const (
	optEngine            = "engine"
	optOpen              = "open"
	optNoTailscaleDetect = "no-tailscale-detect"
)

// Engines crowsnest can run agents with. EngineAuto picks claude when it is installed and signed
// in, else codex when installed.
const (
	EngineAuto   = "auto"
	EngineClaude = "claude"
	EngineCodex  = "codex"
)

// Open modes for --open: whether serve opens a browser on the setup link (or the app).
const (
	OpenAuto   = "auto"
	OpenAlways = "always"
	OpenNever  = "never"
)

func init() {
	options = append(options,
		option{name: optEngine, env: []string{"CROWSNEST_ENGINE"}, def: EngineAuto, usage: "agent engine for the orchestrator and for jobs that name none: auto, claude or codex"},
		option{name: optOpen, env: []string{"CROWSNEST_OPEN"}, def: OpenAuto, usage: "open a browser at start (the setup link, else the app): auto (only from a desktop terminal), always or never"},
		option{name: optNoTailscaleDetect, env: []string{"CROWSNEST_NO_TAILSCALE_DETECT"}, kind: kindBool, def: "false", usage: "do not add this machine's Tailscale MagicDNS name to the allowed hosts at start"},
	)
}

// Where a value came from, as SourceKind reports it.
const (
	SourceDatabase = "database" // saved in the app (Config.Sources value)

	KindFlag     = "flag"
	KindEnv      = "env"
	KindDatabase = "database"
	KindFile     = "file"
	KindDefault  = "default"
)

// SourceKind classifies a Config.Sources value.
func SourceKind(where string) string {
	switch {
	case where == "" || where == "default":
		return KindDefault
	case where == SourceDatabase:
		return KindDatabase
	case strings.HasPrefix(where, "--"):
		return KindFlag
	case strings.HasSuffix(where, ".yaml") || strings.HasSuffix(where, ".yml") || strings.Contains(where, "."):
		return KindFile
	default:
		return KindEnv
	}
}

// Apply says what it takes for a change to a setting to take effect.
type Apply string

const (
	ApplyLive         Apply = "live"         // at once
	ApplyOrchestrator Apply = "orchestrator" // the orchestrator is restarted (crowsnest does it)
	ApplyRestart      Apply = "restart"      // crowsnest itself must be restarted
)

// Setting describes one option the app may change.
type Setting struct {
	Name      string   `json:"name"`    // the option (flag) name
	Label     string   `json:"label"`   // what the Settings page calls it
	Section   string   `json:"section"` // the Settings page section
	Help      string   `json:"help"`
	Kind      string   `json:"kind"` // text, bool, int, duration, list, choice, secret
	Choices   []string `json:"choices,omitempty"`
	Apply     Apply    `json:"apply"`
	WriteOnly bool     `json:"write_only,omitempty"` // the value is never sent back (the notify URL)
	// Invert shows a "no-…" option as its positive ("Remote Control" on = no-remote-control false).
	Invert bool `json:"invert,omitempty"`
}

// editable is the table of settings the app may change, in Settings page order.
var editable = []Setting{
	{Name: optModel, Label: "Orchestrator model", Section: "Agents", Kind: "text", Apply: ApplyOrchestrator,
		Help: "sonnet, opus, haiku or a full model id; empty means Claude's own default"},
	{Name: optJobModel, Label: "Job model", Section: "Agents", Kind: "text", Apply: ApplyLive,
		Help: "for Claude job agents whose job names none; empty means Claude's own default"},
	{Name: optEngine, Label: "Default engine", Section: "Agents", Kind: "choice", Choices: []string{EngineAuto, EngineClaude, EngineCodex}, Apply: ApplyOrchestrator,
		Help: "the orchestrator's agent, and the engine of jobs that name none. auto: Claude when signed in, else Codex"},
	{Name: optPermissionMode, Label: "Permission mode", Section: "Agents", Kind: "choice", Choices: permissionModes, Apply: ApplyOrchestrator,
		Help: "the orchestrator's Claude Code permission mode"},
	{Name: optNoRemoteControl, Label: "Remote Control", Section: "Agents", Kind: "bool", Invert: true, Apply: ApplyOrchestrator,
		Help: "start the orchestrator with Claude Code's Remote Control, so the Claude app can reach it too"},
	{Name: optJobRemoteControl, Label: "Remote Control for jobs", Section: "Agents", Kind: "bool", Apply: ApplyRestart,
		Help: "start Claude job agents with Remote Control too"},
	{Name: optPokeInterval, Label: "Poke interval", Section: "Orchestrator upkeep", Kind: "duration", Apply: ApplyLive,
		Help: "how often the timer considers poking the orchestrator; 0 turns timer pokes off"},
	{Name: optCompactAt, Label: "Compact past (tokens)", Section: "Orchestrator upkeep", Kind: "int", Apply: ApplyLive,
		Help: "compact the orchestrator once its context passes this size; 0 off"},
	{Name: optFreshAt, Label: "Fresh conversation at", Section: "Orchestrator upkeep", Kind: "text", Apply: ApplyLive,
		Help: "HH:MM local time: a daily hand-over and new conversation; empty off"},
	{Name: optAttachmentMaxMB, Label: "Largest attachment (MB)", Section: "Attachments", Kind: "int", Apply: ApplyRestart,
		Help: "pasted, dropped or attached by an agent"},
	{Name: optAttachmentQuotaMB, Label: "Attachment quota (MB)", Section: "Attachments", Kind: "int", Apply: ApplyRestart,
		Help: "disk all attachments may take; past it the oldest unneeded files are deleted"},
	{Name: optNotifyURL, Label: "Notify URL", Section: "Notices", Kind: "secret", WriteOnly: true, Apply: ApplyRestart,
		Help: "ntfy-style URL that gets a one-line POST when something needs you. Write-only: it is never shown again"},
	{Name: optMaxRunningJobs, Label: "Running jobs at once", Section: "Throttling and review", Kind: "int", Apply: ApplyLive,
		Help: "job agents running at once; more wait queued and start in order. 0: a quarter of the cores, at least 2"},
	{Name: optLoadHold, Label: "Hold starts above load per core", Section: "Throttling and review", Kind: "text", Apply: ApplyLive,
		Help: "new job starts wait while the load average per core stays above this for a minute; 0 off"},
	{Name: optHeavySlots, Label: "Heavy command slots", Section: "Throttling and review", Kind: "int", Apply: ApplyLive,
		Help: "builds and test suites that may run at once through crowsnest heavy"},
	{Name: optReview, Label: "Review gate", Section: "Throttling and review", Kind: "choice", Choices: []string{ReviewAuto, ReviewSame, ReviewClaude, ReviewCodex, ReviewOff}, Apply: ApplyLive,
		Help: "for builder jobs in a worktree: auto is the other engine when installed, else the same one"},
	{Name: optMerge, Label: "Land merges with", Section: "Throttling and review", Kind: "choice", Choices: []string{MergeCommit, MergeFF}, Apply: ApplyLive,
		Help: "merge: a merge commit; ff: fast-forward only (refused when the base moved on)"},
	{Name: optDigestAt, Label: "Daily digest at", Section: "Throttling and review", Kind: "text", Apply: ApplyLive,
		Help: "HH:MM local time: the day's digest in the Inbox; empty off"},
	{Name: optProtectedHost, Label: "Protected hosts", Section: "Guard", Kind: "list", Apply: ApplyRestart,
		Help: "production hosts no agent may reach unattended (added to the protected-hosts file's)"},
	{Name: optProtectedBranch, Label: "Protected branches", Section: "Guard", Kind: "list", Apply: ApplyRestart,
		Help: "branches agents may not commit or push onto, nor force-push"},
}

// EditableSettings returns the table of settings the app may change.
func EditableSettings() []Setting { return slices.Clone(editable) }

// Editable reports whether the app may change option name.
func Editable(name string) bool {
	return slices.ContainsFunc(editable, func(setting Setting) bool { return setting.Name == name })
}

// LookupSetting returns the Setting row for name.
func LookupSetting(name string) (Setting, bool) {
	index := slices.IndexFunc(editable, func(setting Setting) bool { return setting.Name == name })
	if index < 0 {
		return Setting{}, false
	}
	return editable[index], true
}

// OptionDefault is an option's built-in default in text form.
func OptionDefault(name string) string { return lookupOption(name).def }

// OptionEnv is the environment variables an option reads, first set one wins.
func OptionEnv(name string) []string { return slices.Clone(lookupOption(name).env) }

// KnownEnv reports whether name is an environment variable some option reads.
func KnownEnv(name string) bool {
	for _, opt := range options {
		if slices.Contains(opt.env, name) {
			return true
		}
	}
	return false
}

// AllEnvNames lists every environment variable the option table reads.
func AllEnvNames() []string {
	var names []string
	for _, opt := range options {
		names = append(names, opt.env...)
	}
	return names
}

// OptionNames lists every option name, in --help order.
func OptionNames() []string {
	names := make([]string, 0, len(options))
	for _, opt := range options {
		names = append(names, opt.name)
	}
	return names
}

// SettingValue renders an option's resolved value from cfg in the text form a flag would take,
// for the Settings page. Write-only settings report only whether they are set.
func (c Config) SettingValue(name string) string {
	if value, ok := c.acceptSettingValue(name); ok {
		return value
	}
	switch name {
	case optModel:
		return c.Model
	case optJobModel:
		return c.JobModel
	case optEngine:
		return c.Engine
	case optPermissionMode:
		return c.PermissionMode
	case optNoRemoteControl:
		return boolText(c.NoRemoteControl)
	case optJobRemoteControl:
		return boolText(c.JobRemoteControl)
	case optPokeInterval:
		return durationText(c.PokeInterval.String())
	case optCompactAt:
		return itoa(c.CompactAt)
	case optFreshAt:
		return c.FreshAtText()
	case optAttachmentMaxMB:
		return itoa(c.AttachmentMaxMB)
	case optAttachmentQuotaMB:
		return itoa(c.AttachmentQuotaMB)
	case optNotifyURL:
		return boolText(c.NotifyURL != "")
	case optMaxRunningJobs:
		return itoa(c.MaxRunningJobs)
	case optLoadHold:
		return strconv.FormatFloat(c.LoadHold, 'f', -1, 64)
	case optHeavySlots:
		return itoa(c.HeavySlots)
	case optReview:
		return c.Review
	case optMerge:
		return c.MergeMode
	case optDigestAt:
		return c.DigestAt
	case optProtectedHost:
		return strings.Join(c.ProtectedHosts, ",")
	case optProtectedBranch:
		return strings.Join(c.ProtectedBranches, ",")
	}
	return ""
}

// FreshAtText is FreshAt as HH:MM, or "" when off.
func (c Config) FreshAtText() string {
	if c.FreshAt < 0 {
		return ""
	}
	return twoDigits(c.FreshAt/60) + ":" + twoDigits(c.FreshAt%60)
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// durationText shortens Go's duration text: 15m0s -> 15m, 1h0m0s -> 1h, 0s -> 0.
func durationText(text string) string {
	if text == "0s" {
		return "0"
	}
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + itoa(value)
	}
	return itoa(value)
}

func itoa(value int) string { return strconv.Itoa(value) }

func (l *loader) loadEngine(cfg *Config) error {
	cfg.Engine = strings.ToLower(l.str(optEngine))
	switch cfg.Engine {
	case EngineAuto, EngineClaude, EngineCodex:
	default:
		return fmt.Errorf("--engine %q is not auto, claude or codex", cfg.Engine)
	}
	cfg.Open = strings.ToLower(l.str(optOpen))
	switch cfg.Open {
	case OpenAuto, OpenAlways, OpenNever:
	case "true", "yes", "1":
		cfg.Open = OpenAlways
	case "false", "no", "0":
		cfg.Open = OpenNever
	default:
		return fmt.Errorf("--open %q is not auto, always or never", cfg.Open)
	}
	return nil
}

// WithSettings returns c with every app-editable setting (and Sources) taken from from: how the
// server applies a reloaded config without touching paths, binds or auth, which never change at
// run time.
func (c Config) WithSettings(from Config) Config {
	c.Model, c.JobModel, c.Engine, c.PermissionMode = from.Model, from.JobModel, from.Engine, from.PermissionMode
	c.NoRemoteControl, c.JobRemoteControl = from.NoRemoteControl, from.JobRemoteControl
	c.PokeInterval, c.CompactAt, c.FreshAt = from.PokeInterval, from.CompactAt, from.FreshAt
	c.AttachmentMaxMB, c.AttachmentQuotaMB = from.AttachmentMaxMB, from.AttachmentQuotaMB
	c.NotifyURL = from.NotifyURL
	c.ProtectedHosts, c.ProtectedBranches = from.ProtectedHosts, from.ProtectedBranches
	c.MaxRunningJobs, c.LoadHold, c.HeavySlots = from.MaxRunningJobs, from.LoadHold, from.HeavySlots
	c.Review, c.MergeMode, c.DigestAt = from.Review, from.MergeMode, from.DigestAt
	c = c.withAcceptSettings(from)
	c.Sources = from.Sources
	return c
}
