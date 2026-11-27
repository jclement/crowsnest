// Folder trust for projects the human creates (DESIGN.md 8.2.1): whether creating a project in
// the app may record the agents' own folder-trust answer for its folder, so the lead and its jobs
// never stop at the trust prompt. On by default; the project form shows it as a checkbox, and this
// setting turns the checkbox off for good. Kept in its own file and loader step, like changes.go.

package config

const optTrustProjectFolders = "trust-project-folders"

func init() {
	options = append(options, option{name: optTrustProjectFolders, env: []string{"CROWSNEST_TRUST_PROJECT_FOLDERS"}, kind: kindBool, def: "true",
		usage: "when you create a project in the app (and leave its box ticked), record Claude Code's and Codex's folder-trust answer for its folder, so its agents start without the trust prompt (false: never write either agent's state)"})
	editable = append(editable, Setting{Name: optTrustProjectFolders, Label: "Trust the folders of projects you create", Section: "Agents",
		Kind: "bool", Apply: ApplyLive, Help: "creating a project records Claude Code's and Codex's \"trust this folder\" for it (only folders you chose in the app, never one an agent chose); off, every new project's first agent asks in its terminal and the question comes to you as a card"})
}

func (l *loader) loadTrust(cfg *Config) error {
	var err error
	cfg.TrustProjectFolders, err = l.boolean(optTrustProjectFolders)
	return err
}
