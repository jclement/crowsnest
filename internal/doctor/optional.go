// Optional features (talk mode, Web Push, the notify URL, updates) and the configuration:
// whether it loads, what the startup refusals say, where each setting came from, and
// CROWSNEST_* variables that look like typos.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/store"
)

func (r *runner) optionalChecks(ctx context.Context) {
	if !r.configOK() {
		return
	}
	cfg := r.in.Config
	r.voiceCheck(ctx)
	r.pushCheck(ctx)
	notify := Check{ID: "optional.notify", Group: GroupOptional, Title: "Notify URL"}
	if cfg.NotifyURL != "" {
		notify.Status, notify.Detail = StatusOK, "set (from "+cfg.Sources["notify-url"]+")"
	} else {
		notify.Status, notify.Detail = StatusInfo, "not set: notices reach you through Web Push and the app only"
		notify.Fix = "Optional: an ntfy-style URL in Settings → Notices, or CROWSNEST_NOTIFY_URL"
	}
	r.add(notify)
	r.updateCheck()
}

func (r *runner) voiceCheck(ctx context.Context) {
	cfg := r.in.Config
	check := Check{ID: "optional.voice", Group: GroupOptional, Title: "Talk mode (ElevenLabs)"}
	if cfg.ElevenLabsAPIKey == "" {
		check.Status, check.Detail = StatusInfo, "no ElevenLabs key: talk mode is off"
		check.Fix = "Optional: set ELEVENLABS_API_KEY (or --elevenlabs-api-key-file) to talk to the orchestrator"
		r.add(check)
		return
	}
	if !r.in.Deep {
		check.Status, check.Detail = StatusOK, "key present (run with --deep to check it works)"
		r.add(check)
		return
	}
	status, _, err := r.env.HTTPGet(ctx, cfg.ElevenLabsURL+"/v1/user", map[string]string{"xi-api-key": cfg.ElevenLabsAPIKey})
	switch {
	case err != nil:
		check.Status, check.Detail = StatusWarn, "could not reach ElevenLabs"
	case status == 401 || status == 403:
		check.Status, check.Detail, check.Fix = StatusFail, "ElevenLabs refused the key", "Create a new key at elevenlabs.io and set it again"
	case status >= 200 && status < 300:
		check.Status, check.Detail = StatusOK, "key accepted by ElevenLabs"
	default:
		check.Status, check.Detail = StatusWarn, fmt.Sprintf("ElevenLabs answered %d", status)
	}
	r.add(check)
}

func (r *runner) pushCheck(ctx context.Context) {
	check := Check{ID: "optional.push", Group: GroupOptional, Title: "Web Push"}
	inspection, err := store.Inspect(ctx, r.in.Config.DatabasePath())
	switch {
	case r.in.Config.Demo:
		check.Status, check.Detail = StatusSkip, "demo mode"
	case err == nil && inspection.PushKeys:
		check.Status, check.Detail = StatusOK, "keys made; turn notifications on per device in the Notifications sheet"
	default:
		check.Status, check.Detail = StatusOK, "keys are made at first start"
	}
	r.add(check)
}

func (r *runner) updateCheck() {
	check := Check{ID: "optional.update", Group: GroupOptional, Title: "Updates"}
	live := r.report.Live
	switch {
	case live != nil && live.UpdateLatest != "":
		check.Status, check.Detail = StatusInfo, "crowsnest "+live.UpdateLatest+" is available"
		check.Fix, check.Command = "Update from the app's banner, or `crowsnest update`", "crowsnest update"
	case live != nil:
		check.Status, check.Detail = StatusOK, "the running instance knows of no newer release"
	default:
		check.Status, check.Detail = StatusSkip, "checked by the running server (or `crowsnest update --check`)"
	}
	r.add(check)
}

// ---- Configuration -----------------------------------------------------------

