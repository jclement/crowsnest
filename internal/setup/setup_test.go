package setup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/store"
)

const host = "localhost:2769"

func TestMain(m *testing.M) {
	auth.SetArgon2Params(auth.Argon2Params{Time: 1, Memory: 64, Threads: 1})
	m.Run()
}

type harness struct {
	service *Service
	auth    *auth.Service
	db      *store.Store
	now     time.Time

	mu      sync.Mutex
	tokens  []string
	rotated []bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir()+"/setup.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &harness{db: db, now: time.Unix(1_800_000_000, 0)}
	h.auth, err = auth.New(ctx, auth.Options{Now: h.clock}, db)
	if err != nil {
		t.Fatal(err)
	}
	h.service, err = New(Options{Auth: h.auth, DefaultName: "quark", Now: h.clock, Announce: func(token string, rotated bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.tokens = append(h.tokens, token)
		h.rotated = append(h.rotated, rotated)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *harness) token() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tokens[len(h.tokens)-1]
}

func good(token string) Request {
	return Request{Token: token, Username: "jeff", Password: "correct horse battery", Name: "devbox"}
}

func TestTokenShape(t *testing.T) {
	h := newHarness(t)
	if len(h.tokens) != 1 || h.rotated[0] || len(h.token()) != 43 {
		t.Fatalf("announced %v (rotated %v)", h.tokens, h.rotated)
	}
	other := newHarness(t)
	if other.token() == h.token() {
		t.Fatal("two services minted the same token")
	}
}

func TestHappyPath(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if !h.service.Required() {
		t.Fatal("not required before setup")
	}
	session, info, err := h.service.Submit(ctx, good(h.token()), "ua", host)
	if err != nil {
		t.Fatal(err)
	}
	if info.Username != "jeff" || session == "" {
		t.Fatalf("session %q %+v", session, info)
	}
	select {
	case <-h.service.Done():
	default:
		t.Fatal("Done not closed")
	}
	if h.service.Required() {
		t.Fatal("still required")
	}
	if _, err := h.auth.Authenticate(ctx, session, host); err != nil {
		t.Fatalf("the setup session does not authenticate: %v", err)
	}
	if name, _ := h.db.GetKV(ctx, store.InstanceNameKey); name != "devbox" {
		t.Fatalf("name %q", name)
	}
	owner, err := auth.LoadOwner(ctx, h.db)
	if err != nil || owner.Username != "jeff" || !strings.HasPrefix(owner.Hash, "$argon2id$") {
		t.Fatalf("owner %+v %v", owner, err)
	}
	// The burnt token is useless, as is any other.
	if _, _, err := h.service.Submit(ctx, good(h.token()), "ua", host); !errors.Is(err, ErrDone) {
		t.Fatalf("second submit: %v", err)
	}
}

func TestDefaultNameWhenBlank(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	request := good(h.token())
	request.Name = " "
	if _, _, err := h.service.Submit(ctx, request, "ua", host); err != nil {
		t.Fatal(err)
	}
	if name, _ := h.db.GetKV(ctx, store.InstanceNameKey); name != "quark" {
		t.Fatalf("name %q, want the default", name)
	}
}

func TestValidationCostsNoAttempt(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	bad := []Request{
		{Token: "x", Username: "", Password: "correct horse battery"},
		{Token: "x", Username: "jeff", Password: "short"},
		{Token: "x", Username: "jeff", Password: "correct horse battery", Name: "has spaces"},
	}
	for range 3 {
		for _, request := range bad {
			var invalid *ValidationError
			if _, _, err := h.service.Submit(ctx, request, "ua", host); !errors.As(err, &invalid) {
				t.Fatalf("%+v: %v", request, err)
			}
		}
	}
	if _, _, err := h.service.Submit(ctx, good(h.token()), "ua", host); err != nil {
		t.Fatalf("nine invalid forms used up the limit: %v", err)
	}
}

func TestWrongTokenRateLimitAndRotation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	first := h.token()
	wrong := good("not the token")
	for i := range attemptBurst {
		if _, _, err := h.service.Submit(ctx, wrong, "ua", host); !errors.Is(err, ErrBadToken) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	var limited *RateLimitedError
	if _, _, err := h.service.Submit(ctx, good(first), "ua", host); !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		t.Fatalf("past the burst, even the right token: %v", err)
	}
	// Keep guessing as the limit allows until the token rotates.
	for range rotateAfter - attemptBurst {
		h.advance(attemptRefill)
		if _, _, err := h.service.Submit(ctx, wrong, "ua", host); !errors.Is(err, ErrBadToken) {
			t.Fatalf("slow guess: %v", err)
		}
	}
	if len(h.tokens) != 2 || !h.rotated[1] || h.token() == first {
		t.Fatalf("no rotation after %d wrong tokens: %v", rotateAfter, h.rotated)
	}
	h.advance(attemptRefill)
	if _, _, err := h.service.Submit(ctx, good(first), "ua", host); !errors.Is(err, ErrBadToken) {
		t.Fatalf("the rotated-out token: %v", err)
	}
	h.advance(attemptRefill)
	if _, _, err := h.service.Submit(ctx, good(h.token()), "ua", host); err != nil {
		t.Fatalf("the new token: %v", err)
	}
}

// Two (or eight) submissions with the right token racing: one wins, the others get ErrDone.
func TestDoubleSubmitRace(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.token()
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		results = make(chan error, 4)
	)
	for i := range 4 {
		wg.Go(func() {
			<-start
			request := good(token)
			request.Username = "user" + string(rune('a'+i))
			_, _, err := h.service.Submit(ctx, request, "ua", host)
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrDone):
			t.Fatalf("loser got %v, want ErrDone", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d winners", wins)
	}
}

// Another process on the same database completed setup: this one notices and finishes too.
func TestOwnerStoredElsewhere(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if err := h.auth.CompleteSetup(ctx, "other", "another long password", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.service.Submit(ctx, good(h.token()), "ua", host); !errors.Is(err, ErrDone) {
		t.Fatalf("submit: %v", err)
	}
	select {
	case <-h.service.Done():
	default:
		t.Fatal("Done not closed")
	}
}
