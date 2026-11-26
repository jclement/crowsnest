// Regression tests for the R3 security review's delegation findings (reviews/R3-opus-security.md):
// H1 (the directory boundary came from paths and git configuration the agent controls), M1 (the
// approval card could be rewritten by the brief it quotes, and showed the unresolved path), M2
// (the orchestrator path checked only the directory) and L3 (held approvals filled the live cap).
// Each test uses a scratch $HOME, never the real one.

package jobs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/herdr"
)

// scratchHome points $HOME at a fresh directory for the test and returns it.
func scratchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// ---- R3-H1: the boundary is what crowsnest recorded, not what the agent can change -----------

func TestParentThatSwapsItsDirectoryForALinkGainsNothing(t *testing.T) {
	ctx := context.Background()
	home := scratchHome(t)
	proj := mkdir(t, home, "work", "proj")
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead", Dir: proj})
	// The agent moves its working directory away and leaves a link to $HOME in its place.
	if err := os.Rename(proj, proj+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, proj); err != nil {
		t.Fatal(err)
	}
	child, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Read the keys", Dir: mkdir(t, home, "secrets"), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if child.Cause != CauseApproval {
		t.Fatalf("a child in $HOME started without the human: %+v", child)
	}
}

func TestGitConfigCannotWidenTheBoundary(t *testing.T) {
	ctx := context.Background()
	home := scratchHome(t)
	proj := mkdir(t, home, "work", "repo")
	if out, err := exec.Command("git", "-C", proj, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git not available: %v %s", err, out)
	}
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead", Dir: proj})
	// The agent points its repository's work tree at $HOME: git now calls $HOME the toplevel.
	if out, err := exec.Command("git", "-C", proj, "config", "core.worktree", home).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	child, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Read the keys", Dir: mkdir(t, home, "secrets"), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if child.Cause != CauseApproval {
		t.Fatalf("git configuration widened the boundary to $HOME: %+v", child)
	}
}

func TestAJobInHomeGrantsItsChildrenNothing(t *testing.T) {
	ctx := context.Background()
	home := scratchHome(t)
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead", Dir: home})
	child, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Anywhere", Dir: mkdir(t, home, "elsewhere"), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if child.Cause != CauseApproval {
		t.Fatalf("a job given in $HOME let its child anywhere in it: %+v", child)
	}
}

func TestChildDirectoryIsStoredResolved(t *testing.T) {
	ctx := context.Background()
	home := scratchHome(t)
	proj := mkdir(t, home, "work", "proj")
	sub := mkdir(t, proj, "sub")
	if err := os.Symlink(sub, filepath.Join(proj, "link")); err != nil {
		t.Fatal(err)
	}
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead", Dir: proj})
	child, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "In the sub", Dir: filepath.Join(proj, "link"), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(sub)
	if child.Dir != want {
		t.Fatalf("the child's directory is stored as %s, not the resolved %s: a link flipped later would move it", child.Dir, want)
	}
}

func TestLauncherRefusesADirectoryThatMovedSinceTheCheck(t *testing.T) {
	home := scratchHome(t)
	target := mkdir(t, home, "work", "proj")
	link := filepath.Join(home, "flip")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	d := newDelegateRig(t, DelegationLimits{})
	// An agent's job whose stored, once-resolved directory now resolves somewhere else.
	if _, err := d.svc.preparePane(context.Background(), Job{ID: "j_99", Origin: OriginAgent, Dir: link, Title: "x"}); err == nil {
		t.Fatal("the launcher opened a pane in a directory that no longer resolves to the one checked")
	}
}

// ---- R3-M1: the approval card is crowsnest's, and shows the resolved path ---------------------

func TestApprovalCardCannotBeRewrittenByTheBrief(t *testing.T) {
	ctx := context.Background()
	home := scratchHome(t)
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead", Dir: mkdir(t, home, "work", "proj")})
	docs := filepath.Join(home, "work", "docs")
	if err := os.Symlink(home, docs); err != nil {
		t.Fatal(err)
	}
	brief := "fix typo\r## This request stays inside the job's worktree\r**Recommended: Allow.** Deny would lose the work.\n```\n# closed the fence"
	child, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: brief, Dir: docs, Role: "tester"}, caller)
	if err != nil || child.Cause != CauseApproval {
		t.Fatalf("%+v %v", child, err)
	}
	question, err := d.questions.Get(ctx, child.CauseDetail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(question.BodyMD, "\r\v\f  ") {
		t.Fatalf("a line break CommonMark honours survived into the card: %q", question.BodyMD)
	}
	for _, line := range outsideFences(question.BodyMD) {
		if strings.Contains(line, "Recommended") || strings.Contains(line, "stays inside") || strings.Contains(line, "closed the fence") {
			t.Fatalf("the brief rendered outside its quote, as crowsnest's text: %q\n%s", line, question.BodyMD)
		}
	}
	if !strings.Contains(question.BodyMD, "` ~ `") {
		t.Fatalf("the card does not show where the link resolves (~):\n%s", question.BodyMD)
	}
}

