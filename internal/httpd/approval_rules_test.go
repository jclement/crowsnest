package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/store"
)

// ruleFixture: project 1 ("Alpha"), job j_1 in it (live, rooted at its directory), j_2 too, and a
// signed-in browser.
type ruleFixture struct {
	h         *harness
	approvals *approvals.Service
	cookie    *http.Cookie
	root      string
}

func newRuleFixture(t *testing.T) *ruleFixture {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	home := t.TempDir()
	root := filepath.Join(home, "alpha")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	projectService := projects.New(projects.Options{Store: db, Home: home})
	name := "Alpha"
	if _, err := projectService.Create(context.Background(), projects.Input{Name: &name, Dir: &root}); err != nil {
		t.Fatal(err)
	}
	f := &ruleFixture{root: root}
	f.approvals = approvals.New(approvals.Options{Store: db, Scope: func(_ context.Context, jobID, _ string) approvals.Scope {
		if jobID == "" {
			return approvals.Scope{}
		}
		return approvals.Scope{ProjectID: 1, ProjectName: "Alpha", JobLive: true, Root: root}
	}})
	f.h = newHarness(t, harnessOptions{approvals: f.approvals, projects: projectService})
	f.cookie = f.h.login()
	return f
}

func (f *ruleFixture) ask(t *testing.T, jobID, command string) approvals.Approval {
	t.Helper()
	input, _ := json.Marshal(map[string]string{"command": command})
	approval, err := f.approvals.Request(context.Background(), approvals.RequestInput{JobID: jobID, PaneID: "w1:p1", ToolName: "Bash", ToolInput: input, Hooked: true})
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func (f *ruleFixture) send(method, target, body string) *httptest.ResponseRecorder {
	return f.h.do(withCookie(request(method, target, body), f.cookie))
}

func TestRuleRoutesNeedASessionAndTheHeader(t *testing.T) {
	f := newRuleFixture(t)
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/projects/1/approval-rules", ""),
		request(http.MethodPost, "/api/v1/projects/1/approval-rules", `{"kind":"bash","pattern":"go test *"}`),
		request(http.MethodPost, "/api/v1/projects/1/approval-rules/preview", `{"kind":"bash","pattern":"go test *"}`),
		request(http.MethodDelete, "/api/v1/approval-rules/r_1", ""),
		request(http.MethodGet, "/api/v1/jobs/j_1/approval-rules", ""),
		request(http.MethodPost, "/api/v1/approvals/batch-allow", `{"ids":["ap_1"]}`),
	} {
		if response := f.h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", r.Method, r.URL.Path, response.Code)
		}
	}
	noHeader := withCookie(request(http.MethodPost, "/api/v1/projects/1/approval-rules", `{"kind":"bash","pattern":"go test *"}`), f.cookie)
	noHeader.Header.Del("X-Crowsnest")
	foreign := withCookie(request(http.MethodPost, "/api/v1/projects/1/approval-rules", `{"kind":"bash","pattern":"go test *"}`), f.cookie)
	foreign.Header.Set("Origin", "https://evil.example")
	for _, r := range []*http.Request{noHeader, foreign} {
		if response := f.h.do(r); response.Code != http.StatusForbidden {
			t.Errorf("a cross-site rule add: %d", response.Code)
		}
	}
	if rules, _ := f.approvals.ListRules(context.Background(), approvals.RuleFilter{ProjectID: 1}); len(rules) != 0 {
		t.Fatalf("a refused request added a rule: %+v", rules)
	}
}

