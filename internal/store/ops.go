// Scoped API tokens and the other machines this instance shows (DESIGN.md 4.21, 11). Only a
// token's SHA-256 is kept; a machine's token (minted on that machine) is kept as given, because
// it has to be presented, and a backup blanks it (ScrubSnapshot).

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// APIToken is one scoped token, without its secret.
type APIToken struct {
	ID         int64
	Name       string
	Scope      string
	Hint       string
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

// InsertAPIToken stores a token's hash and returns its id.
func (s *Store) InsertAPIToken(ctx context.Context, token APIToken, hash string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens (name, scope, hash, hint, created_at) VALUES (?, ?, ?, ?, ?)`,
		token.Name, token.Scope, hash, token.Hint, token.CreatedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("saving an API token: %w", err)
	}
	return result.LastInsertId()
}

const apiTokenColumns = `id, name, scope, hint, created_at, last_used_at, revoked_at`

func scanAPIToken(row interface{ Scan(...any) error }) (APIToken, error) {
	var token APIToken
	var created, used, revoked int64
	if err := row.Scan(&token.ID, &token.Name, &token.Scope, &token.Hint, &created, &used, &revoked); err != nil {
		return APIToken{}, err
	}
	token.CreatedAt = time.Unix(created, 0)
	token.LastUsedAt = unixOrZero(sql.NullInt64{Int64: used, Valid: used > 0})
	token.RevokedAt = unixOrZero(sql.NullInt64{Int64: revoked, Valid: revoked > 0})
	return token, nil
}

// ListAPITokens lists every token, newest first.
func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing API tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []APIToken
	for rows.Next() {
		token, err := scanAPIToken(rows)
		if err != nil {
			return nil, fmt.Errorf("listing API tokens: %w", err)
		}
		out = append(out, token)
	}
	return out, rows.Err()
}

// APITokenByHash finds a live (not revoked) token by its hash and stamps its use.
func (s *Store) APITokenByHash(ctx context.Context, hash string, now time.Time) (APIToken, error) {
	token, err := scanAPIToken(s.db.QueryRowContext(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens WHERE hash = ? AND revoked_at = 0`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, fmt.Errorf("reading an API token: %w", err)
	}
	if now.Sub(token.LastUsedAt) > time.Minute {
		_, _ = s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now.Unix(), token.ID)
	}
	return token, nil
}

// RevokeAPIToken revokes a token; false when there was no live token with that id.
func (s *Store) RevokeAPIToken(ctx context.Context, id int64, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at = 0`, now.Unix(), id)
	if err != nil {
		return false, fmt.Errorf("revoking an API token: %w", err)
	}
	count, _ := result.RowsAffected()
	return count > 0, nil
}

// Machine is another crowsnest instance.
type Machine struct {
	ID        int64
	Name      string
	URL       string
	Token     string
	CreatedAt time.Time
}

// InsertMachine adds a machine.
func (s *Store) InsertMachine(ctx context.Context, machine Machine) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO machines (name, url, token, created_at) VALUES (?, ?, ?, ?)`,
		machine.Name, machine.URL, machine.Token, machine.CreatedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("adding a machine: %w", err)
	}
	return result.LastInsertId()
}

// ListMachines lists the machines by name.
func (s *Store) ListMachines(ctx context.Context) ([]Machine, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, url, token, created_at FROM machines ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("listing machines: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Machine
	for rows.Next() {
		var machine Machine
		var created int64
		if err := rows.Scan(&machine.ID, &machine.Name, &machine.URL, &machine.Token, &created); err != nil {
			return nil, fmt.Errorf("listing machines: %w", err)
		}
		machine.CreatedAt = time.Unix(created, 0)
		out = append(out, machine)
	}
	return out, rows.Err()
}

// DeleteMachine removes a machine; false when it did not exist.
func (s *Store) DeleteMachine(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM machines WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("removing a machine: %w", err)
	}
	count, _ := result.RowsAffected()
	return count > 0, nil
}
