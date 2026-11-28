//go:build integration

package orch_test

import (
	"testing"

	"github.com/jclement/crowsnest/internal/agenttrust"
)

// forgetScratch removes exactly the records the real agents made of a test's scratch folder in
// the user's own ~/.claude.json and ~/.codex/config.toml (they run with the user's login, so their
// state is the user's), with the same careful update as a trust write. A scratch home is not an
// option: Claude's login is in the keychain under the default config directory, and Codex's refresh
// token is single-use, so copying auth.json to a scratch CODEX_HOME could sign the user out.
func forgetScratch(t *testing.T, dir string) {
	if dir == "" {
		return
	}
	note, err := agenttrust.ForgetScratch(dir)
	if err != nil {
		t.Errorf("removing the scratch folder's agent records: %v", err)
		return
	}
	t.Log(note)
}