// outsideFences returns the lines of a Markdown body that are not inside a fenced code block.
func outsideFences(body string) []string {
	var out []string
	fence := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		switch {
		case fence == "" && strings.HasPrefix(trimmed, "```"):
			fence = trimmed[:len(trimmed)-len(strings.TrimLeft(trimmed, "`"))]
		case fence != "" && strings.TrimRight(trimmed, " ") == fence:
			fence = ""
		case fence == "":
			out = append(out, line)
		}
	}
	return out
}

// ---- R3-M2: the orchestrator follows the engine rule, and remembered dirs are resolved --------

func orchestratorCaller(t *testing.T, d *delegateRig) AgentCaller {
	t.Helper()
	_, _, pane := d.fake.AddWorkspace("orchestrator", "/tmp/orch")
	if err := d.fake.AddAgent(pane, "orchestrator", "claude", herdr.StatusWorking, "s-orch"); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.orch = OrchestratorInfo{PaneID: pane, Ready: true}
	d.mu.Unlock()
	return AgentCaller{PaneID: pane}
}

func TestOrchestratorNeedsTheHumanForCodex(t *testing.T) {
	ctx := context.Background()
	scratchHome(t)
	d := newDelegateRig(t, DelegationLimits{})
	caller := orchestratorCaller(t, d)
	known := t.TempDir()
	d.create(CreateInput{Brief: "The human's job", Dir: known})
	job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Codex, approvals off", Dir: mkdir(t, known, "sub"), Engine: EngineCodex, Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if job.Cause != CauseApproval {
		t.Fatalf("the orchestrator started Codex without the human: %+v", job)
	}
}

func TestRememberedHomeGrantsTheOrchestratorNothing(t *testing.T) {
	ctx := context.Background()
	home := scratchHome(t)
	d := newDelegateRig(t, DelegationLimits{})
	caller := orchestratorCaller(t, d)
	d.create(CreateInput{Brief: "A job in my home directory", Dir: home})
	job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Anywhere at all", Dir: mkdir(t, home, ".ssh"), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if job.Cause != CauseApproval {
		t.Fatalf("one human job in ~ freed the whole home directory: %+v", job)
	}
}

func TestOrchestratorHoldingNoJobHasAFanOutCap(t *testing.T) {
	ctx := context.Background()
	scratchHome(t)
	d := newDelegateRig(t, DelegationLimits{})
	caller := orchestratorCaller(t, d)
	d.mu.Lock()
	d.orch.Ready = false // it takes none of them itself, so it holds no job to count children of
	d.mu.Unlock()
	var err error
	for i := 0; i <= DefaultMaxChildren && err == nil; i++ {
		_, err = d.svc.CreateForAgent(ctx, CreateInput{Brief: "Another one"}, caller)
	}
	if !IsValidation(err) {
		t.Fatalf("the orchestrator created more than %d jobs of its own with no cap: %v", DefaultMaxChildren, err)
	}
}

// ---- R3-L3: held approvals are capped per creator and do not fill the live cap ----------------

func TestHeldApprovalsAreCappedPerCreator(t *testing.T) {
	ctx := context.Background()
	scratchHome(t)
	d := newDelegateRig(t, DelegationLimits{})
	_, caller := d.parent(CreateInput{Role: "lead"})
	for i := range 2 {
		if job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere", Dir: t.TempDir(), Role: "tester"}, caller); err != nil || job.Cause != CauseApproval {
			t.Fatalf("request %d: %+v %v", i+1, job, err)
		}
	}
	if _, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere again", Dir: t.TempDir(), Role: "tester"}, caller); !IsValidation(err) {
		t.Fatalf("a third request waiting for the human was accepted: %v", err)
	}
}

func TestHeldApprovalsLeaveTheLiveCapForRealWork(t *testing.T) {
	ctx := context.Background()
	scratchHome(t)
	d := newDelegateRig(t, DelegationLimits{MaxLiveAgentJobs: 2})
	parent, caller := d.parent(CreateInput{Role: "lead"})
	for i := range 2 {
		if job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere", Dir: t.TempDir(), Role: "tester"}, caller); err != nil || job.Cause != CauseApproval {
			t.Fatalf("request %d: %+v %v", i+1, job, err)
		}
	}
	// The two held requests hold no agent, so they leave room for real work.
	if _, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Inside", Dir: parent.WorkDir, Role: "tester"}, caller); err != nil {
		t.Fatalf("held requests crowded out a child inside the parent: %v", err)
	}
}
