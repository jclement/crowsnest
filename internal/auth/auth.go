// Package auth is crowsnest's single-user login: credential checks, the login rate limit and
// server-side sessions (DESIGN.md section 4, items 2 and 9). HTTP concerns (cookies, status
// codes) live in httpd; this package deals in tokens, digests and errors only.
//
// A session token is 32 random bytes handed to the browser once. The database stores only its
// SHA-256, plus a digest of the credentials it was issued under, so changing the password
// invalidates every session without a separate revocation step.
package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

const (
	// SessionTTL is how long a session lives without use; every use pushes it forward.
	SessionTTL = 30 * 24 * time.Hour
	// touchInterval limits sliding-expiry writes to one per minute per session: a busy page
	// polls and streams constantly, and the expiry does not need second precision.
	touchInterval = time.Minute
	// tokenBytes of crypto randomness per session token.
	tokenBytes = 32
	// credDigestIterations makes the stored credential digest slow to brute force if the
	// database leaks. It runs once per process start, so the cost is paid once.
	credDigestIterations = 200_000
	// saltKey is the kv row holding this install's random salt for the credential digest.
	saltKey = "auth.salt"
)

var (
	// ErrBadCredentials means the username or password was wrong. Which one is never said.
	ErrBadCredentials = errors.New("wrong username or password")
	// ErrNoSession means the token is missing, unknown, expired or from old credentials.
	ErrNoSession = errors.New("not signed in")
)

// RateLimitedError is returned by Login while a client (or everyone) is locked out.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("too many failed logins; try again in %s", e.RetryAfter.Round(time.Second))
}

// SessionStore is the slice of the store auth needs; *store.Store implements it.
type SessionStore interface {
	CreateSession(ctx context.Context, session store.AuthSession) error
	GetSession(ctx context.Context, idHash string) (store.AuthSession, error)
	TouchSession(ctx context.Context, idHash string, lastSeen, expires time.Time) error
	DeleteSession(ctx context.Context, idHash string) error
	DeleteStaleSessions(ctx context.Context, now time.Time, credDigest string) (int64, error)
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
	InsertKVIfAbsent(ctx context.Context, key, value string, extra map[string]string) (bool, error)
}

// Options configures a Service. With NoAuth, no login. With a Username and Password (from
// flags, the environment or the config file), those. With neither, the owner stored in the
// database (owner.go), which may not exist yet: that is setup mode.
type Options struct {
	Username string
	Password string
	NoAuth   bool             // every request is let through; Login and sessions are inert
	Now      func() time.Time // clock, injectable for tests; nil means time.Now
}

// Source says where the login comes from.
type Source string

const (
	SourceNone     Source = "none"     // --no-auth
	SourceConfig   Source = "config"   // --username and a password from flags, env or the config file
	SourceDatabase Source = "database" // the owner stored at setup
)

// Service checks credentials and manages sessions.
type Service struct {
	source         Source
	noAuth         bool
	usernameDigest [sha256.Size]byte
	passwordDigest [sha256.Size]byte
	username       string
	credDigest     string
	sessions       SessionStore
	limiter        *Limiter
	now            func() time.Time
	// admitted, when set, runs between admission and the credential check; tests use it to
	// line concurrent logins up at that point.
	admitted func()
}

// Session is an authenticated browser session.
type Session struct {
	Username  string
	ExpiresAt time.Time
	// Refreshed is true when this request pushed the expiry forward, so the cookie should be
	// re-sent with the new lifetime.
	Refreshed bool
}

