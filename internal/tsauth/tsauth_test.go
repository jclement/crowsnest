// Tests for the Tailscale gate: serve config detection by host, the allowlist, normalising.

package tsauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

type memoryKV map[string]string

func (m memoryKV) GetKV(_ context.Context, key string) (string, error) {
	value, ok := m[key]
	if !ok {
		return "", store.ErrNotFound
	}
	return value, nil
}

func (m memoryKV) SetKV(_ context.Context, key, value string) error { m[key] = value; return nil }

const serveStatus = `{"Web":{"quark.tail1234.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:2769"}}}}}`

func gate(enabled bool, status string, calls *int) *Gate {
	return New(Options{Enabled: enabled, KV: memoryKV{}, Port: 2769, Run: func(context.Context, ...string) ([]byte, error) {
		*calls++
		if status == "" {
			return nil, errors.New("tailscale is not running")
		}
		return []byte(status), nil
	}})
}

func TestServesHost(t *testing.T) {
	ctx := context.Background()
	calls := 0
	g := gate(true, serveStatus, &calls)
	if !g.ServesHost(ctx, "quark.tail1234.ts.net") || !g.ServesHost(ctx, "QUARK.tail1234.ts.net:443") {
		t.Error("the served name was not recognised")
	}
	if g.ServesHost(ctx, "localhost:2769") || g.ServesHost(ctx, "evil.example") {
		t.Error("a name serve does not proxy was accepted")
	}
	if calls != 1 {
		t.Errorf("tailscale asked %d times within the cache window, want 1", calls)
	}
	if gate(true, "", &calls).ServeConfigured(ctx) {
		t.Error("a failing tailscale counted as a serve config")
	}
	if gate(false, serveStatus, &calls).ServesHost(ctx, "quark.tail1234.ts.net") {
		t.Error("the gate answered with --tailscale-auth off")
	}
	other := New(Options{Enabled: true, KV: memoryKV{}, Port: 9999, Run: func(context.Context, ...string) ([]byte, error) { return []byte(serveStatus), nil }})
	if other.ServeConfigured(ctx) {
		t.Error("a serve config for another port counted")
	}
}

func TestAllowList(t *testing.T) {
	ctx := context.Background()
	calls := 0
	g := gate(true, serveStatus, &calls)
	if g.Allowed(ctx, "jeff@github") {
		t.Error("allowed with an empty list")
	}
	list, err := g.SetList(ctx, []string{" Jeff@GitHub ", "jeff@github", "friend@example.com"})
	if err != nil || len(list) != 2 {
		t.Fatalf("list %v, %v", list, err)
	}
	if !g.Allowed(ctx, "JEFF@github") || g.Allowed(ctx, "stranger@github") {
		t.Error("allowlist answers wrong")
	}
	if _, err := g.SetList(ctx, []string{"two words"}); !errors.Is(err, ErrInvalidLogin) {
		t.Errorf("a login with a space: %v", err)
	}
	g.opts.Now = func() time.Time { return time.Now() }
	if (&Gate{}).Allowed(ctx, "jeff@github") {
		t.Error("a disabled gate allowed a login")
	}
}
