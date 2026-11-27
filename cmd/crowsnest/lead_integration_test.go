//go:build integration

// The real-Claude test for project leads (DESIGN.md 21.2, 21.3): the built binary, started with
// --no-orchestrator (so a lead must stand on its own) on a throwaway crowsnest-test-* herdr
// session, gets a project through the HTTP API with a project instruction layer, starts the
// project's lead (haiku), and then:
//
//   - the lead answers a prompt with the SAY contract's first line;
//   - the lead knows a codeword that only the project's instruction layer gives (the layer is
//     demonstrably in effect);
//   - the lead takes a job given "to the project" and declares it done through crowsnest's MCP
//     tools, under the Stop hook's supervision;
//   - Remote Control comes up under "<instance> <project>", with its session link captured;
//   - the InstructionsLoaded hook recorded the scratch repository's CLAUDE.md for lead:<slug>.
//
// It costs a few cents: -tags integration and CROWSNEST_E2E_CLAUDE=1 (`mise run test:claude`).
// Safety: the session name is checked against ^crowsnest-test-, every child gets an environment
// without HERDR_*, and teardown stops and deletes only that session.

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
)

var leadTestSession = regexp.MustCompile(`^crowsnest-test-[a-z0-9]+$`)

type leadRig struct {
	t    *testing.T
	base string
}

func (r *leadRig) call(method, path string, body, out any) int {
	r.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, r.base+path, reader)
	request.Header.Set("Origin", r.base)
	request.Header.Set("X-Crowsnest", "1")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		r.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(response.Body)
	if out != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.Data != nil {
			_ = json.Unmarshal(envelope.Data, out)
		}
	}
	if response.StatusCode >= 300 {
		r.t.Logf("%s %s = %d %s", method, path, response.StatusCode, raw)
	}
	return response.StatusCode
}

