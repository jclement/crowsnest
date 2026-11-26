// The stored owner: the login chosen on the first-run setup page (or set by `crowsnest
// reset-password`), kept in the database rather than in flags or the environment.
//
// Two credential paths exist side by side (DESIGN.md 4.2):
//
//   - Credentials from flags, the environment or the config file arrive as plain text at every
//     start. They are compared in constant time over SHA-256 digests held in memory, and the
//     database keeps only a slow PBKDF2 digest of them, to bind sessions to the credentials.
//   - The stored owner is written once and must survive restarts, so the database holds a real
//     password hash: argon2id with a random per-user salt, in the PHC string format. Sessions are
//     bound to a digest of that string, which changes with every new salt, so a password change
//     or reset signs every session out through the same mechanism as a changed env password.

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/jclement/crowsnest/internal/store"
)

// OwnerKey is the kv row holding the stored owner as JSON.
const OwnerKey = "auth.owner"

// Password rules for a stored owner. A length floor and nothing else: composition rules make
// passwords worse, not better.
const (
	MinPasswordLength = 12   // runes
	maxPasswordBytes  = 1024 // argon2 does not care, but a request body should be small
	maxUsernameRunes  = 64
)

var (
	// ErrNoOwner means no owner has been stored: setup has not been completed.
	ErrNoOwner = errors.New("no owner is stored; complete setup first")
	// ErrSetupDone means an owner already exists, so setup cannot run again.
	ErrSetupDone = errors.New("setup is already complete")
	// ErrManagedOutside means the login comes from flags, the environment or the config file,
	// so it cannot be changed from the web app.
	ErrManagedOutside = errors.New("the login is set by --username/CROWSNEST_PASSWORD, not stored in crowsnest")
)

// Argon2Params are argon2id's cost parameters. Stored hashes carry their own, so changing the
// defaults affects only new hashes.
type Argon2Params struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
}

// DefaultArgon2 is RFC 9106's second recommended option (64 MiB, 3 passes), with one lane: a
// login is one user at a time, and hashes run one at a time (hashSlot), so memory stays bounded
// at 64 MiB however many logins arrive together.
var DefaultArgon2 = Argon2Params{Time: 3, Memory: 64 * 1024, Threads: 1}

// argon2Params is what new hashes use; tests lower it to keep the suite fast.
var argon2Params = DefaultArgon2

const (
	argon2SaltBytes = 16
	argon2KeyBytes  = 32
)

// hashSlot serialises argon2 work across the process, so a burst of logins (already capped by
// the limiter) cannot multiply its memory.
var hashSlot = make(chan struct{}, 1)

// Owner is the stored login.
type Owner struct {
	Username string `json:"username"`
	Hash     string `json:"hash"` // argon2id PHC string
}

// digest is the credential digest sessions issued under this owner carry. It is derived from
// the PHC string, whose salt is new for every password set, so any change revokes them.
func (o Owner) digest() string {
	sum := sha256.Sum256([]byte("crowsnest-owner-v1\x00" + o.Username + "\x00" + o.Hash))
	return hex.EncodeToString(sum[:])
}

// ValidateUsername checks a username for a stored owner, returning it trimmed.
func ValidateUsername(username string) (string, error) {
	username = strings.TrimSpace(username)
	switch {
	case username == "":
		return "", errors.New("choose a username")
	case utf8.RuneCountInString(username) > maxUsernameRunes:
		return "", fmt.Errorf("a username is at most %d characters", maxUsernameRunes)
	case strings.IndexFunc(username, unicode.IsControl) >= 0:
		return "", errors.New("a username cannot contain control characters")
	}
	return username, nil
}

// ValidatePassword checks a new password for a stored owner.
func ValidatePassword(username, password string) error {
	switch {
	case utf8.RuneCountInString(password) < MinPasswordLength:
		return fmt.Errorf("a password is at least %d characters; a few unrelated words make a good one", MinPasswordLength)
	case len(password) > maxPasswordBytes:
		return fmt.Errorf("a password is at most %d bytes", maxPasswordBytes)
	case strings.EqualFold(password, username):
		return errors.New("the password cannot be the username")
	}
	return nil
}

