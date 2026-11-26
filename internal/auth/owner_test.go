package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jclement/crowsnest/internal/store"
)

// The suite hashes many passwords; the production cost (64 MiB, 3 passes) would make it slow
// under -race. Stored hashes carry their parameters, so verification follows whatever hashed.
func init() { argon2Params = Argon2Params{Time: 1, Memory: 64, Threads: 1} }

const ownerPassword = "correct horse battery"

func newStoredService(t *testing.T, db *store.Store, c *clock) *Service {
	t.Helper()
	service, err := New(context.Background(), Options{Now: c.Now}, db)
	if err != nil {
		t.Fatal(err)
	}
	if service.Source() != SourceDatabase {
		t.Fatalf("source %q, want database", service.Source())
	}
	return service
}

func setUpOwner(t *testing.T, service *Service) {
	t.Helper()
	if err := service.CompleteSetup(context.Background(), "jeff", ownerPassword, map[string]string{store.InstanceNameKey: "quark"}); err != nil {
		t.Fatal(err)
	}
}

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("hunter2 hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("hash %q is not a PHC argon2id string", hash)
	}
	if !VerifyPassword(hash, "hunter2 hunter2") || VerifyPassword(hash, "hunter2 hunter3") {
		t.Fatal("verify does not tell the right password from a wrong one")
	}
	again, _ := HashPassword("hunter2 hunter2")
	if again == hash {
		t.Fatal("two hashes of one password share a salt")
	}
}

func TestVerifyRejectsMalformedAndCostlyHashes(t *testing.T) {
	for _, hash := range []string{
		"", "plain", "$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=16$m=64,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=4194304,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5a2V5", // 4 GiB: refused, not allocated
		"$argon2id$v=19$m=64,t=99,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$!!$a2V5a2V5a2V5a2V5a2V5a2V5",
	} {
		if VerifyPassword(hash, "anything") {
			t.Errorf("%q verified", hash)
		}
	}
}

func TestDefaultArgon2IsRFC9106(t *testing.T) {
	if DefaultArgon2.Memory != 64*1024 || DefaultArgon2.Time != 3 {
		t.Fatalf("default argon2 parameters %+v", DefaultArgon2)
	}
}

func TestValidation(t *testing.T) {
	if _, err := ValidateUsername("  "); err == nil {
		t.Error("blank username accepted")
	}
	if name, err := ValidateUsername("  jeff "); err != nil || name != "jeff" {
		t.Errorf("username %q, %v", name, err)
	}
	if _, err := ValidateUsername("je\x00ff"); err == nil {
		t.Error("control character accepted")
	}
	if err := ValidatePassword("jeff", "short"); err == nil {
		t.Error("short password accepted")
	}
	if err := ValidatePassword("jeffjeffjeffjeff", "JEFFJEFFJEFFJEFF"); err == nil {
		t.Error("password equal to the username accepted")
	}
	if err := ValidatePassword("jeff", "twelve chars"); err != nil {
		t.Errorf("twelve characters refused: %v", err)
	}
}

func TestStoredOwnerSetupAndLogin(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	db := newStore(t)
	service := newStoredService(t, db, c)

	if required, err := service.SetupRequired(ctx); err != nil || !required {
		t.Fatalf("setup required %v, %v before setup", required, err)
	}
	if _, _, err := service.Login(ctx, "10.0.0.1", "", "", "ua", testHost); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("login before setup: %v", err)
	}
	if _, _, err := service.NewSession(ctx, "ua", testHost); err == nil {
		t.Fatal("a session was issued before setup")
	}

	setUpOwner(t, service)
	if required, _ := service.SetupRequired(ctx); required {
		t.Fatal("setup still required after setup")
	}
	if err := service.CompleteSetup(ctx, "mallory", "another long password", nil); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("second setup: %v", err)
	}
	if name, _ := db.GetKV(ctx, store.InstanceNameKey); name != "quark" {
		t.Fatalf("instance name %q", name)
	}
	if service.Username(ctx) != "jeff" {
		t.Fatalf("username %q", service.Username(ctx))
	}

	for _, attempt := range []struct{ user, password string }{{"jeff", "wrong password!"}, {"mallory", ownerPassword}} {
		if _, _, err := service.Login(ctx, "10.0.0.2", attempt.user, attempt.password, "ua", testHost); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("login as %s/%s: %v", attempt.user, attempt.password, err)
		}
	}
	token, session, err := service.Login(ctx, "10.0.0.1", "jeff", ownerPassword, "ua", testHost)
	if err != nil || session.Username != "jeff" {
		t.Fatalf("login: %v %+v", err, session)
	}
	if got, err := service.Authenticate(ctx, token, testHost); err != nil || got.Username != "jeff" {
		t.Fatalf("authenticate: %v %+v", err, got)
	}

	// A second service over the same database (a restart) accepts the same session.
	restarted := newStoredService(t, db, c)
	if _, err := restarted.Authenticate(ctx, token, testHost); err != nil {
		t.Fatalf("session lost across a restart: %v", err)
	}
}

