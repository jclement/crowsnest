// Tests for the demo's transcripts: seeding, and the scripted orchestrator answering a prompt.

package fake

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
)

func TestDemoTranscripts(t *testing.T) {
	demo := NewDemo()
	dir := t.TempDir()
	if err := demo.EnableTranscripts(dir, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agent, _ := demo.AgentGet(context.Background(), DemoOrchestrator)
	path := filepath.Join(dir, "projects", projectDirName(agent.Cwd), agent.AgentSession.Value+".jsonl")
	seeded, err := os.ReadFile(path)
	if err != nil || strings.Count(string(seeded), "\n") < 8 {
		t.Fatalf("seeded transcript %s: %v (%d bytes)", path, err, len(seeded))
	}

	if _, err := demo.AgentPrompt(context.Background(), DemoOrchestrator, "how are we doing?", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := demo.AgentGet(context.Background(), DemoOrchestrator); got.AgentStatus != herdr.StatusWorking {
		t.Fatalf("status right after the prompt = %s", got.AgentStatus)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := demo.AgentGet(context.Background(), DemoOrchestrator)
		if got.AgentStatus == herdr.StatusIdle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the demo orchestrator never finished its turn")
		}
		time.Sleep(10 * time.Millisecond)
	}
	after, _ := os.ReadFile(path)
	text := string(after)
	if !strings.Contains(text, "how are we doing?") || !strings.Contains(text, "tool_result") || len(after) <= len(seeded) {
		t.Fatalf("turn not recorded:\n%s", text[len(seeded):])
	}
}

func TestDemoSayFollowsTheTalkFlag(t *testing.T) {
	reply := cannedReplies[0]
	voiceOn := "[crowsnest voice: transcribed speech, may contain mis-hearings] talk=on\nwhat is the status"
	if got := demoSay(voiceOn, reply); got != "SAY: "+reply.say+"\n\n" {
		t.Fatalf("talk=on: %q", got)
	}
	for _, prompt := range []string{
		"[crowsnest voice: transcribed speech, may contain mis-hearings] talk=off\nhi",
		"plain prompt mentioning talk=on",
	} {
		if got := demoSay(prompt, reply); got != "" {
			t.Errorf("%q: %q", prompt, got)
		}
	}
	if got := promptBody(voiceOn); got != "what is the status" {
		t.Errorf("promptBody: %q", got)
	}
}
