// The standing-agent generalisation (DESIGN.md 21.2): a project's lead runs the very lifecycle
// the orchestrator does, under its own agent name, workspace label, kv keys and instructions.
// The orchestrator's own tests (orch_test.go) run unchanged; this table runs the same
// scenarios for both kinds, and checks that the two never take each other's pane, kv or name.

package orch

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/transcript"
)

type standingKind struct {
	name      string
	agent     string
	workspace string
	kvPrefix  string
	adjust    func(*Options)
}

func standingKinds() []standingKind {
	return []standingKind{
		{name: "orchestrator", agent: Name, workspace: WorkspaceLabel, kvPrefix: "orchestrator", adjust: func(*Options) {}},
		{name: "lead", agent: "lead-alpha", workspace: "lead alpha", kvPrefix: "lead.alpha", adjust: func(o *Options) {
			o.AgentName, o.Workspace, o.KVPrefix = "lead-alpha", "lead alpha", "lead.alpha"
			o.RemoteControlName = "quark alpha"
			o.Render = func() (string, error) { return "# You are the lead of project alpha\n", nil }
		}},
	}
}

func TestStandingAgentLifecycle(t *testing.T) {
	for _, kind := range standingKinds() {
		t.Run(kind.name+"/creates its workspace and starts", func(t *testing.T) {
			f := fake.New()
			rig := newRig(t, f, kind.adjust)
			status, err := rig.orch.Ensure(context.Background())
			if err != nil || status.State != StateIdle || status.Name != kind.agent {
				t.Fatalf("status %+v err %v", status, err)
			}
			starts := f.Starts()
			if len(starts) != 1 || starts[0].Name != kind.agent {
				t.Fatalf("starts %+v", starts)
			}
			workspaces, _ := f.WorkspaceList(context.Background())
			if len(workspaces) != 1 || workspaces[0].Label != kind.workspace {
				t.Fatalf("workspaces %+v", workspaces)
			}
			if stored, _ := rig.db.GetKV(context.Background(), kind.kvPrefix+".session_id"); stored != status.SessionID {
				t.Fatalf("session stored under %s: %q", kind.kvPrefix, stored)
			}
			body, _ := os.ReadFile(argValue(starts[0].Args, "--append-system-prompt-file"))
			if kind.name == "lead" && !strings.Contains(string(body), "lead of project alpha") {
				t.Fatalf("instructions are the lead's: %q", body)
			}
			if kind.name == "lead" && argValue(starts[0].Args, "--remote-control") != "quark alpha" {
				t.Fatalf("remote control: %v", starts[0].Args)
			}
		})
		t.Run(kind.name+"/adopts a live agent", func(t *testing.T) {
			f := fake.New()
			_, _, pane := f.AddWorkspace(kind.workspace, "/somewhere")
			_ = f.AddAgent(pane, kind.agent, "claude", herdr.StatusWorking, "existing")
			rig := newRig(t, f, kind.adjust)
			status, err := rig.orch.Ensure(context.Background())
			if err != nil || status.State != StateWorking || status.PaneID != pane || len(f.Starts()) != 0 {
				t.Fatalf("status %+v starts %d err %v", status, len(f.Starts()), err)
			}
		})
		t.Run(kind.name+"/resumes its stored conversation", func(t *testing.T) {
			f := fake.New()
			rig := newRig(t, f, kind.adjust)
			first, _ := rig.orch.Ensure(context.Background())
			writeTranscript(t, rig.claude, rig.opts.Dir, first.SessionID)
			_ = f.ExitAgent(first.PaneID)
			rig.orch.Watch(context.Background())
			starts := f.Starts()
			if len(starts) != 2 || argValue(starts[1].Args, "--resume") != first.SessionID {
				t.Fatalf("second start %+v", starts)
			}
		})
		t.Run(kind.name+"/restart and new conversation", func(t *testing.T) {
			f := fake.New()
			rig := newRig(t, f, kind.adjust)
			first, _ := rig.orch.Ensure(context.Background())
			writeTranscript(t, rig.claude, rig.opts.Dir, first.SessionID)
			if _, err := rig.orch.Restart(context.Background()); err != nil {
				t.Fatal(err)
			}
			fresh, err := rig.orch.NewConversation(context.Background())
			if err != nil || fresh.SessionID == first.SessionID {
				t.Fatalf("new conversation %+v err %v", fresh, err)
			}
		})
	}
}

// TestLeadAndOrchestratorCoexist: both in one session never share a pane, a name or a kv key.
func TestLeadAndOrchestratorCoexist(t *testing.T) {
	f := fake.New()
	orchestrator := newRig(t, f)
	lead := newRig(t, f, standingKinds()[1].adjust)
	lead.orch.opts.KV = orchestrator.db // one database, as in serve
	oStatus, err := orchestrator.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lStatus, err := lead.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if oStatus.PaneID == lStatus.PaneID || oStatus.SessionID == lStatus.SessionID {
		t.Fatalf("shared pane or session: %+v %+v", oStatus, lStatus)
	}
	o, _ := orchestrator.db.GetKV(context.Background(), "orchestrator.session_id")
	l, _ := orchestrator.db.GetKV(context.Background(), "lead.alpha.session_id")
	if o != oStatus.SessionID || l != lStatus.SessionID {
		t.Fatalf("kv o=%q l=%q", o, l)
	}
	// The lead exiting is the lead's business: the orchestrator's watch leaves it alone.
	_ = f.ExitAgent(lStatus.PaneID)
	orchestrator.orch.Watch(context.Background())
	if len(f.Starts()) != 2 {
		t.Fatalf("the orchestrator's watch restarted the lead: %+v", f.Starts())
	}
	lead.orch.Watch(context.Background())
	if starts := f.Starts(); len(starts) != 3 || starts[2].Name != "lead-alpha" {
		t.Fatalf("the lead's watch restarts it: %+v", starts)
	}
}

func writeTranscript(t *testing.T, configDir, cwd, session string) {
	t.Helper()
	path := transcript.Path(configDir, cwd, session)
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dirOf(path string) string { return path[:strings.LastIndex(path, "/")] }

// TestInstructionsForALead: a lead's CROWSNEST.md names its project and keeps the orchestrator's
// contracts word for word; the orchestrator's mentions the project tools, the lead's does not.
func TestInstructionsForALead(t *testing.T) {
	lead, err := RenderInstructions(InstructionData{Session: "s", Dir: "/w/alpha", Version: "v", Project: "alpha", ProjectName: "Alpha", Description: "the thing"})
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := RenderInstructions(InstructionData{Session: "s", Dir: "/w/o", Version: "v"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lead of project Alpha", "`/w/alpha`", "the thing", "SAY contract", "[crowsnest poke", "The job protocol"} {
		if !strings.Contains(lead, want) {
			t.Errorf("lead instructions lack %q", want)
		}
	}
	if strings.Contains(lead, "project_list") || !strings.Contains(orchestrator, "project_list") || strings.Contains(orchestrator, "lead of project") {
		t.Error("the project tools paragraph is on the wrong agent")
	}
}
