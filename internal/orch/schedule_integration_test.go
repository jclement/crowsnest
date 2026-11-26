//go:build integration

// Scheduled work against a real Claude (DESIGN.md 7.8): an `every: poke` task file in the
// orchestrator's tasks directory rides along with a manual poke (with one synthetic reason, since
// a ride-along task never makes a poke by itself), the orchestrator reads the file the reason
// names and does what it says (writes a marker file), and the task is stamped as run. Opt-in like
// the other real-Claude tests (CROWSNEST_E2E_CLAUDE=1, `mise run test:claude`); haiku, a few cents.

package orch_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/schedule"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

// taskAdapter is cmd/crowsnest's scheduleTasks, repeated here (package main cannot be imported).
type taskAdapter struct{ service *schedule.Service }

func (a taskAdapter) DueTasks(ctx context.Context, now time.Time) []orch.ScheduledTask {
	var out []orch.ScheduledTask
	for _, task := range a.service.DueTasks(ctx, now) {
		out = append(out, orch.ScheduledTask{Name: task.Name, Text: task.Text, RideAlong: task.RideAlong})
	}
	return out
}

func (a taskAdapter) Delivered(ctx context.Context, at time.Time, names []string) {
	a.service.Delivered(ctx, at, names)
}

func TestIntegrationClaudeScheduledTask(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	session := startSession(t)
	dir, err := filepath.Abs("e2e-task-" + randomSuffix(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	marker := filepath.Join(dir, "scheduled-marker.txt")
	task := "---\nevery: poke\n---\n# Leave a marker\n\nCreate the file " + marker +
		" containing exactly the text crowsnest-task-ok (use the Write tool). Do nothing else, then reply noop.\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks", "marker.md"), []byte(task), 0o600); err != nil {
		t.Fatal(err)
	}
	claudeDir, err := transcript.ConfigDir(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	o := orch.New(orch.Options{
		Client: session.Client, KV: db, Session: session.Name, Dir: dir, StateDir: t.TempDir(),
		Model: "haiku", PermissionMode: "auto", Version: "integration", ClaudeConfigDir: claudeDir, Logger: logger,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if status, err := o.Ensure(ctx); err != nil || status.State == orch.StateNeedsTerminal {
		t.Fatalf("Ensure: %v (status %+v)", err, status)
	}
	log := journal.New(db, nil, logger, nil)
	tasks := schedule.New(schedule.Options{KV: db, Journal: log, Logger: logger, Sources: []schedule.Source{{Dir: filepath.Join(dir, "tasks"), Origin: schedule.OriginDir}}})
	poker := orch.NewPoker(orch.PokerOptions{Client: session.Client, KV: db, Journal: log, Logger: logger, Status: o.Status, Tasks: taskAdapter{tasks}})
	authService, err := auth.New(ctx, auth.Options{NoAuth: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpd.New(httpd.Deps{
		Config: config.Config{Session: "crowsnest-test", NoAuth: true, WebDev: true, HostNames: []string{"127.0.0.1", "localhost"}},
		Auth:   authService, Hub: hub.New(), Logger: logger, Version: "integration",
		Herdr: session.Client, Orchestrator: o, Journal: log, Poker: poker, Schedule: tasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	waitIdle(t, ts, o)

	// Alone, the ride-along task makes no poke.
	var result orch.PokeResult
	call(t, ts, http.MethodPost, "/api/v1/orchestrator/poke", nil, &result)
	if result.Outcome != orch.OutcomeSkipped {
		t.Fatalf("a ride-along task alone was sent: %+v", result)
	}
	poker.Add(ctx, orch.Reason{Key: "manual:e2e-task", Source: orch.SourceManual,
		Text: "a synthetic test reason: nothing to do for it; do the scheduled task below"})
	result = orch.PokeResult{} // decoded afresh, not over the skipped one
	sent := time.Now()
	if code := call(t, ts, http.MethodPost, "/api/v1/orchestrator/poke", nil, &result); code != http.StatusOK {
		t.Fatalf("POST poke = %d", code)
	}
	t.Logf("poke result: %+v", result)
	if result.Outcome != orch.OutcomeSent || len(result.Reasons) != 2 || !strings.Contains(result.Reasons[1], "marker.md") {
		t.Fatalf("the task did not ride along: %+v", result)
	}
	var view schedule.View
	call(t, ts, http.MethodGet, "/api/v1/schedule", nil, &view)
	if len(view.Tasks) != 1 || view.Tasks[0].LastRunAt == nil || view.Tasks[0].LastOutcome != "carried by a poke" {
		t.Fatalf("the task was not stamped on delivery: %+v", view)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if body, err := os.ReadFile(marker); err == nil && strings.Contains(string(body), "crowsnest-task-ok") {
			t.Logf("the orchestrator did the scheduled task %s after the poke", time.Since(sent).Round(time.Millisecond))
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("no marker file %s within 3 minutes of the poke", marker)
}
