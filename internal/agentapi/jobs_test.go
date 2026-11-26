package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

// startRunningJob creates a job over the socket and marks it running, as a launch would.
func startRunningJob(t *testing.T, f *socketFixture) string {
	t.Helper()
	ctx := context.Background()
	client := NewClient(f.socket, questions.Caller{})
	job, err := client.CreateJob(ctx, jobs.CreateInput{Brief: "Build the thing", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	numeric, _ := jobs.ParseID(job.ID)
	record, err := f.db.GetJob(ctx, numeric)
	if err != nil {
		t.Fatal(err)
	}
	record.State = string(jobs.StateRunning)
	if err := f.db.UpdateJob(ctx, record); err != nil {
		t.Fatal(err)
	}
	return job.ID
}

func TestSocketJobVerbs(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	id := startRunningJob(t, f)
	client := NewClient(f.socket, questions.Caller{JobID: id})

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
	child, err := client.CreateJob(ctx, jobs.CreateInput{Brief: "A child", Dir: t.TempDir(), Role: "tester"})
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
	client := NewClient(f.socket, questions.Caller{JobID: id})
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
	if out, err = NewClient(f.socket, questions.Caller{}).Hook(ctx, jobs.HookStop, json.RawMessage(`{}`)); err != nil || out != "" {
		t.Fatalf("no job: %q %v", out, err)
	}
}

func TestMCPJobToolsUseTheEnvironmentsJob(t *testing.T) {
	f := startSocket(t)
	ctx := context.Background()
	id := startRunningJob(t, f)
	session := connectMCP(t, NewClient(f.socket, questions.Caller{JobID: id}))
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
	bare := connectMCP(t, NewClient(f.socket, questions.Caller{}))
	result, _ := bare.CallTool(ctx, &mcp.CallToolParams{Name: "job_update", Arguments: map[string]any{"progress": "x"}})
	if !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "job_id") {
		t.Fatalf("no job: %+v", result)
	}
}

func isStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}
