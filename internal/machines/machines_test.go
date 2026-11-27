// Tests for scoped tokens and the machine list.

package machines

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/store"
)

func newService(t *testing.T) *Service {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "crowsnest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Service{Store: db}
}

func TestTokensMintCheckRevoke(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	if _, _, err := svc.Mint(ctx, " "); !errors.Is(err, ErrValidation) {
		t.Errorf("an empty name: %v", err)
	}
	secret, token, err := svc.Mint(ctx, "bedev")
	if err != nil || !strings.HasPrefix(secret, TokenPrefix) || token.Hint != secret[len(secret)-4:] || token.Scope != ScopeRead {
		t.Fatalf("mint: %q %+v %v", secret, token, err)
	}
	if !svc.Check(ctx, secret, ScopeRead) {
		t.Error("a fresh token was refused")
	}
	if svc.Check(ctx, secret, "write") || svc.Check(ctx, secret+"x", ScopeRead) || svc.Check(ctx, "Bearer "+secret, ScopeRead) {
		t.Error("a wrong scope or a changed token was accepted")
	}
	if ok, err := svc.Revoke(ctx, token.ID); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if svc.Check(ctx, secret, ScopeRead) {
		t.Error("a revoked token was accepted")
	}
	list, _ := svc.Tokens(ctx)
	if len(list) != 1 || list[0].RevokedAt == nil {
		t.Errorf("tokens %+v", list)
	}
}

func TestNormalizeURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://bedev.tail1234.ts.net/": "https://bedev.tail1234.ts.net",
		"http://100.101.102.103:2769":    "http://100.101.102.103:2769",
		"http://relic.tail1234.ts.net":   "http://relic.tail1234.ts.net",
		"HTTPS://Example.com:8443":       "https://example.com:8443",
		"http://example.com":             "",
		"https://user:pw@example.com":    "",
		"https://example.com/api":        "",
		"ftp://100.64.0.1":               "",
		"http://192.168.1.10:2769":       "",
	} {
		got, err := NormalizeURL(raw)
		if want == "" && err == nil {
			t.Errorf("%s accepted as %s", raw, got)
		}
		if want != "" && got != want {
			t.Errorf("%s = %q (%v), want %s", raw, got, err, want)
		}
	}
}

func TestListFetchesSummariesWithTheToken(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/summary" || r.Header.Get("Authorization") != "Bearer cnk_good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"name":"bedev","attention":2}}`))
	}))
	defer remote.Close()
	if _, err := svc.Add(ctx, AddInput{Name: "bedev", URL: remote.URL, Token: "cnk_good"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add(ctx, AddInput{Name: "stale", URL: strings.Replace(remote.URL, "127.0.0.1", "localhost", 1), Token: "cnk_bad"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add(ctx, AddInput{Name: "dup", URL: remote.URL, Token: "cnk_good"}); !errors.Is(err, ErrValidation) {
		t.Errorf("a duplicate URL: %v", err)
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
	for _, machine := range list {
		switch machine.Name {
		case "bedev":
			if !strings.Contains(string(machine.Summary), `"attention":2`) || machine.Error != "" {
				t.Errorf("bedev %+v", machine)
			}
		case "stale":
			if machine.Summary != nil || !strings.Contains(machine.Error, "refused the token") {
				t.Errorf("stale %+v", machine)
			}
		}
	}
}
