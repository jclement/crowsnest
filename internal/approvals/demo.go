// Demo approvals for `crowsnest --demo` (DESIGN.md 8.7, 21.5): one of each kind the card renders
// (a low Bash, an Edit with a diff, a dangerous force push), each raised from the pane of a demo
// job (internal/demostory) and naming that job, with the job's directory as its cwd, so the job
// is needs_you/approval-tool (internal/jobs seeds it so; the change callback agrees) and the card,
// the job and the Fleet row tell one story. None is hooked, so nothing supersedes them; the demo
// fleet shows each one's permission dialog until it is decided. A rotation adds another every
// three minutes while fewer than three are pending, from a job whose last call was decided.

package approvals

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/jclement/crowsnest/internal/demostory"
)

// DemoPanes is how the demo finds the job agents its approvals come from, and tells the fake
// fleet about them.
type DemoPanes struct {
	Askers demostory.Askers
	// Asked shows a pending approval's permission dialog on its pane; Settled is the agent's side
	// of the decision. Either may be nil.
	Asked   func(Approval)
	Settled func(Approval)
}

// demoSettledCheck is how often the demo looks for decided approvals to tell the fleet about.
const demoSettledCheck = time.Second

// demoCall is one scripted tool call, by the demo agent that makes it. Paths in the input are
// relative to the agent's job directory.
type demoCall struct {
	agent string
	tool  string
	input func(dir string) json.RawMessage
}

func fixed(raw string) func(string) json.RawMessage {
	return func(string) json.RawMessage { return json.RawMessage(raw) }
}

// demoRequests are ap_1 to ap_3, in the order tests name them.
func demoRequests() []demoCall {
	edit := func(dir string) json.RawMessage {
		body, _ := json.Marshal(map[string]any{
			"file_path":  filepath.Join(dir, "src/screens/InboxScreen.tsx"),
			"old_string": "      <FlatList data={items} renderItem={renderItem} />\n",
			"new_string": "      <FlatList\n        data={items}\n        renderItem={renderItem}\n        refreshing={refreshing}\n        onRefresh={refresh}\n      />\n",
		})
		return body
	}
	return []demoCall{
		{demostory.LinkFixer, "Bash", fixed(`{"command":"npx linkinator ./dist --recurse","description":"Check the built site for broken links"}`)},
		{demostory.RefreshBuilder, "Edit", edit},
		{demostory.LimiterReviewer, "Bash", fixed(`{"command":"git push --force origin main","description":"Force-push the rebased fix/limiter onto main"}`)},
	}
}

// demoRotation is the next call of each of those jobs, asked once its previous one is decided.
func demoRotation() []demoCall {
	return []demoCall{
		{demostory.LimiterReviewer, "WebFetch", fixed(`{"url":"https://pkg.go.dev/golang.org/x/time/rate","prompt":"Does Limiter.Wait return when its context is cancelled?"}`)},
		{demostory.LinkFixer, "mcp__github__create_issue", fixed(`{"owner":"jclement","repo":"website","title":"The changelog links to a deleted release","labels":["docs"]}`)},
		{demostory.RefreshBuilder, "Bash", fixed(`{"command":"npm install --save-dev @testing-library/react-native","description":"Add the testing library for the inbox screen test"}`)},
	}
}

// request turns a call into a request from its agent's pane and job.
func (panes DemoPanes) request(call demoCall) RequestInput {
	var asker demostory.Asker
	if panes.Askers != nil {
		asker = panes.Askers(call.agent)
	}
	return RequestInput{JobID: asker.JobID, PaneID: asker.PaneID, AgentName: call.agent, Cwd: asker.Dir, ToolName: call.tool, ToolInput: call.input(asker.Dir)}
}

// SeedDemo stores the scripted approvals (none hooked: nothing waits on them) into an empty
// store, and shows every pending one's dialog on its pane (a reused demo data dir included).
func SeedDemo(ctx context.Context, service *Service, panes DemoPanes) error {
	existing, err := service.List(ctx, nil, 1)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		for _, call := range demoRequests() {
			if _, err := service.Request(ctx, panes.request(call)); err != nil {
				return err
			}
		}
	}
	pending, err := service.List(ctx, []Status{StatusPending}, 50)
	if err != nil || panes.Asked == nil {
		return err
	}
	for _, approval := range pending {
		panes.Asked(approval)
	}
	return nil
}

// RunDemo keeps the demo's approvals moving until ctx ends: every second it tells the fleet about
// decided ones, and every interval (default 3 minutes), while fewer than three are pending, it
// asks the next call from the rotation whose job has nothing pending.
func RunDemo(ctx context.Context, service *Service, panes DemoPanes, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	asks := time.NewTicker(interval)
	defer asks.Stop()
	checks := time.NewTicker(demoSettledCheck)
	defer checks.Stop()
	watched := map[string]bool{} // pending approvals the fleet shows a dialog for
	rotation := demoRotation()
	for next := 0; ; {
		pending, err := service.List(ctx, []Status{StatusPending}, 50)
		if err == nil {
			watched = panes.reportSettled(ctx, service, watched, pending)
		}
		select {
		case <-ctx.Done():
			return
		case <-checks.C:
			continue
		case <-asks.C:
		}
		if err != nil || len(pending) >= 3 {
			continue
		}
		call, ok := nextFree(rotation, &next, pending, panes)
		if !ok {
			continue
		}
		approval, err := service.Request(ctx, panes.request(call))
		if err != nil {
			logger.Warn("demo approval", "err", err)
			continue
		}
		if panes.Asked != nil {
			panes.Asked(approval)
		}
	}
}

// reportSettled tells the fleet about the watched approvals that are no longer pending and
// returns the new watch list: what is pending now.
func (panes DemoPanes) reportSettled(ctx context.Context, service *Service, watched map[string]bool, pending []Approval) map[string]bool {
	now := map[string]bool{}
	for _, approval := range pending {
		now[approval.ID] = true
	}
	for id := range watched {
		if now[id] || panes.Settled == nil {
			continue
		}
		if approval, err := service.Get(ctx, id); err == nil {
			panes.Settled(approval)
		}
	}
	return now
}

// nextFree picks the next rotation call whose pane has no pending approval (an agent waits at one
// dialog at a time), advancing next past it.
func nextFree(rotation []demoCall, next *int, pending []Approval, panes DemoPanes) (demoCall, bool) {
	busy := map[string]bool{}
	for _, approval := range pending {
		busy[approval.AgentName] = true
	}
	for range rotation {
		call := rotation[*next%len(rotation)]
		*next++
		if !busy[call.agent] && panes.Askers != nil && panes.Askers(call.agent).PaneID != "" {
			return call, true
		}
	}
	return demoCall{}, false
}
