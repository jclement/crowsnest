package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

// startRunningJob creates a job as the human would (the browser) and marks it running in its
// directory, as a launch would. It returns the job's id and directory.
func startRunningJob(t *testing.T, f *socketFixture) string {
	t.Helper()
	id, _ := startRunningJobIn(t, f)
	return id
}

func startRunningJobIn(t *testing.T, f *socketFixture) (string, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	job, err := f.jobs.Create(ctx, jobs.CreateInput{Brief: "Build the thing", Dir: dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	numeric, _ := jobs.ParseID(job.ID)
	record, err := f.db.GetJob(ctx, numeric)
	if err != nil {
		t.Fatal(err)
	}
	record.State, record.WorkDir = string(jobs.StateRunning), dir
	if err := f.db.UpdateJob(ctx, record); err != nil {
		t.Fatal(err)
	}
	return job.ID, dir
}

func TestSocketJobVerbs(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	id, dir := startRunningJobIn(t, f)
	client := NewClient(f.socket, f.token(t, "w5:p1", id)).WithJob(id)

	if _, err := client.UpdateJob(ctx, id, jobs.UpdateInput{}); !isStatus(err, 400) {
		t.Fatalf("empty update: %v", err)
	}
	job, err := client.UpdateJob(ctx, id, jobs.UpdateInput{Progress: "reading the code"})
	if err != nil || job.Progress != "reading the code" {
		t.Fatalf("update %+v %v", job, err)
	}
	if _, err := client.UpdateJob(ctx, id, jobs.UpdateInput{Progress: "x", State: "blocked"}); !isStatus(err, 400) {
		t.Fatalf("blocked without a reason: %v", err)
	}
	if job, _ = client.UpdateJob(ctx, id, jobs.UpdateInput{Progress: "x", State: "blocked", Reason: "CI is down"}); job.State != jobs.StateBlocked {
		t.Fatalf("blocked: %+v", job)
	}
	if job, _ = client.UpdateJob(ctx, id, jobs.UpdateInput{Progress: "back", State: "running"}); job.State != jobs.StateRunning {
		t.Fatalf("running: %+v", job)
	}
	text, err := client.JobContext(ctx, id)
	if err != nil || !strings.Contains(text, "Build the thing") {
		t.Fatalf("context %q %v", text, err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	child, err := client.CreateJob(ctx, jobs.CreateInput{Brief: "A child", Dir: sub, Role: "tester"})
	if err != nil || child.ParentID != id {
		t.Fatalf("child %+v %v", child, err)
	}
	list, err := client.ListJobs(ctx, "queued")
	if err != nil || len(list) != 1 || list[0].ID != child.ID {
		t.Fatalf("list %+v %v", list, err)
	}
	if _, err := client.DoneJob(ctx, id, jobs.DoneInput{ResultMD: "ok"}); !isStatus(err, 400) {
		t.Fatalf("done without evidence: %v", err)
	}
	if job, err = client.DoneJob(ctx, id, jobs.DoneInput{ResultMD: "ok", Verified: []string{"go test"}, NotVerified: []string{}}); err != nil || job.State != jobs.StateDone {
		t.Fatalf("done %+v %v", job, err)
	}
	if _, err := client.FailJob(ctx, id, "too late"); !isStatus(err, 400) {
		t.Fatalf("fail after done: %v", err)
	}
	if _, err := client.GetJob(ctx, "j_999"); !isStatus(err, 404) {
		t.Fatalf("missing job: %v", err)
	}
}

func TestSocketHookStopBlocksAndAllows(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	id := startRunningJob(t, f)
	client := NewClient(f.socket, f.token(t, "w5:p1", id)).WithJob(id)
	out, err := client.Hook(ctx, jobs.HookStop, json.RawMessage(`{"session_id":"s1","stop_hook_active":false,"unknown_future_field":1}`))
	if err != nil || !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("stop: %q %v", out, err)
	}
	if _, err := client.FailJob(ctx, id, "cannot"); err != nil {
		t.Fatal(err)
	}
	if out, _ = client.Hook(ctx, jobs.HookStop, json.RawMessage(`{"stop_hook_active":true}`)); out != "" {
		t.Fatalf("failed job may stop: %q", out)
	}
	// No job at all: allowed, silently.
	if out, err = NewClient(f.socket, "").Hook(ctx, jobs.HookStop, json.RawMessage(`{}`)); err != nil || out != "" {
		t.Fatalf("no job: %q %v", out, err)
	}
}

// TestJobVerbsFollowTheToken is R1b N2 for the job protocol: naming a job is not holding it. An
// unidentified caller, or another job's agent, cannot change a job, create one, or have its hook
// count for it; the agent in the pane holding the job (the orchestrator's case) can.
func TestJobVerbsFollowTheToken(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	id := startRunningJob(t, f)
	numeric, _ := jobs.ParseID(id)
	record, _ := f.db.GetJob(ctx, numeric)
	record.PaneID = "w1:p1"
	if err := f.db.UpdateJob(ctx, record); err != nil {
		t.Fatal(err)
	}
	for name, client := range map[string]*Client{
		"unidentified": NewClient(f.socket, "").WithJob(id),
		"another job":  NewClient(f.socket, f.token(t, "w7:p1", "j_99")).WithJob(id),
		"another pane": NewClient(f.socket, f.token(t, "w7:p2", "")).WithJob(id),
	} {
		if _, err := client.UpdateJob(ctx, id, jobs.UpdateInput{Progress: "hijacked"}); !isStatus(err, 403) {
			t.Fatalf("%s updated the job: %v", name, err)
		}
		if _, err := client.FailJob(ctx, id, "hijacked"); !isStatus(err, 403) {
			t.Fatalf("%s failed the job: %v", name, err)
		}
		if out, _ := client.Hook(ctx, jobs.HookStop, json.RawMessage(`{"stop_hook_active":false}`)); out != "" {
			t.Fatalf("%s's hook was counted for the job: %q", name, out)
		}
	}
	if _, err := NewClient(f.socket, "").CreateJob(ctx, jobs.CreateInput{Brief: "x", Dir: t.TempDir()}); !isStatus(err, 403) {
		t.Fatalf("an unidentified caller created a job: %v", err)
	}
	holder := NewClient(f.socket, f.token(t, "w1:p1", "")) // the orchestrator: a pane token, no job
	if job, err := holder.UpdateJob(ctx, id, jobs.UpdateInput{Progress: "mine"}); err != nil || job.Progress != "mine" {
		t.Fatalf("the agent holding the job by pane: %+v %v", job, err)
	}
	if out, _ := holder.Hook(ctx, jobs.HookStop, json.RawMessage(`{"stop_hook_active":false}`)); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("the holder's stop hook was not mapped to its job: %q", out)
	}
}

func TestMCPJobToolsUseTheEnvironmentsJob(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	id := startRunningJob(t, f)
	client := NewClient(f.socket, f.token(t, "w5:p1", id))
	client.job = id // as CROWSNEST_JOB in the MCP server's environment
	session := connectMCP(t, client)
	call := func(name string, args map[string]any) (string, bool) {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return result.Content[0].(*mcp.TextContent).Text, result.IsError
	}
	if text, isErr := call("job_update", map[string]any{"progress": "testing"}); isErr || !strings.Contains(text, "Keep going") {
		t.Fatalf("job_update %q", text)
	}
	if text, isErr := call("job_get", map[string]any{}); isErr || !strings.Contains(text, "testing") {
		t.Fatalf("job_get %q", text)
	}
	// An ask under the job is recorded with its job id and says how to keep going.
	if text, isErr := call("ask", map[string]any{"title": "Which DB?", "kind": "manual"}); isErr || !strings.Contains(text, "Recorded as q_1") || !strings.Contains(text, "waiting=true") {
		t.Fatalf("ask %q", text)
	}
	if open, _ := f.service.List(ctx, "open"); len(open) != 1 || open[0].JobID != id {
		t.Fatalf("question %+v", open)
	}
	if text, isErr := call("done", map[string]any{"result_md": "ok", "verified": []string{}, "not_verified": []string{"all of it"}}); isErr || !strings.Contains(text, "is done") {
		t.Fatalf("done %q", text)
	}
	// Without a job anywhere, the tools say how to name one.
	bare := connectMCP(t, NewClient(f.socket, f.token(t, "w6:p1", "")))
	result, _ := bare.CallTool(ctx, &mcp.CallToolParams{Name: "job_update", Arguments: map[string]any{"progress": "x"}})
	if !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "job_id") {
		t.Fatalf("no job: %+v", result)
	}
}

// R2-C06: the orchestrator's ask names the job it holds (its token names only its pane); the
// question must be stored under that job, and nobody else may attach a question to it.
func TestOrchestratorAsksAboutTheJobItHolds(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	id := startRunningJob(t, f)
	numeric, _ := jobs.ParseID(id)
	record, _ := f.db.GetJob(ctx, numeric)
	record.PaneID = "w1:p1" // held by the orchestrator in w1:p1
	if err := f.db.UpdateJob(ctx, record); err != nil {
		t.Fatal(err)
	}
	session := connectMCP(t, NewClient(f.socket, f.token(t, "w1:p1", "")))
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask", Arguments: map[string]any{"title": "Which DB?", "kind": "manual", "job_id": id}})
	if err != nil || result.IsError {
		t.Fatalf("ask: %+v %v", result, err)
	}
	if text := result.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "waiting=true") {
		t.Fatalf("the reply should treat it as a job question: %q", text)
	}
	if open, _ := f.service.List(ctx, "open"); len(open) != 1 || open[0].JobID != id || open[0].SourcePane != "w1:p1" {
		t.Fatalf("the question lost its job: %+v", open)
	}
	for name, client := range map[string]*Client{
		"another pane":      NewClient(f.socket, f.token(t, "w7:p2", "")),
		"another job agent": NewClient(f.socket, f.token(t, "w5:p1", "j_99")),
		"unidentified":      NewClient(f.socket, ""),
	} {
		if _, err := client.Ask(ctx, questions.AskInput{Title: "Mine now?", Kind: questions.KindManual, JobID: id}); !isStatus(err, 403) {
			t.Fatalf("%s attached a question to %s: %v", name, id, err)
		}
	}
}

func isStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}
