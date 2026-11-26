// Package setup is crowsnest's first run (DESIGN.md 4.14). When there is no login from flags,
// the environment or the config file, no owner in the database and no --no-auth, crowsnest
// starts locked: it serves only a setup page, gated by a one-time token printed to its log.
// Whoever presents the token chooses the owner's username and password and the instance name;
// the token is then burnt and crowsnest starts for real.
//
// The token is 32 random bytes, new for each process start, kept only as a SHA-256 in memory
// and compared in constant time. Attempts are rate limited globally, and after a run of wrong
// tokens the token is replaced (and the new one logged), so an old log line stops working and a
// guesser starts over. Holding one mutex across the check and the commit, plus the store's
// insert-if-absent, makes a second setup impossible, including two submissions racing.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/ratelimit"
	"github.com/jclement/crowsnest/internal/store"
)

const (
	// tokenBytes of crypto randomness in a setup token.
	tokenBytes = 32
	// attemptBurst and attemptRefill: five token attempts at once, then one every 12 s, for
	// everyone together (about five a minute). A 256-bit token cannot be guessed at any rate;
	// the limit keeps a misbehaving client from spinning argon2 or the log.
	attemptBurst  = 5
	attemptRefill = 12 * time.Second
	// rotateAfter wrong tokens in a row, the token is replaced and the new one logged.
	rotateAfter = 10
	// limiterKey is the one global bucket.
	limiterKey = "setup"
)

var (
	// ErrBadToken is a wrong or missing setup token.
	ErrBadToken = errors.New("that setup token is not right; copy it again from crowsnest's log")
	// ErrDone means setup has completed: the token is burnt and an owner exists.
	ErrDone = errors.New("setup is already complete; sign in instead")
)

// RateLimitedError is returned while token attempts are being refused.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("too many setup attempts; try again in %s", e.RetryAfter.Round(time.Second))
}

// ValidationError is a field the user must correct; nothing was counted or stored.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// Options configures a Service.
type Options struct {
	Auth *auth.Service
	// DefaultName prefills the instance name: the stored one, else the hostname's.
	DefaultName string
	// Announce is called with each new token: at start, and after a rotation (rotated true).
	// It prints the setup URL; nothing else ever sees the token.
	Announce func(token string, rotated bool)
	Now      func() time.Time
}

// Request is a setup submission.
type Request struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// Service holds the token and runs the one setup.
type Service struct {
	opts    Options
	limiter *ratelimit.Limiter

	mu        sync.Mutex
	tokenHash [sha256.Size]byte
	failures  int  // wrong tokens since the last rotation
	done      bool // the token is burnt
	finished  chan struct{}
}

// New mints the first token and announces it.
func New(opts Options) (*Service, error) {
	if opts.Auth == nil || opts.Announce == nil {
		return nil, errors.New("setup: Auth and Announce are required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	service := &Service{opts: opts, limiter: ratelimit.New(attemptBurst, attemptRefill, opts.Now), finished: make(chan struct{})}
	if err := service.rotate(false); err != nil {
		return nil, err
	}
	return service, nil
}

// Done is closed once setup has completed.
func (s *Service) Done() <-chan struct{} { return s.finished }

// Required reports whether setup is still waiting.
func (s *Service) Required() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.done
}

// DefaultName is the instance name the form starts with.
func (s *Service) DefaultName() string { return s.opts.DefaultName }

// Submit checks the token and, if it is right, stores the owner and the name, burns the token
// and signs the caller in: it returns the new session's token. Fields are validated first, so a
// typo in the password costs no attempt.
func (s *Service) Submit(ctx context.Context, request Request, userAgent, host string) (string, auth.Session, error) {
	username, name, err := s.validate(request)
	if err != nil {
		return "", auth.Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return "", auth.Session{}, ErrDone
	}
	if ok, wait := s.limiter.Allow(limiterKey); !ok {
		return "", auth.Session{}, &RateLimitedError{RetryAfter: wait}
	}
	given := sha256.Sum256([]byte(request.Token))
	if subtle.ConstantTimeCompare(given[:], s.tokenHash[:]) != 1 {
		s.failures++
		if s.failures >= rotateAfter {
			if err := s.rotate(true); err != nil {
				return "", auth.Session{}, err
			}
		}
		return "", auth.Session{}, ErrBadToken
	}
	err = s.opts.Auth.CompleteSetup(ctx, username, request.Password, map[string]string{store.InstanceNameKey: name})
	if errors.Is(err, auth.ErrSetupDone) {
		s.finish() // someone else (another process on the same database) got there first
		return "", auth.Session{}, ErrDone
	}
	if err != nil {
		return "", auth.Session{}, err
	}
	s.finish()
	return s.opts.Auth.NewSession(ctx, userAgent, host)
}

func (s *Service) validate(request Request) (string, string, error) {
	username, err := auth.ValidateUsername(request.Username)
	if err != nil {
		return "", "", &ValidationError{err.Error()}
	}
	if err := auth.ValidatePassword(username, request.Password); err != nil {
		return "", "", &ValidationError{err.Error()}
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = s.opts.DefaultName
	}
	if !config.ValidName(name) {
		return "", "", &ValidationError{"an instance name is 1 to 32 letters, digits, '-' or '_', starting with a letter or digit"}
	}
	return username, name, nil
}

// finish burns the token and releases Done. Called with mu held.
func (s *Service) finish() {
	if s.done {
		return
	}
	s.done = true
	s.tokenHash = [sha256.Size]byte{}
	close(s.finished)
}

// rotate mints a new token, forgets the old one and announces the new. Called with mu held (or
// before the service is shared).
func (s *Service) rotate(rotated bool) error {
	buffer := make([]byte, tokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Errorf("generating a setup token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buffer)
	s.tokenHash = sha256.Sum256([]byte(token))
	s.failures = 0
	s.opts.Announce(token, rotated)
	return nil
}
