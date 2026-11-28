// Settings changed in the app (DESIGN.md 3.2). Each is stored as kv settings.<option>; a change
// is validated by loading the whole config again with it (so the app accepts exactly what a flag
// would), saved, journalled, and applied live where that is cheap. A setting given by a flag or
// an environment variable is read-only in the app: whoever started the process chose it.

package system

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/store"
)

// SettingsKeyPrefix is the kv prefix of saved settings.
const SettingsKeyPrefix = "settings."

// ErrNotEditable is a change to a setting the app may not change, or one locked by a flag or
// variable.
var ErrNotEditable = errors.New("not editable")

// ErrInvalid wraps a value the config loader refused.
var ErrInvalid = errors.New("invalid value")

// SettingView is one row of the Settings page.
type SettingView struct {
	config.Setting
	Value   string `json:"value"`             // "" for a write-only setting
	IsSet   bool   `json:"is_set"`            // for a write-only setting: whether it has a value
	Default string `json:"default"`           // the built-in default
	Source  string `json:"source"`            // flag, env, database, file, default
	Where   string `json:"where"`             // "--model", "CROWSNEST_MODEL", "config.yaml", …
	Locked  string `json:"locked,omitempty"`  // why it is read-only here
	Pending bool   `json:"pending,omitempty"` // saved, takes effect only after a restart
	// RestoredValue is the value a restore set aside (R9-S2: the setting acts outward or grants),
	// which does not apply until the human uses it; RestoredWhy says why it was set aside.
	RestoredValue *string `json:"restored_value,omitempty"`
	RestoredWhy   string  `json:"restored_why,omitempty"`
}

// SettingsPage is GET /settings.
type SettingsPage struct {
	Settings []SettingView `json:"settings"`
	// Bind is shown read-only, with the command that changes it.
	Bind         string `json:"bind"`
	BindCommand  string `json:"bind_command"`
	Name         string `json:"name"`
	ConfigFile   string `json:"config_file,omitempty"`
	Orchestrator string `json:"orchestrator_engine,omitempty"` // what engine auto resolved to
}

// Settings lists every editable setting with its value and where it came from.
func (s *Service) Settings(ctx context.Context) (SettingsPage, error) {
	cfg := s.Config()
	stored, err := s.opts.KV.KVPrefix(ctx, SettingsKeyPrefix)
	if err != nil {
		return SettingsPage{}, err
	}
	page := SettingsPage{Name: cfg.Name, ConfigFile: cfg.ConfigFile, Orchestrator: s.Selection(ctx).Orchestrator}
	var binds []string
	for _, listener := range cfg.Listeners {
		binds = append(binds, listener.Addr.String())
	}
	page.Bind = strings.Join(binds, ", ")
	page.BindCommand = fmt.Sprintf("crowsnest --bind tailnet   (or CROWSNEST_BIND=tailnet; now: %s)", strings.Join(cfg.Binds, ","))
	restored, err := s.opts.KV.KVPrefix(ctx, store.RestoredSettingPrefix)
	if err != nil {
		return SettingsPage{}, err
	}
	for _, setting := range config.EditableSettings() {
		view := s.view(cfg, stored, setting)
		if value, ok := restored[setting.Name]; ok {
			view.RestoredValue = &value
			view.RestoredWhy, _ = config.RestoreSetsAside(setting.Name)
		}
		page.Settings = append(page.Settings, view)
	}
	return page, nil
}

func (s *Service) view(cfg config.Config, stored map[string]string, setting config.Setting) SettingView {
	where := cfg.Sources[setting.Name]
	view := SettingView{Setting: setting, Default: config.OptionDefault(setting.Name), Source: config.SourceKind(where), Where: where}
	if setting.WriteOnly {
		view.IsSet = cfg.NotifyURL != ""
		if fileWhere := cfg.Sources[setting.Name+"-file"]; config.SourceKind(fileWhere) != config.KindDefault {
			view.Source, view.Where = config.SourceKind(fileWhere), fileWhere
		}
	} else {
		view.Value = cfg.SettingValue(setting.Name)
	}
	switch view.Source {
	case config.KindFlag:
		view.Locked = "Set by " + view.Where + " on crowsnest's command line: change it there"
	case config.KindEnv:
		view.Locked = "Set by the environment (" + view.Where + "): change it there"
	}
	if raw, ok := stored[setting.Name]; ok && setting.Apply == config.ApplyRestart && view.Locked == "" {
		view.Pending = raw != s.opts.Config.SettingValue(setting.Name) && !setting.WriteOnly
	}
	return view
}

