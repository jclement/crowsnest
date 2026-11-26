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
}

// Options configures a Service.
type Options struct {
	Username string
	Password string
	NoAuth   bool             // every request is let through; Login and sessions are inert
	Now      func() time.Time // clock, injectable for tests; nil means time.Now
}

// Service checks credentials and manages sessions.
type Service struct {
	noAuth         bool
	usernameDigest [sha256.Size]byte
	passwordDigest [sha256.Size]byte
	username       string
	credDigest     string
	sessions       SessionStore
	limiter        *Limiter
	now            func() time.Time
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
	if opts.NoAuth {
		return service, nil
	}
	if opts.Username == "" || opts.Password == "" {
		return nil, errors.New("auth: username and password are required unless NoAuth is set")
	}
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

// Username is the configured login name (empty with --no-auth).
func (s *Service) Username() string { return s.username }

// Limiter exposes the rate limiter, for tests and diagnostics.
func (s *Service) Limiter() *Limiter { return s.limiter }

// ---- Login and logout --------------------------------------------------------

// Login checks credentials for a client and, on success, creates a session and returns its
// token (the cookie value). It returns *RateLimitedError while locked out and ErrBadCredentials
// on a wrong username or password.
func (s *Service) Login(ctx context.Context, clientIP, username, password, userAgent string) (string, Session, error) {
	if s.noAuth {
		return "", Session{}, errors.New("auth: login is disabled with --no-auth")
	}
	if retryAfter, ok := s.limiter.Allow(clientIP); !ok {
		return "", Session{}, &RateLimitedError{RetryAfter: retryAfter}
	}
	if !s.credentialsMatch(username, password) {
		s.limiter.RecordFailure(clientIP)
		return "", Session{}, ErrBadCredentials
	}
	s.limiter.Reset(clientIP)
	return s.createSession(ctx, userAgent)
}

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

func (s *Service) createSession(ctx context.Context, userAgent string) (string, Session, error) {
	token, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	now := s.now()
	row := store.AuthSession{
		IDHash:     hashToken(token),
		CredDigest: s.credDigest,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionTTL),
		UserAgent:  truncate(userAgent, 256),
	}
	if err := s.sessions.CreateSession(ctx, row); err != nil {
		return "", Session{}, err
	}
	return token, Session{Username: s.username, ExpiresAt: row.ExpiresAt, Refreshed: true}, nil
}

// Logout deletes the session for token. An unknown token is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if s.noAuth || token == "" {
		return nil
	}
	return s.sessions.DeleteSession(ctx, hashToken(token))
}

// ---- Session checks ----------------------------------------------------------

// Authenticate resolves a token to a session, enforcing expiry and the credential digest, and
// slides the expiry forward at most once per touchInterval. Any failure is ErrNoSession (or a
// store error); a dead session is deleted on the way out.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
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
	now := s.now()
	if subtle.ConstantTimeCompare([]byte(row.CredDigest), []byte(s.credDigest)) != 1 || !now.Before(row.ExpiresAt) {
		_ = s.sessions.DeleteSession(ctx, idHash)
		return Session{}, ErrNoSession
	}
	session := Session{Username: s.username, ExpiresAt: row.ExpiresAt}
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
	return s.sessions.DeleteStaleSessions(ctx, s.now(), s.credDigest)
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