// New builds a Service. With auth on, it derives the credential digest (salted with a random
// per-install value kept in the kv table) and removes sessions issued under other credentials.
func New(ctx context.Context, opts Options, sessions SessionStore) (*Service, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	service := &Service{noAuth: opts.NoAuth, sessions: sessions, now: opts.Now, limiter: NewLimiter(opts.Now)}
	switch {
	case opts.NoAuth:
		service.source = SourceNone
		return service, nil
	case opts.Username == "" && opts.Password == "":
		service.source = SourceDatabase
		if _, err := service.Cleanup(ctx); err != nil {
			return nil, err
		}
		return service, nil
	case opts.Username == "" || opts.Password == "":
		return nil, errors.New("auth: a username needs a password, and a password a username")
	}
	service.source = SourceConfig
	service.username = opts.Username
	service.usernameDigest = sha256.Sum256([]byte(opts.Username))
	service.passwordDigest = sha256.Sum256([]byte(opts.Password))
	salt, err := installSalt(ctx, sessions)
	if err != nil {
		return nil, err
	}
	if service.credDigest, err = credentialDigest(opts.Username, opts.Password, salt); err != nil {
		return nil, err
	}
	if _, err := service.Cleanup(ctx); err != nil {
		return nil, err
	}
	return service, nil
}

// Enabled reports whether a login is required.
func (s *Service) Enabled() bool { return !s.noAuth }

// Source says where the login comes from.
func (s *Service) Source() Source { return s.source }

// Username is the login name: the configured one, the stored owner's, or empty (--no-auth, or
// no owner yet).
func (s *Service) Username(ctx context.Context) string {
	if s.source != SourceDatabase {
		return s.username
	}
	owner, err := LoadOwner(ctx, s.sessions)
	if err != nil {
		return ""
	}
	return owner.Username
}

// SetupRequired reports whether the login is the stored owner and there is none yet.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	if s.source != SourceDatabase {
		return false, nil
	}
	_, err := LoadOwner(ctx, s.sessions)
	if errors.Is(err, ErrNoOwner) {
		return true, nil
	}
	return false, err
}

// Limiter exposes the rate limiter, for tests and diagnostics.
func (s *Service) Limiter() *Limiter { return s.limiter }

// ---- Login and logout --------------------------------------------------------

// Login checks credentials for a client and, on success, creates a session bound to host (the
// Host:port the browser used) and returns its token (the cookie value). It returns
// *RateLimitedError while locked out and ErrBadCredentials on a wrong username or password.
func (s *Service) Login(ctx context.Context, clientIP, username, password, userAgent, host string) (string, Session, error) {
	if s.noAuth {
		return "", Session{}, errors.New("auth: login is disabled with --no-auth")
	}
	attempt, retryAfter, ok := s.limiter.Admit(clientIP)
	if !ok {
		return "", Session{}, &RateLimitedError{RetryAfter: retryAfter}
	}
	if s.admitted != nil {
		s.admitted()
	}
	digest, name, ok, err := s.checkCredentials(ctx, username, password)
	if err != nil {
		return "", Session{}, err
	}
	if !ok {
		return "", Session{}, ErrBadCredentials // Admit already counted it
	}
	s.limiter.Succeeded(attempt)
	return s.createSession(ctx, digest, name, userAgent, host)
}

// checkCredentials compares a username and password with the login in effect and returns the
// credential digest and username a session would carry. For the stored owner the username is
// compared over SHA-256 digests in constant time and argon2 always runs, against a dummy hash
// when there is no owner, so timing says neither which half was wrong nor whether setup is done.
func (s *Service) checkCredentials(ctx context.Context, username, password string) (string, string, bool, error) {
	if s.source == SourceConfig {
		return s.credDigest, s.username, s.credentialsMatch(username, password), nil
	}
	owner, err := LoadOwner(ctx, s.sessions)
	if err != nil && !errors.Is(err, ErrNoOwner) {
		return "", "", false, err
	}
	hash, exists := owner.Hash, err == nil
	if !exists {
		hash = dummyHash()
	}
	given := sha256.Sum256([]byte(username))
	want := sha256.Sum256([]byte(owner.Username))
	usernameOK := subtle.ConstantTimeCompare(given[:], want[:])
	passwordOK := 0
	if VerifyPassword(hash, password) {
		passwordOK = 1
	}
	if !exists || usernameOK&passwordOK != 1 {
		return "", "", false, nil
	}
	return owner.digest(), owner.Username, true, nil
}

