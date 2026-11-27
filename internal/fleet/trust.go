// The folder-trust prompt (DESIGN.md 8.2.1): the one start-up dialog every new project meets.
// Both engines draw one, and neither draws it the same way twice across versions:
//
//   - Claude Code 2.1.332: "Accessing workspace:", the folder (wrapped at the terminal's width),
//     then an unnumbered arrow-key menu, "❯ No, exit" preselected, "Yes, I trust this folder"
//     below it. Older versions drew a numbered box ("Do you trust the files in this folder?").
//   - codex-cli 0.167: "Folder access", the folder, "Trust this folder? …", then a numbered list,
//     "› 1. Trust and continue" preselected, "2. Back to Agent Command Center". A digit only moves
//     the selection there: the answer is the digit and then Enter (verified on 0.167 in tmux).
//
// DetectDialog already finds the options of either shape; Trust recognises that the dialog is the
// trust question, reads the folder off the screen, and names which option trusts and which does
// not, so the card can say "Trust" and "Don't trust" in words rather than echo the agent's menu.
// Crowsnest still never presses either by itself (decision 86).

package fleet

import (
	"regexp"
	"strings"
)

// Trust prompt engines.
const (
	TrustClaude = "claude"
	TrustCodex  = "codex"
)

// TrustPrompt is a folder-trust question found on a screen.
type TrustPrompt struct {
	// Engine is whose prompt it is: claude or codex.
	Engine string `json:"engine"`
	// Folder is the folder the agent asks about, as the screen shows it (wrapped lines joined);
	// "" when it could not be read.
	Folder string `json:"folder"`
	// Allow and Deny are the options that trust the folder and that refuse (the agent then exits,
	// or goes back to its own launcher). Their Keys always hold the whole answer.
	Allow DialogOption `json:"allow"`
	Deny  DialogOption `json:"deny"`
}

var (
	// trustHeadings start the block that names the folder.
	trustHeadings = regexp.MustCompile(`^(?:Accessing workspace:|Folder access|Do you trust the files in this folder\?)$`)
	// trustQuestion is a phrase only a trust prompt carries.
	trustQuestion = regexp.MustCompile(`(?i)trust this folder|do you trust the files|project you created or one you trust`)
	trustAllow    = regexp.MustCompile(`(?i)\btrust\b|^yes\b`)
	trustDeny     = regexp.MustCompile(`(?i)^no\b|\bexit\b|\bquit\b|\bback\b|don'?t trust|do not trust`)
)

// detectTrust reads a trust prompt from the dialog DetectDialog found on lines (border-stripped).
func detectTrust(lines []string, options []DialogOption) *TrustPrompt {
	if len(options) != 2 || !trustQuestion.MatchString(strings.Join(lines, "\n")) {
		return nil
	}
	prompt := TrustPrompt{Engine: TrustClaude, Folder: trustFolder(lines)}
	for _, line := range lines {
		if line == "Folder access" {
			prompt.Engine = TrustCodex
		}
	}
	allow, deny := -1, -1
	for i, option := range options {
		switch {
		case trustDeny.MatchString(option.Label):
			deny = i
		case trustAllow.MatchString(option.Label):
			allow = i
		}
	}
	if allow < 0 || deny < 0 {
		return nil
	}
	prompt.Allow, prompt.Deny = answerKeys(options[allow], prompt.Engine), answerKeys(options[deny], prompt.Engine)
	return &prompt
}

// answerKeys makes an option's Keys the whole answer. Claude's arrow menu already carries its
// keys; a numbered Codex option needs Enter after the digit (the digit alone only selects).
func answerKeys(option DialogOption, engine string) DialogOption {
	if len(option.Keys) == 0 && option.Key != "" {
		option.Keys = []string{option.Key}
		if engine == TrustCodex {
			option.Keys = append(option.Keys, "Enter")
		}
	}
	return option
}

// trustFolder is the folder under the prompt's heading: the lines after it down to the next blank
// line, joined without a separator because the terminal wrapped one long path.
func trustFolder(lines []string) string {
	for i, line := range lines {
		if !trustHeadings.MatchString(line) {
			continue
		}
		var parts []string
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == "" {
				if len(parts) == 0 {
					continue
				}
				break
			}
			if !strings.HasPrefix(lines[j], "/") && !strings.HasPrefix(lines[j], "~") && len(parts) == 0 {
				break // a question line, not a path: this shape names no folder here
			}
			parts = append(parts, lines[j])
		}
		return strings.Join(parts, "")
	}
	return ""
}
