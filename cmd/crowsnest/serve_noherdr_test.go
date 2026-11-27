// serve on a machine without herdr (DESIGN.md 3.4): it comes up and answers, the orchestrator
// waits with the reason, and once herdr appears (the lazy client connects) the orchestrator
// starts by itself, without a restart.

package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
)

type orchestratorView struct {
	Data struct {
		State  string `json:"state"`
		Detail string `json:"detail"`
	} `json:"data"`
}

func TestServeStartsWithoutHerdrAndRecovers(t *testing.T) {
	isolate(t)
	port := freeTCPPort(t)
	cfg := loadServeConfig(t, "--bind", "127.0.0.1", "--port", fmt.Sprint(port), "--session", "crowsnest-test-noherdr", "--web-dev", "--no-auth", "--no-remote-control")
	lazy := herdr.NewLazy() // never connected by itself: the test plays herdr appearing
	machine := doctor.DemoEnv(doctor.DemoAllGreen)
	hooks := serveHooks{
		connect:   func(context.Context, config.Config, *slog.Logger) (herdr.Client, string, error) { return lazy, "", nil },
		doctorEnv: &machine,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &lockedWriter{w: &bytes.Buffer{}}
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, slog.New(slog.NewJSONHandler(logs, nil)), hooks) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitFor := func(what string, ok func(orchestratorView) bool) orchestratorView {
		t.Helper()
		// Alone this takes about 7 s; under a loaded machine (several agents running suites)
		// 20 s was not enough and the gate failed for timing, not behaviour.
		deadline := time.Now().Add(90 * time.Second)
		for {
			var view orchestratorView
			code := getJSON(base+"/api/v1/orchestrator", nil, &view)
			if code == 200 && ok(view) {
				return view
			}
			select {
			case err := <-done:
				// serve stopped (it could not bind its port, say): no poll can succeed now, and
				// its background goroutines may keep logging as if it were still up.
				t.Fatalf("%s: serve returned early: %v (last HTTP %d)\n%s", what, err, code, logs.String())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: last HTTP %d, %+v\n%s", what, code, view.Data, logs.String())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waiting := waitFor("waiting for herdr", func(view orchestratorView) bool { return view.Data.State == "waiting" })
	if !strings.Contains(waiting.Data.Detail, "herdr") {
		t.Fatalf("the waiting reason does not name herdr: %q", waiting.Data.Detail)
	}
	if code := get(t, base+"/api/v1/health", nil); code != 200 {
		t.Fatalf("health without herdr: %d", code)
	}

	lazy.Set(fake.New()) // herdr is installed and its session answers
	waitFor("the orchestrator starting once herdr appeared", func(view orchestratorView) bool { return view.Data.State == "idle" })

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
}
