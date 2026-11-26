package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// agentSocket serves the agent API on a short temp socket, exports it as CROWSNEST_SOCKET with
// an agent token for pane w2:p3 as CROWSNEST_TOKEN, and returns the service behind it.
func agentSocket(t *testing.T) *questions.Service {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := questions.New(questions.Options{Store: db})
	identity := agentid.New(db, nil)
	token, err := identity.Mint(context.Background(), agentid.Binding{PaneID: "w2:p3", Kind: agentid.KindPane})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "cn-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := agentapi.Listen(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = agentapi.NewServer(agentapi.ServerOptions{Questions: service, Identity: identity}).Serve(ctx, listener)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	t.Setenv(agentapi.EnvSocket, path)
	t.Setenv(agentapi.EnvToken, token)
	t.Setenv("HERDR_PANE_ID", "orchestrator") // what a pane says about itself is not believed
	return service
}

// runCLI runs the root command with args and stdin, returning stdout and the exit code.
func runCLI(t *testing.T, stdin string, args ...string) (string, int, error) {
	t.Helper()
	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), exitCodeFor(err), err
}

func TestAskPrintsTheIDAndRecordsThePane(t *testing.T) {
	service := agentSocket(t)
	out, code, err := runCLI(t, `{"title":"Ship?","kind":"confirm","recommend":"yes"}`, "ask", "--file", "-")
	if code != 0 || strings.TrimSpace(out) != "q_1" {
		t.Fatalf("ask: %q code %d err %v", out, code, err)
	}
	question, _ := service.Get(context.Background(), "q_1")
	if question.SourcePane != "w2:p3" || question.Wait {
		t.Fatalf("stored %+v", question)
	}
}

func TestAskExitCodes(t *testing.T) {
	service := agentSocket(t)
	file := filepath.Join(t.TempDir(), "q.json")
	if err := os.WriteFile(file, []byte(`{"title":"Deploy?","kind":"confirm","recommend":"no"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settle := func(answer bool) {
		go func() {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				open, _ := service.List(context.Background(), "open")
				if len(open) == 1 {
					if answer {
						_, _ = service.Answer(context.Background(), open[0].ID, questions.AnswerInput{Value: json.RawMessage(`"yes"`), Note: "ok"})
					} else {
						_, _ = service.Cancel(context.Background(), open[0].ID, false)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}

	settle(true)
	out, code, err := runCLI(t, "", "ask", "--file", file, "--wait")
	var result AskResult
	if code != 0 || json.Unmarshal([]byte(out), &result) != nil || result.Status != "answered" || result.AnswerValue != "yes" || result.AnswerNote != "ok" {
		t.Fatalf("answered: %q code %d err %v", out, code, err)
	}

	settle(false)
	out, code, _ = runCLI(t, "", "ask", "--file", file, "--wait")
	if code != exitUnanswered || !strings.Contains(out, `"status":"cancelled"`) {
		t.Fatalf("cancelled: %q code %d", out, code)
	}

	if _, code, _ := runCLI(t, `{"title":"x","kind":"confirm"}`, "ask", "-f", "-"); code != 1 {
		t.Fatalf("validation error: code %d", code)
	}
	if _, code, _ := runCLI(t, `not json`, "ask", "-f", "-"); code != 2 {
		t.Fatalf("bad JSON: code %d", code)
	}
	if _, code, _ := runCLI(t, ``, "ask"); code != 2 {
		t.Fatalf("missing --file: code %d", code)
	}
}

func TestNotifyAndSay(t *testing.T) {
	service := agentSocket(t)
	out, code, err := runCLI(t, "", "notify", "Backup done\nAll 3 volumes, 14 s", "--urgency", "low")
	if code != 0 || strings.TrimSpace(out) != "q_1" {
		t.Fatalf("notify: %q %d %v", out, code, err)
	}
	notice, _ := service.Get(context.Background(), "q_1")
	if notice.Kind != questions.KindNotice || notice.Title != "Backup done" || notice.BodyMD != "All 3 volumes, 14 s" || notice.Urgency != questions.UrgencyLow {
		t.Fatalf("notice %+v", notice)
	}
	if _, code, err := runCLI(t, "", "say", "Tests are green."); code != 0 {
		t.Fatalf("say: %d %v", code, err)
	}
}

func TestAgentCommandsWithoutAServer(t *testing.T) {
	t.Setenv(agentapi.EnvSocket, "")
	if _, code, err := runCLI(t, "", "say", "hi"); code != 1 || !strings.Contains(err.Error(), "CROWSNEST_SOCKET is not set") {
		t.Fatalf("unset socket: %d %v", code, err)
	}
	t.Setenv(agentapi.EnvSocket, filepath.Join(t.TempDir(), "gone.sock"))
	if _, code, err := runCLI(t, "", "notify", "hi"); code != 1 || !strings.Contains(err.Error(), "is `crowsnest serve` running?") {
		t.Fatalf("server down: %d %v", code, err)
	}
}