// HashPassword returns an argon2id PHC string for password with a fresh random salt.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating a password salt: %w", err)
	}
	params := argon2Params
	key := idKey(password, salt, params, argon2KeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, params.Memory, params.Time, params.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the PHC string, comparing in constant time.
// A malformed hash never matches.
func VerifyPassword(hash, password string) bool {
	params, salt, want, err := parsePHC(hash)
	if err != nil {
		return false
	}
	got := idKey(password, salt, params, uint32(len(want))) //nolint:gosec // len(want) is a decoded 32-byte key
	return subtle.ConstantTimeCompare(got, want) == 1
}

func idKey(password string, salt []byte, params Argon2Params, length uint32) []byte {
	hashSlot <- struct{}{}
	defer func() { <-hashSlot }()
	return argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, length)
}

// parsePHC reads "$argon2id$v=19$m=…,t=…,p=…$salt$key". It bounds the cost parameters, so a
// tampered database cannot make one login allocate gigabytes.
func parsePHC(hash string) (Argon2Params, []byte, []byte, error) {
	var params Argon2Params
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params, nil, nil, errors.New("not an argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return params, nil, nil, errors.New("unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.Memory, &params.Time, &params.Threads); err != nil {
		return params, nil, nil, errors.New("bad argon2 parameters")
	}
	if params.Time < 1 || params.Time > 16 || params.Memory < 8 || params.Memory > 1024*1024 || params.Threads < 1 || params.Threads > 16 {
		return params, nil, nil, errors.New("argon2 parameters out of range")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return params, nil, nil, errors.New("bad argon2 salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return params, nil, nil, errors.New("bad argon2 key")
	}
	return params, salt, key, nil
}

// dummyHash is verified against when no owner exists, so a login takes as long either way.
var dummyHash = sync.OnceValue(func() string {
	hash, err := HashPassword("crowsnest has no owner yet")
	if err != nil {
		return ""
	}
	return hash
})

// ---- Storage -----------------------------------------------------------------

// OwnerStore is what the stored-owner functions need; *store.Store implements it.
type OwnerStore interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
	InsertKVIfAbsent(ctx context.Context, key, value string, extra map[string]string) (bool, error)
	DeleteStaleSessions(ctx context.Context, now time.Time, credDigest string) (int64, error)
}

// LoadOwner returns the stored owner, or ErrNoOwner.
func LoadOwner(ctx context.Context, kv interface {
	GetKV(ctx context.Context, key string) (string, error)
}) (Owner, error) {
	raw, err := kv.GetKV(ctx, OwnerKey)
	if errors.Is(err, store.ErrNotFound) {
		return Owner{}, ErrNoOwner
	}
	if err != nil {
		return Owner{}, err
	}
	var owner Owner
	if err := json.Unmarshal([]byte(raw), &owner); err != nil || owner.Username == "" || owner.Hash == "" {
		return Owner{}, fmt.Errorf("the stored owner (kv %s) is unreadable; run `crowsnest reset-setup`", OwnerKey)
	}
	return owner, nil
}

func encodeOwner(owner Owner) (string, error) {
	raw, err := json.Marshal(owner)
	return string(raw), err
}

// ResetPassword gives the stored owner a new password and signs every session out. It is the
// `crowsnest reset-password` path: whoever can write the database is the authority. It returns
// the owner's username.
func ResetPassword(ctx context.Context, db OwnerStore, password string, now time.Time) (string, error) {
	owner, err := LoadOwner(ctx, db)
	if err != nil {
		return "", err
	}
	if err := ValidatePassword(owner.Username, password); err != nil {
		return "", err
	}
	if owner.Hash, err = HashPassword(password); err != nil {
		return "", err
	}
	if err := storeOwner(ctx, db, owner, now); err != nil {
		return "", err
	}
	return owner.Username, nil
}

// ResetSetup removes the stored owner and every session, so the next start is in setup mode.
// It reports whether there was an owner to remove. The instance name is kept.
func ResetSetup(ctx context.Context, db OwnerStore, now time.Time) (bool, error) {
	_, err := LoadOwner(ctx, db)
	existed := err == nil
	if err := db.DeleteKV(ctx, OwnerKey); err != nil {
		return false, err
	}
	// No session carries an empty digest, so this deletes them all.
	if _, err := db.DeleteStaleSessions(ctx, now, ""); err != nil {
		return false, err
	}
	return existed, nil
}

// storeOwner writes owner and deletes every session not issued under it (all of them, since its
// hash is new).
func storeOwner(ctx context.Context, db OwnerStore, owner Owner, now time.Time) error {
	raw, err := encodeOwner(owner)
	if err != nil {
		return err
	}
	if err := db.SetKV(ctx, OwnerKey, raw); err != nil {
		return err
	}
	_, err = db.DeleteStaleSessions(ctx, now, owner.digest())
	return err
}
