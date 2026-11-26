// Login sessions and the key-value table. Times are stored as unix seconds; the session id is
// only ever seen here as its hash (the auth package hashes before calling in).

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AuthSession is one row of auth_sessions.
type AuthSession struct {
	IDHash     string // hex SHA-256 of the cookie value
	CredDigest string // digest of the credentials the session was created under
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	UserAgent  string
}

// CreateSession inserts a session.
func (s *Store) CreateSession(ctx context.Context, session AuthSession) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_sessions (id_hash, cred_digest, created_at, last_seen_at, expires_at, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		session.IDHash, session.CredDigest, session.CreatedAt.Unix(), session.LastSeenAt.Unix(),
		session.ExpiresAt.Unix(), session.UserAgent)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	return nil
}

// GetSession returns the session with this id hash, or ErrNotFound. It does not check expiry;
// that is the auth package's decision.
func (s *Store) GetSession(ctx context.Context, idHash string) (AuthSession, error) {
	var session AuthSession
	var created, lastSeen, expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id_hash, cred_digest, created_at, last_seen_at, expires_at, user_agent
		 FROM auth_sessions WHERE id_hash = ?`, idHash).
		Scan(&session.IDHash, &session.CredDigest, &created, &lastSeen, &expires, &session.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	if err != nil {
		return AuthSession{}, fmt.Errorf("reading session: %w", err)
	}
	session.CreatedAt = time.Unix(created, 0)
	session.LastSeenAt = time.Unix(lastSeen, 0)
	session.ExpiresAt = time.Unix(expires, 0)
	return session, nil
}

// TouchSession records activity and pushes the expiry forward (the 30-day sliding window).
func (s *Store) TouchSession(ctx context.Context, idHash string, lastSeen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE auth_sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?`,
		lastSeen.Unix(), expires.Unix(), idHash)
	if err != nil {
		return fmt.Errorf("touching session: %w", err)
	}
	return nil
}

// DeleteSession removes one session (logout). Deleting a missing session is not an error.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id_hash = ?`, idHash); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// DeleteStaleSessions removes sessions that have expired by now or were created under other
// credentials, returning how many went. Run at start and periodically.
func (s *Store) DeleteStaleSessions(ctx context.Context, now time.Time, credDigest string) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE expires_at <= ? OR cred_digest <> ?`, now.Unix(), credDigest)
	if err != nil {
		return 0, fmt.Errorf("deleting stale sessions: %w", err)
	}
	return result.RowsAffected()
}

// ---- Key-value ---------------------------------------------------------------

// GetKV returns the value for key, or ErrNotFound.
func (s *Store) GetKV(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading kv %q: %w", key, err)
	}
	return value, nil
}

// SetKV inserts or replaces key.
func (s *Store) SetKV(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("writing kv %q: %w", key, err)
	}
	return nil
}

// DeleteKV removes key; a missing key is not an error.
func (s *Store) DeleteKV(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM kv WHERE key = ?`, key); err != nil {
		return fmt.Errorf("deleting kv %q: %w", key, err)
	}
	return nil
}