func TestProjectRulesAddListPreviewRemove(t *testing.T) {
	f := newRuleFixture(t)
	f.ask(t, "j_1", "go test ./a")
	f.ask(t, "j_1", "rm -rf build")

	for body, fragment := range map[string]string{
		`{"kind":"bash","pattern":"*"}`:                         "every command",
		`{"kind":"edit","pattern":"../x"}`:                      "cannot leave",
		`{"kind":"shell","pattern":"ls"}`:                       "bash, edit or tool",
		`{"kind":"bash","pattern":"ls","max_risk":"dangerous"}`: "never allows a dangerous",
		`{"kind":"bash","pattern":"ls","extra":1}`:              "Send the rule as JSON",
	} {
		response := f.send(http.MethodPost, "/api/v1/projects/1/approval-rules", body)
		if response.Code != http.StatusBadRequest || !strings.Contains(decodeError(t, response).Message, fragment) {
			t.Errorf("%s: %d %s, want 400 saying %q", body, response.Code, response.Body.String(), fragment)
		}
	}

	response := f.send(http.MethodPost, "/api/v1/projects/1/approval-rules/preview", `{"kind":"bash","pattern":"rm *"}`)
	var preview struct{ Data approvals.Preview }
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&preview) != nil || len(preview.Data.Dangerous) != 1 || len(preview.Data.Allowed) != 0 {
		t.Fatalf("preview of a dangerous match: %d %+v", response.Code, preview)
	}
	if rules, _ := f.approvals.ListRules(context.Background(), approvals.RuleFilter{ProjectID: 1}); len(rules) != 0 {
		t.Fatal("a preview saves nothing")
	}

	response = f.send(http.MethodPost, "/api/v1/projects/alpha/approval-rules", `{"kind":"bash","pattern":"go test *","note":"tests are safe"}`)
	var added struct{ Data RuleView }
	if response.Code != http.StatusCreated || json.NewDecoder(response.Body).Decode(&added) != nil {
		t.Fatalf("add: %d %s", response.Code, response.Body.String())
	}
	if added.Data.ID != "r_1" || added.Data.Sentence != "Allow Bash commands matching `go test *`" || added.Data.Recent == nil || len(added.Data.Recent.Allowed) != 1 {
		t.Fatalf("added %+v", added.Data)
	}

	response = f.send(http.MethodGet, "/api/v1/projects/1/approval-rules", "")
	var list struct{ Data []RuleView }
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&list) != nil || len(list.Data) != 1 || list.Data[0].Recent == nil {
		t.Fatalf("list: %d %+v", response.Code, list)
	}
	if next := f.ask(t, "j_1", "go test ./b"); next.Status != approvals.StatusAllowed {
		t.Fatalf("the rule allows the next matching call: %s", next.Status)
	}

	if response := f.send(http.MethodDelete, "/api/v1/approval-rules/r_1", ""); response.Code != http.StatusOK {
		t.Fatalf("remove: %d", response.Code)
	}
	if response := f.send(http.MethodDelete, "/api/v1/approval-rules/r_1", ""); response.Code != http.StatusNotFound {
		t.Fatalf("remove twice: %d", response.Code)
	}
	if next := f.ask(t, "j_1", "go test ./c"); next.Status != approvals.StatusPending {
		t.Fatalf("a removed rule allows nothing: %s", next.Status)
	}
	if response := f.send(http.MethodGet, "/api/v1/projects/nope/approval-rules", ""); response.Code != http.StatusNotFound {
		t.Fatalf("an unknown project: %d", response.Code)
	}
}

func TestDecideRememberAndJobRules(t *testing.T) {
	f := newRuleFixture(t)
	first := f.ask(t, "j_1", "go test ./a")
	second := f.ask(t, "j_1", "go test ./b")
	dangerous := f.ask(t, "j_1", "rm -rf build")
	lead := f.ask(t, "", "go test ./c")

	if response := f.send(http.MethodPost, "/api/v1/approvals/"+dangerous.ID+"/decide", `{"decision":"allow","confirm":true,"remember":{"pattern":"rm *"}}`); response.Code != http.StatusConflict {
		t.Fatalf("remember on a dangerous call: %d", response.Code)
	}
	if response := f.send(http.MethodPost, "/api/v1/approvals/"+lead.ID+"/decide", `{"decision":"allow","remember":{"pattern":"go test *"}}`); response.Code != http.StatusConflict {
		t.Fatalf("remember without a job: %d", response.Code)
	}
	if response := f.send(http.MethodPost, "/api/v1/approvals/"+first.ID+"/decide", `{"decision":"allow","remember":{"pattern":"*"}}`); response.Code != http.StatusBadRequest {
		t.Fatalf("remember with a bad pattern: %d", response.Code)
	}
	if response := f.send(http.MethodPost, "/api/v1/approvals/"+first.ID+"/decide", `{"decision":"allow","remember":{"pattern":"go test *"}}`); response.Code != http.StatusOK {
		t.Fatalf("remember: %d %s", response.Code, response.Body.String())
	}
	if got, _ := f.approvals.Get(context.Background(), second.ID); got.Status != approvals.StatusAllowed || got.DecidedVia != approvals.ViaPolicy {
		t.Fatalf("the job's other matching call: %+v", got)
	}
	response := f.send(http.MethodGet, "/api/v1/jobs/j_1/approval-rules", "")
	var rules struct{ Data []approvals.Rule }
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&rules) != nil || len(rules.Data) != 1 || rules.Data[0].Scope != "job" {
		t.Fatalf("job rules: %d %+v", response.Code, rules)
	}
}

func TestBatchAllowRoute(t *testing.T) {
	f := newRuleFixture(t)
	a := f.ask(t, "j_1", "ls")
	b := f.ask(t, "j_1", "pwd")
	other := f.ask(t, "j_2", "ls")
	response := f.send(http.MethodPost, "/api/v1/approvals/batch-allow", `{"ids":["`+a.ID+`","`+other.ID+`"]}`)
	if response.Code != http.StatusConflict || !strings.Contains(decodeError(t, response).Message, "same job") {
		t.Fatalf("mixed jobs: %d %s", response.Code, response.Body.String())
	}
	if got, _ := f.approvals.Get(context.Background(), a.ID); got.Status != approvals.StatusPending {
		t.Fatal("a refused batch allowed something")
	}
	response = f.send(http.MethodPost, "/api/v1/approvals/batch-allow", `{"ids":["`+a.ID+`","`+b.ID+`"]}`)
	var allowed struct{ Data []approvals.Approval }
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&allowed) != nil || len(allowed.Data) != 2 {
		t.Fatalf("batch: %d %s", response.Code, response.Body.String())
	}
}
