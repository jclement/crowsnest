package httpd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/engines"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
)

func TestEnginesRoute(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	response := h.do(request(http.MethodGet, "/api/v1/engines", ""))
	table := decodeData[[]engines.Engine](t, response.Body.Bytes())
	if response.Code != http.StatusOK || len(table) != 2 || table[0].ID != "claude" || table[1].ID != "codex" {
		t.Fatalf("engines: %d %s", response.Code, response.Body)
	}
	if len(table[1].Without) == 0 || len(table[0].Without) != 0 {
		t.Errorf("without: claude %v codex %v", table[0].Without, table[1].Without)
	}
}

// The Rescue sheet's Claude-only commands are refused for a Codex agent, with the engines table's
// sentence, and nothing is typed.
func TestPromptRefusesClaudeOnlyRescueCommandsForCodex(t *testing.T) {
	f := fake.New()
	_, _, pane := f.AddWorkspace("crowsnest", "/src")
	if err := f.AddAgent(pane, "job-j_9", "codex", herdr.StatusIdle, ""); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, harnessOptions{noAuth: true, herdr: f, fleet: fleet.New(fleet.Options{Client: f, Session: "crowsnest-test"})})
	for _, text := range []string{"/compact", "/model gpt-x"} {
		response := h.do(request(http.MethodPost, "/api/v1/agents/job-j_9/prompt", `{"text":"`+text+`"}`))
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Codex") && !strings.Contains(response.Body.String(), "Claude") {
			t.Errorf("%s: %d %s", text, response.Code, response.Body)
		}
	}
	if len(f.Prompts()) != 0 {
		t.Errorf("something was typed: %+v", f.Prompts())
	}
	// Anything else still goes through.
	if response := h.do(request(http.MethodPost, "/api/v1/agents/job-j_9/prompt", `{"text":"carry on"}`)); response.Code != http.StatusOK {
		t.Errorf("a plain prompt: %d %s", response.Code, response.Body)
	}
}
