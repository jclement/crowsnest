// Per-agent voices (DESIGN.md 13.5): a standing agent's SAY line is labelled with the agent and
// spoken in the voice chosen for it; an agent with none speaks in the instance's voice.

package voice_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/voice"
	"github.com/jclement/crowsnest/internal/voice/voicetest"
)

type kvMap struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *kvMap) GetKV(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	value, ok := k.m[key]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (k *kvMap) SetKV(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

func TestAgentVoices(t *testing.T) {
	ctx := context.Background()
	fake := voicetest.New(t, "")
	events := hub.New()
	sub := events.Subscribe(16)
	t.Cleanup(sub.Close)
	service := voice.NewService(voice.Options{Client: newClient(t, fake, voicetest.Key), Hub: events, Settings: &kvMap{m: map[string]string{}}})

	for _, bad := range []string{"job-3", "lead-", "Lead-x", "orchestrator2"} {
		if _, err := service.SetAgentVoice(ctx, bad, voicetest.VoiceBrian); !errors.Is(err, voice.ErrBadSettings) {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
	if _, err := service.SetAgentVoice(ctx, "lead-alpha", "not a voice!"); !errors.Is(err, voice.ErrBadSettings) {
		t.Fatalf("bad voice accepted: %v", err)
	}
	voices, err := service.SetAgentVoice(ctx, "lead-alpha", voicetest.VoiceBrian)
	if err != nil || voices["lead-alpha"] != voicetest.VoiceBrian {
		t.Fatalf("voices %v err %v", voices, err)
	}

	leadID := service.SayAs("Alpha is green.", voice.SourceReply, "w2:p1", "lead-alpha")
	if event := nextSay(t, sub); event.Agent != "lead-alpha" || event.TextID != leadID {
		t.Fatalf("event %+v", event)
	}
	orchID := service.SayAs("Alpha is green.", voice.SourceReply, "w1:p1", "orchestrator")
	if orchID == leadID {
		t.Fatal("the same words from two agents share one text (and so one voice)")
	}
	nextSay(t, sub)
	for id, wantVoice := range map[string]string{leadID: voicetest.VoiceBrian, orchID: service.DefaultVoice()} {
		audio, err := service.Speech(ctx, "s", id)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, audio)
		_ = audio.Close()
		requests := fake.Requests()
		if last := requests[len(requests)-1]; !strings.Contains(last.Path, "/"+wantVoice+"/") {
			t.Fatalf("spoken with %s, want %s", last.Path, wantVoice)
		}
	}
	if voices, _ := service.SetAgentVoice(ctx, "lead-alpha", ""); len(voices) != 0 {
		t.Fatalf("clearing kept %v", voices)
	}
}
