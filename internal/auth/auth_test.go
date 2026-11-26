package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// clock is a settable time source shared by the service and the limiter.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Unix(1_800_000_000, 0)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newService(t *testing.T, db *store.Store, c *clock, password string) *Service {
	t.Helper()
	service, err := New(context.Background(), Options{Username: "jeff", Password: password, Now: c.Now}, db)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLoginAndAuthenticate(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	service := newService(t, newStore(t), c, "hunter2")

	token, session, err := service.Login(ctx, "10.0.0.1", "jeff", "hunter2", "ua")
	if err != nil || token == "" || session.Username != "jeff" {
		t.Fatalf("login: %q %+v %v", token, session, err)
	}
	if len(token) < 40 {
		t.Errorf("token %q looks too short for 32 random bytes", token)
	}
	got, err := service.Authenticate(ctx, token)
	if err != nil || got.Username != "jeff" || got.Refreshed {
		t.Fatalf("authenticate: %+v %v", got, err)
	}
}

func TestWrongCredentials(t *testing.T) {
	ctx := context.Background()
	service := newService(t, newStore(t), newClock(), "hunter2")
	for _, attempt := range [][2]string{{"jeff", "wrong"}, {"jef", "hunter2"}, {"", ""}, {"jeff", "hunter2 "}} {
		if _, _, err := service.Login(ctx, "10.0.0."+attempt[0], attempt[0], attempt[1], ""); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("%q/%q: got %v", attempt[0], attempt[1], err)
		}
	}
}

func TestTokenIsStoredHashed(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)
	service := newService(t, db, newClock(), "pw")
	token, _, err := service.Login(ctx, "ip", "jeff", "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetSession(ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("the raw token must never be a database key")
	}
	if _, err := db.GetSession(ctx, hashToken(token)); err != nil {
		t.Fatalf("hashed token not found: %v", err)
	}
}

func TestUnknownAndEmptyTokens(t *testing.T) {
	service := newService(t, newStore(t), newClock(), "pw")
	for _, token := range []string{"", "not-a-real-token"} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrNoSession) {
			t.Errorf("token %q: %v", token, err)
		}
	}
}

func TestSessionExpiresAndSlides(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	db := newStore(t)
	service := newService(t, db, c, "pw")
	token, _, _ := service.Login(ctx, "ip", "jeff", "pw", "")

	c.Advance(30 * time.Second)
	if session, _ := service.Authenticate(ctx, token); session.Refreshed {
		t.Fatal("should not touch within the touch interval")
	}
	c.Advance(29 * 24 * time.Hour) // day 29: still valid, and this use slides it
	session, err := service.Authenticate(ctx, token)
	if err != nil || !session.Refreshed || !session.ExpiresAt.Equal(c.Now().Add(SessionTTL)) {
		t.Fatalf("slide: %+v %v", session, err)
	}
	c.Advance(SessionTTL - time.Second) // just inside the slid window
	if _, err := service.Authenticate(ctx, token); err != nil {
		t.Fatalf("slid session should still be valid: %v", err)
	}
	c.Advance(SessionTTL) // idle for the full TTL
	if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired session accepted: %v", err)
	}
	if _, err := db.GetSession(ctx, hashToken(token)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("an expired session should be deleted when seen")
	}
}

func TestLogout(t *testing.T) {
	ctx := context.Background()
	service := newService(t, newStore(t), newClock(), "pw")
	token, _, _ := service.Login(ctx, "ip", "jeff", "pw", "")
	if err := service.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session survived logout: %v", err)
	}
	if err := service.Logout(ctx, token); err != nil {
		t.Fatalf("second logout: %v", err)
	}
}

func TestPasswordChangeInvalidatesSessions(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	db := newStore(t)
	old := newService(t, db, c, "old-password")
	token, _, _ := old.Login(ctx, "ip", "jeff", "old-password", "")

	restarted := newService(t, db, c, "old-password") // same credentials: survives a restart
	if _, err := restarted.Authenticate(ctx, token); err != nil {
		t.Fatalf("session lost across a restart with the same password: %v", err)
	}
	changed := newService(t, db, c, "new-password")
	if _, err := changed.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old session accepted after a password change: %v", err)
	}
}

func TestStoredDigestIsNotAPlainHash(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)
	service := newService(t, db, newClock(), "pw")
	token, _, _ := service.Login(ctx, "ip", "jeff", "pw", "")
	row, _ := db.GetSession(ctx, hashToken(token))
	if row.CredDigest == hashToken("pw") || row.CredDigest == hashToken("jeff\x00pw") || len(row.CredDigest) != 64 {
		t.Fatalf("credential digest %q looks like a bare hash", row.CredDigest)
	}
}

func TestLoginLockout(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	service := newService(t, newStore(t), c, "pw")
	for range PerClientFailures {
		if _, _, err := service.Login(ctx, "1.1.1.1", "jeff", "nope", ""); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("expected bad credentials, got %v", err)
		}
	}
	_, _, err := service.Login(ctx, "1.1.1.1", "jeff", "pw", "") // right password, still locked
	var limited *RateLimitedError
	if !errors.As(err, &limited) || limited.RetryAfter != time.Minute {
		t.Fatalf("expected a one-minute lockout, got %v", err)
	}
	if _, _, err := service.Login(ctx, "2.2.2.2", "jeff", "pw", ""); err != nil {
		t.Fatalf("another client should not be locked out: %v", err)
	}
	c.Advance(FailureWindow + time.Second)
	if _, _, err := service.Login(ctx, "1.1.1.1", "jeff", "pw", ""); err != nil {
		t.Fatalf("lockout should lift after the window: %v", err)
	}
}

func TestNoAuthService(t *testing.T) {
	service, err := New(context.Background(), Options{NoAuth: true}, newStore(t))
	if err != nil {
		t.Fatal(err)
	}
	if service.Enabled() {
		t.Fatal("no-auth service reports enabled")
	}
	if _, err := service.Authenticate(context.Background(), ""); err != nil {
		t.Fatalf("no-auth authenticate: %v", err)
	}
	if _, _, err := service.Login(context.Background(), "ip", "a", "b", ""); err == nil {
		t.Fatal("login should be refused with no-auth")
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New(context.Background(), Options{Username: "jeff"}, newStore(t)); err == nil {
		t.Fatal("expected an error without a password")
	}
}