// currentDigest is the credential digest valid sessions must carry right now, and the username
// they belong to. The stored owner is read on every call, so `crowsnest reset-password` run
// beside a live server takes effect at once. ErrNoSession when there is no owner.
func (s *Service) currentDigest(ctx context.Context) (string, string, error) {
	if s.source == SourceConfig {
		return s.credDigest, s.username, nil
	}
	owner, err := LoadOwner(ctx, s.sessions)
	if errors.Is(err, ErrNoOwner) {
		return "", "", ErrNoSession
	}
	if err != nil {
		return "", "", err
	}
	return owner.digest(), owner.Username, nil
}

// NewSession signs a browser in under the login in effect without asking for credentials: the
// caller has already proved who it is (the setup token, or the current password).
func (s *Service) NewSession(ctx context.Context, userAgent, host string) (string, Session, error) {
	if s.noAuth {
		return "", Session{}, errors.New("auth: sessions are disabled with --no-auth")
	}
	digest, username, err := s.currentDigest(ctx)
	if err != nil {
		return "", Session{}, err
	}
	return s.createSession(ctx, digest, username, userAgent, host)
}

// CompleteSetup stores the first owner, and the extra kv pairs with it (the instance name), in
// one transaction. ErrSetupDone if an owner exists already, which is what makes a second setup,
// or the loser of two racing ones, impossible. Validation is the caller's.
func (s *Service) CompleteSetup(ctx context.Context, username, password string, extra map[string]string) error {
	if s.source != SourceDatabase {
		return ErrManagedOutside
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	raw, err := encodeOwner(Owner{Username: username, Hash: hash})
	if err != nil {
		return err
	}
	inserted, err := s.sessions.InsertKVIfAbsent(ctx, OwnerKey, raw, extra)
	if err != nil {
		return err
	}
	if !inserted {
		return ErrSetupDone
	}
	return nil
}

// ChangePassword replaces the stored owner's password after checking the current one, and
// signs every session out; the caller then signs its own browser in again with NewSession. A
// wrong current password counts against the login limiter like a failed sign-in, so a stolen
// session cannot be used to guess the password faster than the login page allows.
func (s *Service) ChangePassword(ctx context.Context, clientIP, current, next string) error {
	if s.source != SourceDatabase {
		return ErrManagedOutside
	}
	owner, err := LoadOwner(ctx, s.sessions)
	if err != nil {
		return err
	}
	if err := ValidatePassword(owner.Username, next); err != nil {
		return &ValidationError{err}
	}
	attempt, retryAfter, ok := s.limiter.Admit(clientIP)
	if !ok {
		return &RateLimitedError{RetryAfter: retryAfter}
	}
	if !VerifyPassword(owner.Hash, current) {
		return ErrBadCredentials
	}
	s.limiter.Succeeded(attempt)
	if owner.Hash, err = HashPassword(next); err != nil {
		return err
	}
	return storeOwner(ctx, s.sessions, owner, s.now())
}

// ValidationError is a rejected new password or username; its message is shown to the user.
type ValidationError struct{ Err error }

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// credentialsMatch compares SHA-256 digests in constant time. Both comparisons always run, so
// timing says nothing about which half was wrong; hashing first makes the comparison length
// independent of the inputs.
func (s *Service) credentialsMatch(username, password string) bool {
	usernameDigest := sha256.Sum256([]byte(username))
	passwordDigest := sha256.Sum256([]byte(password))
	usernameOK := subtle.ConstantTimeCompare(usernameDigest[:], s.usernameDigest[:])
	passwordOK := subtle.ConstantTimeCompare(passwordDigest[:], s.passwordDigest[:])
	return usernameOK&passwordOK == 1
}

func (s *Service) createSession(ctx context.Context, digest, username, userAgent, host string) (string, Session, error) {
	token, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	now := s.now()
	row := store.AuthSession{
		IDHash:     hashToken(token),
		CredDigest: digest,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionTTL),
		UserAgent:  truncate(userAgent, 256),
		Host:       host,
	}
	if err := s.sessions.CreateSession(ctx, row); err != nil {
		return "", Session{}, err
	}
	return token, Session{Username: username, ExpiresAt: row.ExpiresAt, Refreshed: true}, nil
}

