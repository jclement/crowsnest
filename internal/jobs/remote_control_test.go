package jobs

import (
	"slices"
	"testing"
)

// --job-remote-control (DESIGN.md 7.10): job agents get --remote-control with a clean name; off
// by default; never for Codex.
func TestAgentArgsRemoteControl(t *testing.T) {
	job := Job{ID: "j_12", Title: "Fix the\nflaky \x1b[31mlogin test and also everything else in the suite", SessionID: "s"}
	files := jobFiles{prompt: "/p", settings: "/s"}
	on := &Service{opts: Options{Launch: LaunchOptions{RemoteControlPrefix: "quark"}}}
	args := on.agentArgs(job, files, false)
	i := slices.Index(args, "--remote-control")
	if i < 0 || args[i+1] != "quark j_12 Fix the flaky login test and also every…" {
		t.Fatalf("args %q", args)
	}
	off := &Service{}
	if slices.Contains(off.agentArgs(job, files, false), "--remote-control") {
		t.Fatal("remote control without --job-remote-control")
	}
	job.Engine = EngineCodex
	if slices.Contains(on.agentArgs(job, files, false), "--remote-control") {
		t.Fatal("codex has no remote control")
	}
}
