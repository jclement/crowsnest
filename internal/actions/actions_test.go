package actions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

func newTokens(t *testing.T) (*Tokens, *time.Time) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Unix(1_800_000_000, 0)
	return &Tokens{Store: db, Now: func() time.Time { return now }}, &now
}

func TestSingleUse(t *testing.T) {
	tokens, _ := newTokens(t)
	ctx := context.Background()
	token, err := tokens.Mint(ctx, "approval:ap_1", []string{"allow", "deny"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := tokens.Redeem(ctx, token, "allow")
	if err != nil || item != "approval:ap_1" {
		t.Fatalf("first redeem = %q, %v", item, err)
	}
	if _, err := tokens.Redeem(ctx, token, "allow"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second redeem must fail, got %v", err)
	}
	if _, err := tokens.Redeem(ctx, token, "deny"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a used token is used for every action, got %v", err)
	}
}

func TestRacingRedeemsHaveOneWinner(t *testing.T) {
	tokens, _ := newTokens(t)
	ctx := context.Background()
	token, _ := tokens.Mint(ctx, "approval:ap_1", []string{"allow"})
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tokens.Redeem(ctx, token, "allow"); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d redeems won, want exactly 1", wins)
	}
}

func TestExpiry(t *testing.T) {
	tokens, now := newTokens(t)
	ctx := context.Background()
	token, _ := tokens.Mint(ctx, "question:q_2", []string{"yes"})
	*now = now.Add(TTL + time.Second)
	if _, err := tokens.Redeem(ctx, token, "yes"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an expired token must fail, got %v", err)
	}
}

func TestScope(t *testing.T) {
	tokens, _ := newTokens(t)
	ctx := context.Background()
	token, _ := tokens.Mint(ctx, "question:q_2", []string{"yes", "no"})
	if _, err := tokens.Redeem(ctx, token, "maybe"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an action outside the token's scope must fail, got %v", err)
	}
	if _, err := tokens.Redeem(ctx, token, "yes"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a probed token is spent, got %v", err)
	}
	if _, err := tokens.Redeem(ctx, "cna_forged", "yes"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown token must fail, got %v", err)
	}
	if _, err := tokens.Redeem(ctx, "crowsnest_session_value", "yes"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a non-token must fail, got %v", err)
	}
}