// UpdateSetting saves value for name (an empty value clears the saved one when clear is set)
// and applies it.
func (s *Service) UpdateSetting(ctx context.Context, name, value string, clear bool) (SettingView, error) {
	setting, ok := config.LookupSetting(name)
	if !ok {
		return SettingView{}, fmt.Errorf("%w: %q cannot be changed in the app", ErrNotEditable, name)
	}
	value = normaliseSetting(setting, value)
	previous := s.Config()
	stored, err := s.opts.KV.KVPrefix(ctx, SettingsKeyPrefix)
	if err != nil {
		return SettingView{}, err
	}
	if locked := s.view(previous, stored, setting).Locked; locked != "" {
		return SettingView{}, fmt.Errorf("%w: %s", ErrNotEditable, locked)
	}
	if clear {
		delete(stored, name)
	} else {
		stored[name] = value
	}
	next, err := s.reload(stored)
	if err != nil {
		return SettingView{}, fmt.Errorf("%w: %s", ErrInvalid, cleanLoadError(err))
	}
	if clear {
		err = s.opts.KV.DeleteKV(ctx, SettingsKeyPrefix+name)
	} else {
		err = s.opts.KV.SetKV(ctx, SettingsKeyPrefix+name, value)
	}
	if err != nil {
		return SettingView{}, err
	}
	// The human has now decided this setting: a value a restore set aside is no longer offered.
	if err := s.opts.KV.DeleteKV(ctx, store.RestoredSettingPrefix+name); err != nil {
		return SettingView{}, err
	}
	s.mu.Lock()
	s.cfg = s.cfg.WithSettings(next)
	s.report = nil
	current := s.cfg
	s.mu.Unlock()
	s.journalChange(ctx, setting, previous, current, clear)
	if s.opts.Apply != nil {
		s.opts.Apply(previous, current)
	}
	if name == "engine" {
		s.kickOrchestrator()
	}
	return s.view(current, stored, setting), nil
}

func (s *Service) reload(stored map[string]string) (config.Config, error) {
	if s.opts.Reload == nil {
		return config.Config{}, errors.New("settings cannot be changed in this process")
	}
	return s.opts.Reload(stored)
}

// normaliseSetting trims and, for lists, tidies the separators.
func normaliseSetting(setting config.Setting, value string) string {
	value = strings.TrimSpace(value)
	if setting.Kind != "list" {
		return value
	}
	var parts []string
	for part := range strings.FieldsFuncSeq(value, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}

// cleanLoadError keeps the loader's message, which already names the option and what to pass.
func cleanLoadError(err error) string {
	text := err.Error()
	text = strings.ReplaceAll(text, " from database", "")
	return text
}

func (s *Service) journalChange(ctx context.Context, setting config.Setting, previous, current config.Config, clear bool) {
	before, after := previous.SettingValue(setting.Name), current.SettingValue(setting.Name)
	if setting.WriteOnly {
		before, after = setOrNot(before), setOrNot(after)
	}
	summary := fmt.Sprintf("Settings: %s %s → %s", setting.Label, orNone(before), orNone(after))
	if clear {
		summary += " (saved value cleared)"
	}
	switch setting.Apply {
	case config.ApplyOrchestrator:
		summary += "; applies when the orchestrator restarts"
	case config.ApplyRestart:
		summary += "; applies when crowsnest restarts"
	}
	s.journal(ctx, summary, map[string]any{"setting": setting.Name, "apply": setting.Apply})
}

func setOrNot(value string) string {
	if value == "true" {
		return "set"
	}
	return "not set"
}

func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

func itoa(value int) string { return strconv.Itoa(value) }