// Logout deletes the session for token. An unknown token is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if s.noAuth || token == "" {
		return nil
	}
	return s.sessions.DeleteSession(ctx, hashToken(token))
}

// ---- Session checks ----------------------------------------------------------

// Authenticate resolves a token presented at host (the request's Host:port) to a session,
// enforcing expiry, the credential digest and the host the session was issued for, and slides
// the expiry forward at most once per touchInterval. Any failure is ErrNoSession (or a store
// error); a dead session is deleted on the way out.
//
// The host check is R1 M2: browsers send a host's cookies to every port on it, so another
// server on the same name receives the cookie. A session is accepted only at the Host:port it
// was issued for. That does not stop a replay that forges Host, but it does stop a cookie
// issued to (or tossed by) a sibling port from working here. A mismatch does not delete the
// session: whoever presents it at the wrong place should not be able to sign the real one out.
func (s *Service) Authenticate(ctx context.Context, token, host string) (Session, error) {
	if s.noAuth {
		return Session{}, nil
	}
	if token == "" {
		return Session{}, ErrNoSession
	}
	idHash := hashToken(token)
	row, err := s.sessions.GetSession(ctx, idHash)
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	digest, username, err := s.currentDigest(ctx)
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	if subtle.ConstantTimeCompare([]byte(row.CredDigest), []byte(digest)) != 1 || !now.Before(row.ExpiresAt) {
		_ = s.sessions.DeleteSession(ctx, idHash)
		return Session{}, ErrNoSession
	}
	if row.Host != host {
		return Session{}, ErrNoSession
	}
	session := Session{Username: username, ExpiresAt: row.ExpiresAt}
	if now.Sub(row.LastSeenAt) >= touchInterval {
		session.ExpiresAt = now.Add(SessionTTL)
		session.Refreshed = true
		if err := s.sessions.TouchSession(ctx, idHash, now, session.ExpiresAt); err != nil {
			return Session{}, err
		}
	}
	return session, nil
}

// Cleanup removes expired sessions and those from other credentials. It returns how many.
func (s *Service) Cleanup(ctx context.Context) (int64, error) {
	if s.noAuth {
		return 0, nil
	}
	digest, _, err := s.currentDigest(ctx)
	if errors.Is(err, ErrNoSession) {
		digest = "" // no owner yet: no session can be valid, and none carries an empty digest
	} else if err != nil {
		return 0, err
	}
	return s.sessions.DeleteStaleSessions(ctx, s.now(), digest)
}

// ---- Helpers -----------------------------------------------------------------

func newToken() (string, error) {
	buffer := make([]byte, tokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// hashToken is what the database stores in place of the token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// installSalt returns this install's salt, creating it on first use.
func installSalt(ctx context.Context, sessions SessionStore) ([]byte, error) {
	encoded, err := sessions.GetKV(ctx, saltKey)
	if err == nil {
		return hex.DecodeString(encoded)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating auth salt: %w", err)
	}
	if err := sessions.SetKV(ctx, saltKey, hex.EncodeToString(salt)); err != nil {
		return nil, err
	}
	return salt, nil
}

// credentialDigest binds sessions to the current username and password. PBKDF2 rather than a
// bare hash because this value sits in the database, and a leaked database must not hand out a
// fast-to-crack password hash.
func credentialDigest(username, password string, salt []byte) (string, error) {
	fullSalt := append(append([]byte{}, salt...), []byte("\x00"+username)...)
	key, err := pbkdf2.Key(sha256.New, password, fullSalt, credDigestIterations, 32)
	if err != nil {
		return "", fmt.Errorf("deriving credential digest: %w", err)
	}
	return hex.EncodeToString(key), nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
