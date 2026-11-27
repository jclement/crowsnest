// job_adopt over the socket and MCP (DESIGN.md 7.10): the orchestrator records work it was given
// in conversation and gets the id back, the job is held in its own pane; a job agent and an
// unidentified caller are refused.

package agentapi

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// startSocketWithOrchestrator is startSocket whose jobs service knows the orchestrator is in
// pane w1:p1.
func startSocketWithOrchestrator(t *testing.T) *socketFixture {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	events := hub.New()
	service := questions.New(questions.Options{Store: db, Hub: events})
	identity := agentid.New(db, nil)
	jobService := jobs.New(jobs.Options{Store: db, Hub: events, Orchestrator: func() jobs.OrchestratorInfo {
		return jobs.OrchestratorInfo{PaneID: "w1:p1", Ready: true}
	}})
	path := filepath.Join(shortDir(t), "run", "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := Listen(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = NewServer(ServerOptions{Questions: service, Identity: identity, Jobs: jobService, Hub: events, Version: "test"}).Serve(ctx, listener)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return &socketFixture{socket: path, service: service, hub: events, identity: identity, jobs: jobService, db: db}
}

func TestJobAdoptTool(t *testing.T) {
	f := startSocketWithOrchestrator(t)
	ctx := context.Background()
	session := connectMCP(t, NewClient(f.socket, f.token(t, "w1:p1", "")))
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "job_adopt", Arguments: map[string]any{"title": "Rename the config flag", "brief": "Asked in the Claude app."}})
	if err != nil || result.IsError {
		t.Fatalf("job_adopt %v %+v", err, result)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	id := regexp.MustCompile(`j_\d+`).FindString(text)
	if id == "" || !strings.Contains(text, "you hold it") {
		t.Fatalf("result %q", text)
	}
	detail, err := f.jobs.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.AgentName != jobs.OrchestratorName || detail.PaneID != "w1:p1" || detail.State != jobs.StateRunning || detail.Title != "Rename the config flag" {
		t.Fatalf("adopted job %+v", detail.Job)
	}
	// The holder reports on it with the existing tools.
	if _, err := NewClient(f.socket, f.token(t, "w1:p1", "")).UpdateJob(ctx, id, jobs.UpdateInput{Progress: "renaming"}); err != nil {
		t.Fatalf("job_update on the adopted job: %v", err)
	}

	agent := connectMCP(t, NewClient(f.socket, f.token(t, "w2:p1", "j_9")))
	refused, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "job_adopt", Arguments: map[string]any{"title": "Mine now", "brief": "x"}})
	if err != nil || !refused.IsError || !strings.Contains(refused.Content[0].(*mcp.TextContent).Text, "job agent") {
		t.Fatalf("job agent: %v %q", err, refused.Content[0].(*mcp.TextContent).Text)
	}
	if _, err := NewClient(f.socket, "").AdoptJob(ctx, jobs.AdoptInput{Title: "x"}); !isStatus(err, 403) {
		t.Fatalf("no token: %v", err)
	}
}