func TestStoredOwnerLoginIsRateLimited(t *testing.T) {
	ctx := context.Background()
	service := newStoredService(t, newStore(t), newClock())
	setUpOwner(t, service)
	for range 5 {
		_, _, _ = service.Login(ctx, "10.0.0.9", "jeff", "wrong password!", "ua", testHost)
	}
	var limited *RateLimitedError
	if _, _, err := service.Login(ctx, "10.0.0.9", "jeff", ownerPassword, "ua", testHost); !errors.As(err, &limited) {
		t.Fatalf("sixth login in a minute: %v, want rate limited", err)
	}
}

func TestChangePasswordSignsOthersOut(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	service := newStoredService(t, newStore(t), c)
	setUpOwner(t, service)
	other, _, err := service.Login(ctx, "10.0.0.1", "jeff", ownerPassword, "phone", testHost)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.ChangePassword(ctx, "10.0.0.1", "wrong current!", "a new long password"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong current password: %v", err)
	}
	var invalid *ValidationError
	if err := service.ChangePassword(ctx, "10.0.0.1", ownerPassword, "short"); !errors.As(err, &invalid) {
		t.Fatalf("short new password: %v", err)
	}
	if err := service.ChangePassword(ctx, "10.0.0.1", ownerPassword, "a new long password"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, other, testHost); !errors.Is(err, ErrNoSession) {
		t.Fatalf("other session after a password change: %v", err)
	}
	mine, _, err := service.NewSession(ctx, "laptop", testHost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, mine, testHost); err != nil {
		t.Fatalf("the new session: %v", err)
	}
	if _, _, err := service.Login(ctx, "10.0.0.1", "jeff", ownerPassword, "ua", testHost); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("old password still works: %v", err)
	}
	if _, _, err := service.Login(ctx, "10.0.0.1", "jeff", "a new long password", "ua", testHost); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestChangePasswordCountsAgainstTheLimiter(t *testing.T) {
	ctx := context.Background()
	service := newStoredService(t, newStore(t), newClock())
	setUpOwner(t, service)
	for range 5 {
		_ = service.ChangePassword(ctx, "10.0.0.3", "guess guess guess", "a new long password")
	}
	var limited *RateLimitedError
	if err := service.ChangePassword(ctx, "10.0.0.3", ownerPassword, "a new long password"); !errors.As(err, &limited) {
		t.Fatalf("after five wrong current passwords: %v", err)
	}
}

func TestChangePasswordRefusedForConfigCredentials(t *testing.T) {
	service := newService(t, newStore(t), newClock(), "hunter2")
	if service.Source() != SourceConfig {
		t.Fatalf("source %q", service.Source())
	}
	if err := service.ChangePassword(context.Background(), "10.0.0.1", "hunter2", "a new long password"); !errors.Is(err, ErrManagedOutside) {
		t.Fatalf("change: %v", err)
	}
	if err := service.CompleteSetup(context.Background(), "x", "a new long password", nil); !errors.Is(err, ErrManagedOutside) {
		t.Fatalf("setup: %v", err)
	}
}

// reset-password and reset-setup write the database beside a running server; the server reads
// the owner on every request, so the old sessions die at once.
func TestResetPasswordAndResetSetupTakeEffectLive(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	db := newStore(t)
	service := newStoredService(t, db, c)
	setUpOwner(t, service)
	token, _, err := service.Login(ctx, "10.0.0.1", "jeff", ownerPassword, "ua", testHost)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ResetPassword(ctx, db, "short", c.Now()); err == nil {
		t.Fatal("reset accepted a short password")
	}
	user, err := ResetPassword(ctx, db, "reset long password", c.Now())
	if err != nil || user != "jeff" {
		t.Fatalf("reset: %q %v", user, err)
	}
	if _, err := service.Authenticate(ctx, token, testHost); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session after reset-password: %v", err)
	}
	token, _, err = service.Login(ctx, "10.0.0.1", "jeff", "reset long password", "ua", testHost)
	if err != nil {
		t.Fatal(err)
	}

	existed, err := ResetSetup(ctx, db, c.Now())
	if err != nil || !existed {
		t.Fatalf("reset-setup: %v %v", existed, err)
	}
	if _, err := service.Authenticate(ctx, token, testHost); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session after reset-setup: %v", err)
	}
	if required, _ := service.SetupRequired(ctx); !required {
		t.Fatal("setup not required after reset-setup")
	}
	if _, err := ResetPassword(ctx, db, "reset long password", c.Now()); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("reset-password with no owner: %v", err)
	}
	if name, _ := db.GetKV(ctx, store.InstanceNameKey); name != "quark" {
		t.Fatal("reset-setup removed the instance name")
	}
}

// Two setups racing: exactly one stores an owner.
func TestCompleteSetupRace(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir()+"/race.db") // a file: :memory: is one connection
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := newStoredService(t, db, newClock())
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			results <- service.CompleteSetup(ctx, "user"+string(rune('a'+i)), "a long enough password", nil)
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrSetupDone):
			t.Fatalf("unexpected error %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d setups succeeded, want 1", wins)
	}
}
