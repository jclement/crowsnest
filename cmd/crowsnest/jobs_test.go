package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// jobSocket serves the agent API with jobs on a short temp socket and exports it.
func jobSocket(t *testing.T) (*jobs.Service, *store.Store, string) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := jobs.New(jobs.Options{Store: db})
	identity := agentid.New(db, nil)
	token, err := identity.Mint(context.Background(), agentid.Binding{PaneID: "w2:p3", Kind: agentid.KindPane})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "cn-job-")
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
		_ = agentapi.NewServer(agentapi.ServerOptions{Questions: questions.New(questions.Options{Store: db}), Jobs: service, Identity: identity}).Serve(ctx, listener)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	t.Setenv(agentapi.EnvSocket, path)
	t.Setenv(agentapi.EnvToken, token) // the pane w2:p3, which runningJob's jobs are held in
	t.Setenv(agentapi.EnvJob, "")
	return service, db, path
}

// runningJob creates a job and marks it running.
func runningJob(t *testing.T, service *jobs.Service, db *store.Store) string {
	t.Helper()
	job, err := service.Create(context.Background(), jobs.CreateInput{Brief: "Do the thing", Dir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	numeric, _ := jobs.ParseID(job.ID)
	record, _ := db.GetJob(context.Background(), numeric)
	record.State, record.PaneID = string(jobs.StateRunning), "w2:p3"
	if err := db.UpdateJob(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	return job.ID
}

func TestHookFailsOpen(t *testing.T) {
	dead := filepath.Join(t.TempDir(), "nobody.sock")
	// A socket file that nobody answers on, too.
	stale := filepath.Join(os.TempDir(), "cn-stale-"+time.Now().Format("150405.000000")+".sock")
	listener, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	t.Cleanup(func() { _ = os.Remove(stale) })
	t.Setenv(agentapi.EnvJob, "")
	cases := []struct {
		name  string
		stdin string
		args  []string
		env   string
	}{
		{"dead socket", `{"stop_hook_active":false}`, []string{"hook", "stop", "--socket", dead, "--job", "j_1"}, ""},
		{"stale socket", `{}`, []string{"hook", "stop", "--socket", stale}, ""},
		{"no socket anywhere", `{}`, []string{"hook", "stop"}, ""},
		{"not JSON", `this is not json`, []string{"hook", "stop", "--socket", dead}, ""},
		{"empty stdin", ``, []string{"hook", "session-start", "--socket", dead}, ""},
		{"unknown flag from a newer crowsnest", `{}`, []string{"hook", "stop", "--socket", dead, "--future-flag", "x"}, ""},
		{"no event", `{}`, []string{"hook"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(agentapi.EnvSocket, tc.env)
			out, code, err := runCLI(t, tc.stdin, tc.args...)
			if code != 0 || out != "" || err != nil {
				t.Fatalf("hook must print nothing and exit 0: out %q code %d err %v", out, code, err)
			}
		})
	}
}

func TestHookTimesOutOpen(t *testing.T) {
	// A server that accepts and never answers: the hook gives up after hookTimeout, silently.
	dir, _ := os.MkdirTemp("/tmp", "cn-slow-")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "slow.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
		}
	}()
	previous := hookTimeout
	hookTimeout = 200 * time.Millisecond
	t.Cleanup(func() { hookTimeout = previous })
	started := time.Now()
	out, code, _ := runCLI(t, `{}`, "hook", "stop", "--socket", path, "--job", "j_1")
	if code != 0 || out != "" || time.Since(started) > 3*time.Second {
		t.Fatalf("slow server: out %q code %d after %s", out, code, time.Since(started))
	}
}

func TestHookPrintsTheServersDecision(t *testing.T) {
	service, db, path := jobSocket(t)
	id := runningJob(t, service, db)
	out, code, err := runCLI(t, `{"session_id":"s","stop_hook_active":false}`, "hook", "stop", "--socket", path, "--job", id)
	if code != 0 || err != nil || !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "job "+id) {
		t.Fatalf("stop hook: %q code %d err %v", out, code, err)
	}
	out, _, _ = runCLI(t, `{"source":"startup","session_id":"s"}`, "hook", "session-start", "--job", id)
	if !strings.Contains(out, "Do the thing") {
		t.Fatalf("session-start: %q", out)
	}
}

func TestJobCommands(t *testing.T) {
	service, db, _ := jobSocket(t)
	id := runningJob(t, service, db)
	if _, code, _ := runCLI(t, "", "job", "update", "doing it"); code != 2 {
		t.Fatalf("no job id should be a usage error, got %d", code)
	}
	t.Setenv(agentapi.EnvJob, id)
	if out, code, err := runCLI(t, "", "job", "update", "writing tests"); code != 0 || !strings.HasPrefix(out, id+" running") {
		t.Fatalf("update: %q %d %v", out, code, err)
	}
	if out, _, _ := runCLI(t, "", "job", "get"); !strings.Contains(out, "writing tests") {
		t.Fatalf("get: %q", out)
	}
	if _, code, _ := runCLI(t, "", "job", "done", "--result", "ok", "--verified", "go test"); code != 2 {
		t.Fatalf("done without --not-verified: code %d", code)
	}
	if out, code, err := runCLI(t, "", "job", "done", "--result", "ok", "--verified", "go test ./...", "--not-verified", "none"); code != 0 || !strings.Contains(out, "done") {
		t.Fatalf("done: %q %d %v", out, code, err)
	}
	detail, _ := service.Get(context.Background(), id)
	if len(detail.Verified) != 1 || len(detail.NotVerified) != 0 {
		t.Fatalf("evidence %+v %+v", detail.Verified, detail.NotVerified)
	}
	if out, code, _ := runCLI(t, "", "job", "list"); code != 0 || !strings.Contains(out, id+"\tdone") {
		t.Fatalf("list: %q", out)
	}
}
