// The glue between herdr and the transcript manager: where an agent's transcript is (its pane,
// its cwd, the Claude session id herdr reports, or the orchestrator's own when herdr has none),
// and the pane text to show when there is no transcript at all.

package orch

import (
	"context"
	"fmt"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/transcript"
)

// fallbackReadLines is how much scrollback the pane-text fallback asks herdr for.
const fallbackReadLines = 400

// TranscriptLookup returns a transcript.LookupFunc over herdr. A target is an agent name or pane
// id. For the orchestrator's pane, the session id the orchestrator started Claude with fills in
// when herdr has not detected one yet. o may be nil (no orchestrator).
func TranscriptLookup(client herdr.Client, o *Orchestrator) transcript.LookupFunc {
	return func(ctx context.Context, target string) (transcript.Origin, error) {
		ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
		defer cancel()
		agent, err := client.AgentGet(ctx, target)
		if herdr.IsNotFound(err) {
			return transcript.Origin{}, fmt.Errorf("agent %s: %w", target, transcript.ErrNoAgent)
		}
		if err != nil {
			return transcript.Origin{}, err
		}
		origin := transcript.Origin{PaneID: agent.PaneID, Cwd: firstNonEmpty(agent.Cwd, agent.ForegroundCwd), SessionID: sessionOf(agent, "")}
		if o != nil {
			// For its own pane the orchestrator's session id wins: it is what Claude was started
			// with, and the watch takes herdr's value whenever that really changes (/clear),
			// while right after a restart herdr can still be reporting the previous session.
			if status := o.Status(); status.PaneID == agent.PaneID && status.SessionID != "" {
				origin.SessionID = status.SessionID
			}
		}
		return origin, nil
	}
}

// PaneFallback returns a transcript.FallbackFunc that reads a pane's recent text, soft wraps
// joined, without colours.
func PaneFallback(client herdr.Client) transcript.FallbackFunc {
	return func(ctx context.Context, paneID string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
		defer cancel()
		read, err := client.PaneRead(ctx, paneID, herdr.ReadOptions{Source: herdr.SourceRecentUnwrapped, Lines: fallbackReadLines})
		if err != nil {
			return "", err
		}
		return read.Text, nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
