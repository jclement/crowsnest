// The lazy client: unavailable until set, then a plain pass-through; Ready closes on Set; Connect
// keeps looking while herdr is not on PATH and records why.

package herdr

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type pongClient struct{ Client }

func (pongClient) Ping(context.Context) (Pong, error) {
	return Pong{Version: "0.9.5", Protocol: ProtocolVersion}, nil
}

func TestLazyUnavailableUntilSet(t *testing.T) {
	lazy := NewLazy()
	if _, err := lazy.Ping(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("before Set: %v", err)
	}
	if _, err := lazy.Snapshot(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("before Set: %v", err)
	}
	ready := lazy.Ready()
	lazy.Set(pongClient{})
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("Ready did not close")
	}
	if pong, err := lazy.Ping(context.Background()); err != nil || pong.Version != "0.9.5" || !lazy.Connected() {
		t.Fatalf("after Set: %+v %v", pong, err)
	}
}

func TestLazyConnectWaitsForTheBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	lazy := NewLazy()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	lazy.Connect(ctx, SessionOptions{Name: "crowsnest-test-lazy", LogDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)}, nil)
	if lazy.Connected() || !strings.Contains(lazy.LastError(), "not installed") {
		t.Fatalf("connected %v, last error %q", lazy.Connected(), lazy.LastError())
	}
}
