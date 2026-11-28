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
	"sync"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &lockedBuffer{}
	t.Cleanup(func() {
		cancel()
		stopTestSession(t, session)
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	env := append(herdr.ScrubEnv(os.Environ()), "XDG_STATE_HOME="+filepath.Join(short, "state"), "XDG_RUNTIME_DIR="+filepath.Join(short, "run"))
	rig := startLeadServer(t, ctx, logs, env, binary, "serve", "--session", session, "--no-auth", "--web-dev",
		"--data-dir", filepath.Join(short, "data"), "--no-orchestrator", "--model", "haiku", "--name", "itest", "--poke-interval", "0")

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
		SessionID         string `json:"session_id"`
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
			if view.Lead.State == "needs_terminal" && acceptLeadTrust(t, rig, view.Lead.PaneID) {
				continue // answered as the human would on the trust card; it starts now
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

	// New instructions for a running lead (DESIGN.md 7.5): the codeword changes in its layer; the
	// lead's watch sees that what it would start with now differs, restarts it on the same
	// conversation at its idle moment, and the resumed conversation is given the new text (Claude
	// Code keeps a resumed conversation's original system prompt, so the SessionStart hook carries
	// it). Asked again, it gives the new codeword.
	if code := rig.call(http.MethodPut, fmt.Sprintf("/api/v1/instructions/project/%d", project.ID), map[string]string{
		"body_md": "The project's codeword is QUINCE-9. Say it whenever anyone asks for the project's codeword.",
	}, nil); code != http.StatusOK {
		t.Fatalf("changing the project's instruction layer = %d", code)
	}
	promptFile := filepath.Join(short, "state", "crowsnest", session, "leads", project.Slug, "CROWSNEST.md")
	restartBy := time.Now().Add(2 * time.Minute)
	for time.Now().Before(restartBy) {
		if body, err := os.ReadFile(promptFile); err == nil && strings.Contains(string(body), "QUINCE-9") {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if body, _ := os.ReadFile(promptFile); !strings.Contains(string(body), "QUINCE-9") {
		t.Fatal("the lead was not restarted for its new instructions within 2 minutes")
	}
	conversation := lead.SessionID
	lead = waitLead(func(s leadStatus) bool { return s.State == "idle" && s.PaneID != "" }, "idle after the restart for new instructions", 2*time.Minute)
	if conversation == "" || lead.SessionID != conversation {
		t.Fatalf("the restart for new instructions did not keep the conversation: %q → %q", conversation, lead.SessionID)
	}
	reply = promptLead(t, rig, lead.PaneID, "What is the project's codeword now? Reply with the codeword only.")
	if !strings.Contains(reply, "QUINCE-9") {
		t.Fatalf("the restarted lead does not follow its new instructions: %q", reply)
	}
	waitLead(func(s leadStatus) bool { return s.State == "idle" }, "idle after the second reply", time.Minute)

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

// lockedBuffer is the server's log, written by its process while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startLeadServer starts the built crowsnest on a free port and waits until that very process
// answers. The suite runs packages side by side (test:claude), and a port picked free can be taken
// by another package's server before crowsnest binds it: then this server exits at once and the
// health check would be answered by the other one. So the process must still be running after
// health answers, and its log must name the port; otherwise another port is tried.
func startLeadServer(t *testing.T, ctx context.Context, logs *lockedBuffer, env []string, binary string, args ...string) *leadRig {
	t.Helper()
	for attempt := 1; attempt <= 3; attempt++ {
		port := freePort(t)
		server := exec.CommandContext(ctx, binary, append(args, "--port", fmt.Sprint(port))...) //nolint:gosec // the binary this test built
		server.Env = env
		server.Stdout, server.Stderr = logs, logs
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() {
			_ = server.Wait()
			close(exited)
		}()
		t.Cleanup(func() {
			if server.Process != nil {
				_ = server.Process.Signal(os.Interrupt)
			}
			select {
			case <-exited:
			case <-time.After(20 * time.Second):
				_ = server.Process.Kill()
			}
		})
		rig := &leadRig{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", port)}
		if waitOwnHealthy(rig, exited, logs, port) {
			return rig
		}
		t.Logf("attempt %d: crowsnest did not come up on port %d (taken by another test?); trying another port", attempt, port)
	}
	t.Fatal("crowsnest did not come up")
	return nil
}

// acceptLeadTrust answers Claude's folder-trust prompt in the lead's pane through the trust card's
// own API path (the screen, then the keys pinned to its hash), once, as the human would. The suite
// runs many Claude agents side by side, and each rewrites ~/.claude.json: one can write back an
// older copy over the trust crowsnest recorded for the project, so the lead may meet the prompt
// although crowsnest trusted the folder. What this test proves (the lead's layer, its jobs, Remote
// Control, the hook) does not depend on who answered that prompt.
func acceptLeadTrust(t *testing.T, rig *leadRig, pane string) bool {
	t.Helper()
	if pane == "" || trustAnswered[pane] {
		return false
	}
	var screen struct {
		Text string `json:"text"`
		Hash string `json:"screen_hash"`
	}
	if code := rig.call(http.MethodGet, "/api/v1/agents/"+pane+"/screen", nil, &screen); code != http.StatusOK ||
		!strings.Contains(screen.Text, "Yes, I trust this folder") {
		return false
	}
	trustAnswered[pane] = true
	t.Logf("NOTE: the lead met Claude's folder-trust prompt (another agent's write to ~/.claude.json?); accepting it on the trust card's path")
	if code := rig.call(http.MethodPost, "/api/v1/agents/"+pane+"/keys", map[string]any{"keys": []string{"down", "Enter"}, "screen_hash": screen.Hash}, nil); code != http.StatusOK {
		t.Fatalf("answering the folder-trust prompt = %d", code)
	}
	return true
}

// trustAnswered are the panes whose trust prompt the test answered (once each).
var trustAnswered = map[string]bool{}

// waitOwnHealthy reports whether the server that is still running (exited open) answers health
// on port and has logged that port.
func waitOwnHealthy(rig *leadRig, exited <-chan struct{}, logs *lockedBuffer, port int) bool {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return false
		default:
		}
		if response, err := http.Get(rig.base + "/api/v1/health"); err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && strings.Contains(logs.String(), fmt.Sprintf(":%d", port)) {
				return true
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
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
