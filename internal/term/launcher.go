// The real launcher: bridges onto a herdr session through the herdr CLI. (The demo launcher is
// in demo.go.)

package term

// HerdrLauncher builds TUI and pane bridges for one herdr session.
type HerdrLauncher struct {
	// Binary is the herdr executable; "herdr" (found on PATH) when empty.
	Binary string
	// Session is the herdr session name, already validated by the config loader.
	Session string
	// ClientStateDir, when set, is the TUI client's own XDG_STATE_HOME. herdr's client reads its
	// saved SSH machines (`herdr machine add`) from `$XDG_STATE_HOME/herdr/client/endpoints.json`
	// and draws every one of them in its sidebar, so a client that shares the user's state
	// directory hands anyone signed in to crowsnest a way onto those hosts too (DESIGN.md 20,
	// R4 5.5). A directory of crowsnest's own gives a client with no saved machines that still
	// attaches to the session: the session's socket lives under herdr's config directory, which
	// is not touched. Empty: the user's own state directory (`--terminal-machines`).
	ClientStateDir string
}

// TUI attaches the herdr client to the session in a PTY.
func (l HerdrLauncher) TUI() Bridge {
	bridge := newTUIBridge(l.binary(), l.Session)
	bridge.stateDir = l.ClientStateDir
	return bridge
}

// Pane controls one pane of the session.
func (l HerdrLauncher) Pane(paneID string) Bridge {
	return newPaneBridge(l.binary(), l.Session, paneID)
}

func (l HerdrLauncher) binary() string {
	if l.Binary == "" {
		return "herdr"
	}
	return l.Binary
}
