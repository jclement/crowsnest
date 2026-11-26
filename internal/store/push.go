// Web Push subscriptions (DESIGN.md 8.5, migration 0009). internal/push owns validation, the
// cap and sending; the store only keeps rows. The VAPID key pair and the notification settings
// live in kv (push.vapid, push.settings), written by internal/push.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PushSubscription is one row of push_subscriptions.
type PushSubscription struct {
	ID            int64
	Endpoint      string
	P256dh        string
	Auth          string
	UserAgent     string
	CreatedAt     time.Time
	LastSuccessAt time.Time // zero: never delivered
	LastFailureAt time.Time
	Failures      int
}

const pushColumns = `id, endpoint, p256dh, auth, user_agent, created_at, last_success_at, last_failure_at, failures`

// UpsertPushSubscription stores sub, keyed by its endpoint: a browser that subscribes again
// with the same endpoint (new keys after a reinstall) replaces its row and keeps its id. It
// returns the row's id and whether the row is new.
func (s *Store) UpsertPushSubscription(ctx context.Context, sub PushSubscription) (int64, bool, error) {
	existing, err := s.pushSubscriptionByEndpoint(ctx, sub.Endpoint)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return 0, false, err
	}
	if err == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE push_subscriptions SET p256dh = ?, auth = ?, user_agent = ?, failures = 0 WHERE id = ?`,
			sub.P256dh, sub.Auth, sub.UserAgent, existing.ID)
		if err != nil {
			return 0, false, fmt.Errorf("updating push subscription %d: %w", existing.ID, err)
		}
		return existing.ID, false, nil
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_agent, created_at) VALUES (?, ?, ?, ?, ?)`,
		sub.Endpoint, sub.P256dh, sub.Auth, sub.UserAgent, sub.CreatedAt.Unix())
	if err != nil {
		return 0, false, fmt.Errorf("inserting push subscription: %w", err)
	}
	id, err := result.LastInsertId()
	return id, true, err
}

func (s *Store) pushSubscriptionByEndpoint(ctx context.Context, endpoint string) (PushSubscription, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+pushColumns+` FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return scanPushSubscription(row)
}

// ListPushSubscriptions returns every subscription, oldest first.
func (s *Store) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pushColumns+` FROM push_subscriptions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing push subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var subs []PushSubscription
	for rows.Next() {
		sub, err := scanPushSubscription(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// CountPushSubscriptions returns how many subscriptions exist (the cap's check).
func (s *Store) CountPushSubscriptions(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_subscriptions`).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting push subscriptions: %w", err)
	}
	return count, nil
}

// DeletePushSubscription removes the subscription with this id; it reports whether one existed.
func (s *Store) DeletePushSubscription(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("deleting push subscription %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// DeletePushSubscriptionByEndpoint removes the subscription for endpoint; it reports whether one
// existed.
func (s *Store) DeletePushSubscriptionByEndpoint(ctx context.Context, endpoint string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	if err != nil {
		return false, fmt.Errorf("deleting a push subscription by endpoint: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// MarkPushResult records a send's outcome: a success stamps last_success_at and clears the
// failure streak; a failure stamps last_failure_at and counts it.
func (s *Store) MarkPushResult(ctx context.Context, id int64, ok bool, at time.Time) error {
	query := `UPDATE push_subscriptions SET last_failure_at = ?, failures = failures + 1 WHERE id = ?`
	if ok {
		query = `UPDATE push_subscriptions SET last_success_at = ?, failures = 0 WHERE id = ?`
	}
	if _, err := s.db.ExecContext(ctx, query, at.Unix(), id); err != nil {
		return fmt.Errorf("recording a push result for subscription %d: %w", id, err)
	}
	return nil
}

func scanPushSubscription(row rowScanner) (PushSubscription, error) {
	var sub PushSubscription
	var created, success, failure int64
	err := row.Scan(&sub.ID, &sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.UserAgent, &created, &success, &failure, &sub.Failures)
	if errors.Is(err, sql.ErrNoRows) {
		return PushSubscription{}, ErrNotFound
	}
	if err != nil {
		return PushSubscription{}, fmt.Errorf("reading a push subscription: %w", err)
	}
	sub.CreatedAt = time.Unix(created, 0)
	sub.LastSuccessAt = secondsOrZero(success)
	sub.LastFailureAt = secondsOrZero(failure)
	return sub, nil
}

func secondsOrZero(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}
