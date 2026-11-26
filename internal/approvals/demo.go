package approvals

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// DemoPanes are the demo fleet's panes the seeded approvals come from.
type DemoPanes struct {
	Builder, Reviewer, Docs string
}

// demoRequests are the scripted approvals: one of each kind the card renders, and one dangerous.
func demoRequests(panes DemoPanes) []RequestInput {
	edit, _ := json.Marshal(map[string]any{
		"file_path":  "/home/crow/src/crowsnest/internal/fleet/screen.go",
		"old_string": "\tif len(lines) > 4 {\n\t\tlines = lines[len(lines)-4:]\n\t}\n",
		"new_string": "\t// Claude's dialogs put their hint on one of the last four non-blank lines.\n\tif len(lines) > dialogTailLines {\n\t\tlines = lines[len(lines)-dialogTailLines:]\n\t}\n",
	})
	return []RequestInput{
		{PaneID: panes.Builder, AgentName: "builder", Cwd: "/home/crow/src/crowsnest", ToolName: "Bash",
			ToolInput: json.RawMessage(`{"command":"go test ./internal/fleet/...","description":"Run the fleet package tests"}`)},
		{PaneID: panes.Docs, AgentName: "docs-writer", Cwd: "/home/crow/src/crowsnest", ToolName: "Edit", ToolInput: edit},
		{PaneID: panes.Reviewer, AgentName: "reviewer", Cwd: "/home/crow/src/crowsnest", ToolName: "Bash",
			ToolInput: json.RawMessage(`{"command":"git push --force origin main","description":"Force-push the rebased branch"}`)},
	}
}

// demoRotation is asked from now and then, so the tray has something arriving.
func demoRotation(panes DemoPanes) []RequestInput {
	return []RequestInput{
		{PaneID: panes.Builder, AgentName: "builder", Cwd: "/home/crow/src/crowsnest", ToolName: "WebFetch",
			ToolInput: json.RawMessage(`{"url":"https://pkg.go.dev/github.com/coder/websocket","prompt":"How do I set a read limit?"}`)},
		{PaneID: panes.Docs, AgentName: "docs-writer", Cwd: "/home/crow/src/crowsnest", ToolName: "mcp__github__create_issue",
			ToolInput: json.RawMessage(`{"owner":"jclement","repo":"crowsnest","title":"Tray covers the composer on iPad","labels":["ui"]}`)},
		{PaneID: panes.Builder, AgentName: "builder", Cwd: "/home/crow/src/crowsnest", ToolName: "Bash",
			ToolInput: json.RawMessage(`{"command":"npm install --save-dev @axe-core/playwright","description":"Add axe for the a11y checks"}`)},
	}
}

// SeedDemo stores the scripted approvals (none hooked: nothing waits on them).
func SeedDemo(ctx context.Context, service *Service, panes DemoPanes) error {
	for _, request := range demoRequests(panes) {
		if _, err := service.Request(ctx, request); err != nil {
			return err
		}
	}
	return nil
}

// RunDemo asks one more from the rotation every interval (default 3 minutes) while fewer than
// three are pending, until ctx ends.
func RunDemo(ctx context.Context, service *Service, panes DemoPanes, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	rotation := demoRotation(panes)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for next := 0; ; {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if pending, err := service.List(ctx, []Status{StatusPending}, 10); err != nil || len(pending) >= 3 {
			continue
		}
		if _, err := service.Request(ctx, rotation[next%len(rotation)]); err != nil {
			logger.Warn("demo approval", "err", err)
		}
		next++
	}
}
