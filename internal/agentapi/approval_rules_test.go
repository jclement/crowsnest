package agentapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/store"
)

// An agent must not be able to widen its own policy: none of the browser's rule routes, nor
// remember or batch allow, exist on the agent socket, even with a valid agent token.
func TestAgentSocketCannotReachRuleRoutes(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := approvals.New(approvals.Options{Store: db})
	f := startSocket(t, func(options *ServerOptions) { options.Approvals = service })
	token := f.token(t, "w1:p1", "")
	client := unixHTTP(f.socket)
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/projects/1/approval-rules", ""},
		{http.MethodPost, "/api/v1/projects/1/approval-rules", `{"kind":"bash","pattern":"*"}`},
		{http.MethodPost, "/api/v1/projects/1/approval-rules/preview", `{"kind":"bash","pattern":"ls *"}`},
		{http.MethodDelete, "/api/v1/approval-rules/r_1", ""},
		{http.MethodGet, "/api/v1/jobs/j_1/approval-rules", ""},
		{http.MethodPost, "/api/v1/approvals/batch-allow", `{"ids":["ap_1"]}`},
		{http.MethodPost, "/api/v1/approvals/ap_1/decide", `{"decision":"allow","remember":{"pattern":"ls *"}}`},
	} {
		request, err := http.NewRequestWithContext(context.Background(), route.method, "http://agent"+route.path, strings.NewReader(route.body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(agentid.HeaderToken, token)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s on the agent socket: %d, want no such route", route.method, route.path, response.StatusCode)
		}
	}
	if rules, _ := service.ListRules(context.Background(), approvals.RuleFilter{All: true}); len(rules) != 0 {
		t.Fatalf("the agent socket added a rule: %+v", rules)
	}
}
