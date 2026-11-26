// The real launcher: bridges onto a herdr session through the herdr CLI. (The demo launcher is
// in demo.go.)

package term

// HerdrLauncher builds TUI and pane bridges for one herdr session.
type HerdrLauncher struct {
	// Binary is the herdr executable; "herdr" (found on PATH) when empty.
	Binary string
	// Session is the herdr session name, already validated by the config loader.
	Session string
}

// TUI attaches the herdr client to the session in a PTY.
func (l HerdrLauncher) TUI() Bridge { return newTUIBridge(l.binary(), l.Session) }

// Pane controls one pane of the session.
func (l HerdrLauncher) Pane(paneID string) Bridge { return newPaneBridge(l.binary(), l.Session, paneID) }

func (l HerdrLauncher) binary() string {
	if l.Binary == "" {
		return "herdr"
	}
	return l.Binary
}
