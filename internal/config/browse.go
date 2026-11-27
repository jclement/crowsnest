// The folder picker's extra roots (DESIGN.md 3, 4.23). In their own file and loader step, as the
// guard's are.

package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// optBrowseRoot names a directory the folder picker may browse besides the home directory and
// the directories projects and the remembered list already use.
const optBrowseRoot = "browse-root"

func init() {
	options = append(options,
		option{name: optBrowseRoot, env: []string{"CROWSNEST_BROWSE_ROOTS"}, kind: kindList, usage: "a directory the folder picker may browse besides your home, projects' and remembered directories, e.g. /srv or /Volumes; `/` only when named here (repeatable)"},
	)
}

func (l *loader) loadBrowse(cfg *Config) error {
	for _, root := range l.list(optBrowseRoot) {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if root == "~" || strings.HasPrefix(root, "~/") {
			root = filepath.Join(l.home, strings.TrimPrefix(root, "~"))
		}
		if !filepath.IsAbs(root) {
			return fmt.Errorf("--%s %q is not an absolute path; pass one like /srv", optBrowseRoot, root)
		}
		cfg.BrowseRoots = append(cfg.BrowseRoots, filepath.Clean(root))
	}
	return nil
}
