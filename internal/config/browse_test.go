// Tests for --browse-root: repeatable, from the environment, ~ expanded, refused when relative.

package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBrowseRoots(t *testing.T) {
	h := newHarness(t, "--browse-root", "/srv/", "--browse-root", "~/mnt", "--no-auth")
	cfg := h.mustLoad()
	if want := []string{"/srv", filepath.Join(h.home, "mnt")}; !slices.Equal(cfg.BrowseRoots, want) {
		t.Fatalf("roots %v, want %v", cfg.BrowseRoots, want)
	}

	env := newHarness(t, "--no-auth").withEnv("CROWSNEST_BROWSE_ROOTS", "/Volumes, /")
	if got := env.mustLoad().BrowseRoots; !slices.Equal(got, []string{"/Volumes", "/"}) {
		t.Fatalf("roots from the environment %v", got)
	}

	if none := newHarness(t, "--no-auth").mustLoad().BrowseRoots; len(none) != 0 {
		t.Fatalf("default roots %v, want none (home and projects are added by serve)", none)
	}

	_, err := newHarness(t, "--browse-root", "srv", "--no-auth").load()
	if err == nil || !strings.Contains(err.Error(), "not an absolute path") {
		t.Fatalf("relative root: %v", err)
	}
}