func suffix(t *testing.T) string {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func freePort(t *testing.T) int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestIntegrationClaudeProjectLead(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	session := "crowsnest-test-" + suffix(t)
	if !leadTestSession.MatchString(session) {
		t.Fatalf("refusing session %q", session)
	}
	short, err := os.MkdirTemp("/tmp", "cnl-") // unix socket paths are limited to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	binary := filepath.Join(short, "crowsnest")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = herdr.ScrubEnv(os.Environ())
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building crowsnest: %v\n%s", err, output)
	}
	repo, err := filepath.Abs("e2e-lead-" + suffix(t)) // inside the worktree, which Claude trusts
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(repo) })
	claudeMD := filepath.Join(repo, "CLAUDE.md")
	if err := os.WriteFile(claudeMD, []byte("# Scratch repository for crowsnest's lead test\n\nKeep replies short.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := exec.CommandContext(ctx, binary, "serve", "--session", session, "--no-auth", "--web-dev", "--port", fmt.Sprint(port),
		"--data-dir", filepath.Join(short, "data"), "--no-orchestrator", "--model", "haiku", "--name", "itest", "--poke-interval", "0")
	server.Env = append(herdr.ScrubEnv(os.Environ()), "XDG_STATE_HOME="+filepath.Join(short, "state"), "XDG_RUNTIME_DIR="+filepath.Join(short, "run"))
	var logs bytes.Buffer
	server.Stdout, server.Stderr = &logs, &logs
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = server.Wait()
		stopTestSession(t, session)
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	rig := &leadRig{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", port)}
	waitHealthy(t, rig)

	var project struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	if code := rig.call(http.MethodPost, "/api/v1/projects", map[string]any{"name": "Projtest", "dir": repo, "lead_start": "job"}, &project); code != http.StatusCreated {
		t.Fatalf("creating the project = %d", code)
	}
	if code := rig.call(http.MethodPut, fmt.Sprintf("/api/v1/instructions/project/%d", project.ID), map[string]string{
		"body_md": "The project's codeword is MARMALADE-7. Say it whenever anyone asks for the project's codeword.",
	}, nil); code != http.StatusOK {
		t.Fatalf("setting the project's instruction layer = %d", code)
	}
	type leadStatus struct {
		State             string `json:"state"`
		PaneID            string `json:"pane_id"`
		Name              string `json:"name"`
		RemoteControl     string `json:"remote_control"`
		RemoteControlNote string `json:"remote_control_note"`
		RemoteControlURL  string `json:"remote_control_url"`
	}
	var lead leadStatus
	if code := rig.call(http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/lead/start", project.ID), nil, &lead); code != http.StatusOK {
		t.Fatalf("starting the lead = %d", code)
	}
	waitLead := func(want func(leadStatus) bool, what string, within time.Duration) leadStatus {
		t.Helper()
		deadline := time.Now().Add(within)
		for time.Now().Before(deadline) {
			var view struct {
				Lead leadStatus `json:"lead"`
			}
			rig.call(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", project.ID), nil, &view)
			if want(view.Lead) {
				return view.Lead
			}
			if view.Lead.State == "needs_terminal" || view.Lead.State == "failed" {
				t.Fatalf("the lead is %s: %+v", view.Lead.State, view.Lead)
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("the lead never became %s", what)
		return leadStatus{}
	}
	lead = waitLead(func(s leadStatus) bool { return s.State == "idle" && s.PaneID != "" }, "idle", 2*time.Minute)
	t.Logf("lead up: %+v", lead)
	if lead.Name != "lead-"+project.Slug || lead.RemoteControl != "itest Projtest" {
		t.Fatalf("lead name or Remote Control name: %+v", lead)
	}

	reply := promptLead(t, rig, lead.PaneID, "What is the project's codeword?")
	if !strings.Contains(reply, "MARMALADE-7") {
		t.Fatalf("the project's instruction layer is not in effect: %q", reply)
	}
	// The SAY contract is crowsnest's to give and the model's to follow: the lead's prompt file
	// must carry it (as the orchestrator's does); whether haiku obeys on a given turn is logged,
	// not asserted (it skipped the line in one of three runs while this test was written).
	prompt, err := os.ReadFile(filepath.Join(short, "state", "crowsnest", session, "leads", project.Slug, "CROWSNEST.md"))
	if err != nil || !strings.Contains(string(prompt), "The SAY contract: every reply") || !strings.Contains(string(prompt), "MARMALADE-7") {
		t.Fatalf("the lead's CROWSNEST.md lacks the SAY contract or its project layer (err %v)", err)
	}
	if strings.HasPrefix(strings.TrimSpace(reply), "SAY:") {
		t.Log("the reply starts with the SAY line")
	} else {
		t.Logf("NOTE: haiku did not start this reply with the SAY line: %q", reply)
	}
	waitLead(func(s leadStatus) bool { return s.State == "idle" }, "idle after the reply", time.Minute)

	var job struct {
		ID string `json:"id"`
	}
	brief := "Prove the job protocol works.\n\nDo nothing else: call crowsnest's done tool for this job with result_md \"done-ok\", verified [] and not_verified []."
	if code := rig.call(http.MethodPost, "/api/v1/jobs", map[string]any{"brief": brief, "project_id": project.ID}, &job); code != http.StatusCreated {
		t.Fatalf("giving the project a job = %d", code)
	}
	var detail struct {
		State     string `json:"state"`
		AgentName string `json:"agent_name"`
		ResultMD  string `json:"result_md"`
		ProjectID int64  `json:"project_id"`
	}
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		rig.call(http.MethodGet, "/api/v1/jobs/"+job.ID, nil, &detail)
		if detail.State == "done" || detail.State == "failed" || detail.State == "cancelled" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("job %s: %+v", job.ID, detail)
	if detail.State != "done" || detail.AgentName != "lead-"+project.Slug || detail.ProjectID != project.ID || !strings.Contains(detail.ResultMD, "done-ok") {
		t.Fatalf("the lead did not hold and finish its job: %+v", detail)
	}

	lead = waitLead(func(s leadStatus) bool { return s.RemoteControlNote != "" }, "noted for Remote Control", time.Minute)
	if lead.RemoteControlNote != "active" {
		t.Fatalf("Remote Control: %q", lead.RemoteControlNote)
	}
	// Active Remote Control carries its session link (DESIGN.md 7.10), as GET /leads serves it.
	lead = waitLead(func(s leadStatus) bool { return s.RemoteControlURL != "" }, "given its Remote Control link", time.Minute)
	if !orch.ValidRemoteControlURL(lead.RemoteControlURL) {
		t.Fatalf("Remote Control link %q", lead.RemoteControlURL)
	}

	var record struct {
		Layers []struct {
			Layer string `json:"layer"`
		} `json:"layers"`
		Loaded []struct {
			Path string `json:"path"`
		} `json:"loaded"`
	}
	rig.call(http.MethodGet, "/api/v1/agents-instructions/lead:"+project.Slug, nil, &record)
	found := false
	for _, file := range record.Loaded {
		found = found || strings.HasSuffix(file.Path, filepath.Join(filepath.Base(repo), "CLAUDE.md"))
	}
	t.Logf("instruction record: %+v", record)
	if !found || len(record.Layers) == 0 {
		t.Fatalf("InstructionsLoaded did not record %s, or the start's layers are missing: %+v", claudeMD, record)
	}
}

// promptLead sends text to the lead and waits for an assistant reply after it.
func promptLead(t *testing.T, rig *leadRig, pane, text string) string {
	t.Helper()
	target := "/api/v1/agents/" + strings.ReplaceAll(pane, ":", "%3A")
	var before struct {
		Cursor string `json:"cursor"`
	}
	rig.call(http.MethodGet, target+"/messages", nil, &before)
	var result struct {
		Started bool `json:"started"`
	}
	if code := rig.call(http.MethodPost, target+"/prompt", map[string]any{"text": text}, &result); code != http.StatusOK || !result.Started {
		t.Fatalf("prompt = %d %+v", code, result)
	}
	cursor := before.Cursor
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var page struct {
			Cursor   string `json:"cursor"`
			Messages []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"messages"`
		}
		rig.call(http.MethodGet, target+"/messages?after="+cursor, nil, &page)
		for _, message := range page.Messages {
			if message.Kind == "assistant" && strings.TrimSpace(message.Text) != "" {
				t.Logf("lead replied: %q", message.Text)
				return message.Text
			}
		}
		if page.Cursor != "" {
			cursor = page.Cursor
		}
		time.Sleep(time.Second)
	}
	t.Fatal("no reply from the lead within 2 minutes")
	return ""
}

func waitHealthy(t *testing.T, rig *leadRig) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(rig.base + "/api/v1/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("crowsnest did not come up")
}

func stopTestSession(t *testing.T, name string) {
	t.Helper()
	if !leadTestSession.MatchString(name) {
		t.Errorf("refusing to tear down %q", name)
		return
	}
	for _, args := range [][]string{{"session", "stop", name, "--json"}, {"session", "delete", name, "--json"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "herdr", args...)
		cmd.Env = herdr.ScrubEnv(os.Environ())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Logf("herdr %s: %v: %s", strings.Join(args, " "), err, output)
		}
		cancel()
	}
}