// paneVariables are CROWSNEST_* variables that are not options: crowsnest sets them in agents'
// panes and its own re-exec, so seeing them is normal.
var paneVariables = []string{"CROWSNEST", "CROWSNEST_SESSION", "CROWSNEST_SOCKET", "CROWSNEST_JOB", "CROWSNEST_TOKEN",
	"CROWSNEST_SECRETS_FD", "CROWSNEST_MISE_WAIT", "CROWSNEST_DEV_SESSION", "CROWSNEST_DEV_CONTAINER_PORT", "CROWSNEST_DEMO_DOCTOR", "CROWSNEST_DEMO_TAILSCALE", "CROWSNEST_DEMO_PARALLEL", "CROWSNEST_E2E_MODEL", "CROWSNEST_E2E_CLAUDE", "CROWSNEST_E2E_PERMISSION_MODE"}

func (r *runner) configChecks() {
	if r.in.LoadErr != nil {
		status, title := StatusFail, "Configuration loads"
		if errors.Is(r.in.LoadErr, config.ErrRefused) {
			title = "Startup rules"
		}
		r.add(Check{ID: "config.load", Group: GroupConfig, Title: title, Status: status,
			Detail: r.in.LoadErr.Error(), Fix: "Fix the setting named above; `crowsnest --help` lists every option"})
	} else {
		cfg := r.in.Config
		detail := "flags, environment, saved settings and file agree"
		if cfg.ConfigFile != "" {
			detail += "; file " + cfg.ConfigFile
		}
		r.add(Check{ID: "config.load", Group: GroupConfig, Title: "Configuration loads", Status: StatusOK, Detail: detail})
		if err := cfg.CheckStartup(); err != nil {
			r.add(Check{ID: "config.startup", Group: GroupConfig, Title: "Startup rules", Status: StatusFail, Detail: err.Error()})
		} else {
			r.add(Check{ID: "config.startup", Group: GroupConfig, Title: "Startup rules", Status: StatusOK, Detail: "no refusal would trigger"})
		}
		for _, warning := range cfg.Warnings {
			r.add(Check{ID: "config.warning", Group: GroupConfig, Title: "Configuration warning", Status: StatusWarn, Detail: warning})
		}
		r.report.Settings = Sources(cfg)
	}
	r.typoCheck()
}

// Sources lists every option not at its default, with where it came from. Secrets show as
// "(set)".
func Sources(cfg config.Config) []SettingSource {
	var out []SettingSource
	names := make([]string, 0, len(cfg.Sources))
	for name := range cfg.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		where := cfg.Sources[name]
		kind := config.SourceKind(where)
		if kind == config.KindDefault {
			continue
		}
		value := cfg.SettingValue(name)
		if secretOption(name) {
			value = "(set)"
		} else if value == "" {
			value = "(see " + where + ")"
		}
		out = append(out, SettingSource{Name: name, Value: value, Source: kind, Where: where})
	}
	return out
}

func secretOption(name string) bool {
	return strings.Contains(name, "password") || strings.Contains(name, "api-key") || strings.HasPrefix(name, "notify-url")
}

// typoCheck finds CROWSNEST_* variables no option reads, suggesting the nearest real one.
func (r *runner) typoCheck() {
	known := config.AllEnvNames()
	var unknown []string
	for _, entry := range r.env.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "CROWSNEST_") || config.KnownEnv(name) || slices.Contains(paneVariables, name) {
			continue
		}
		suggestion := nearest(name, known)
		if suggestion != "" {
			unknown = append(unknown, name+" (did you mean "+suggestion+"?)")
		} else {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	if len(unknown) == 0 {
		r.add(Check{ID: "config.env", Group: GroupConfig, Title: "CROWSNEST_* variables", Status: StatusOK, Detail: "none unrecognised"})
		return
	}
	r.add(Check{ID: "config.env", Group: GroupConfig, Title: "CROWSNEST_* variables", Status: StatusWarn,
		Detail: "not recognised, so ignored: " + strings.Join(unknown, ", "),
		Fix:    "Rename or remove them; `crowsnest --help` lists the real names"})
}

// nearest returns the candidate within a small edit distance of name, if any.
func nearest(name string, candidates []string) string {
	best, bestDistance := "", 4
	for _, candidate := range candidates {
		if distance := editDistance(name, candidate); distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	return best
}

// editDistance is the Levenshtein distance.
func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}
