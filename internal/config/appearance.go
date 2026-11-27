// Appearance (DESIGN.md "Design language"): the theme a browser gets until it chooses one of its
// own. Themes live in the web app (web/src/themes/<id>.css) and each browser keeps its own
// choice; this setting is only the default, which the server writes into index.html so the first
// paint of a new browser is already in it. An id the web app does not know falls back to the
// house theme there, so the server checks only the id's shape, not the list.

package config

import (
	"fmt"
	"regexp"
	"strings"
)

const optTheme = "theme"

// themeID is the shape of a theme id: its file name in web/src/themes, without .css.
var themeID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

func init() {
	options = append(options,
		option{name: optTheme, env: []string{"CROWSNEST_THEME"}, usage: "the theme a browser gets until it chooses its own, such as phosphor (default: the house theme, crowsnest)"},
	)
}

func (l *loader) loadAppearance(cfg *Config) error {
	cfg.Theme = strings.ToLower(strings.TrimSpace(l.str(optTheme)))
	if cfg.Theme == "" || themeID.MatchString(cfg.Theme) {
		return nil
	}
	if l.lookup(optTheme).where == SourceDatabase && !l.opts.StrictStored {
		// A bad value saved in the app (or brought by a restore, R7-I5) must not stop the start:
		// the house theme, and a warning. A flag, variable or file the human wrote still stops it.
		l.warnings = append(l.warnings, fmt.Sprintf("the saved default theme %q is not a theme id; using the house theme", cfg.Theme))
		cfg.Theme = ""
		return nil
	}
	return fmt.Errorf("--theme %q is not a theme id; give one such as crowsnest, phosphor, deep-sea, ember or contrast", cfg.Theme)
}
