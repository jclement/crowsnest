// Package actions mints and redeems the tokens that let a notification's buttons answer one item
// without the session cookie (DESIGN.md 8.7). A token is minted by the server for one push, rides
// in its payload, and is sent back by the service worker with the button pressed:
//
//   - item-scoped: it names one item (approval:ap_3, question:q_7) and the actions it may
//     perform there (allow, deny; a question's first two option keys), nothing else;
//   - single use: redeeming it marks it used in one conditional UPDATE, so a replay, a second
//     device or a double tap gets nothing;
//   - short-lived: it expires after TTL (minutes);
//   - stored only as its SHA-256.
//
// The session cookie never reaches the service worker's request, and the token grants nothing
// beyond that one answer.
package actions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// TTL is how long a minted token works.
const TTL = 15 * time.Minute

// prefix marks a crowsnest action token.
const prefix = "cna_"

// ErrInvalid is the one answer for an unknown, used, expired or out-of-scope token.
var ErrInvalid = errors.New("this action has expired or was already used; open crowsnest to answer")

// Tokens mints and redeems.
type Tokens struct {
	Store *store.Store
	Now   func() time.Time
}

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Mint returns a new token for item, good for the given actions until TTL.
func (t *Tokens) Mint(ctx context.Context, item string, actions []string) (string, error) {
	if item == "" || len(actions) == 0 {
		return "", errors.New("a token needs an item and at least one action")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := prefix + base64.RawURLEncoding.EncodeToString(raw)
	encoded, _ := json.Marshal(actions)
	now := t.now()
	if err := t.Store.InsertActionToken(ctx, store.ActionTokenRecord{
		Hash: hash(token), Item: item, Actions: string(encoded), CreatedAt: now, ExpiresAt: now.Add(TTL),
	}); err != nil {
		return "", err
	}
	_ = t.Store.DeleteActionTokensBefore(ctx, now.Add(-time.Hour))
	return token, nil
}

// Redeem spends a token on one action and returns the item it is for. The token is spent even
// when the action is not one it allows (a token that was probed is not trusted again).
func (t *Tokens) Redeem(ctx context.Context, token, action string) (string, error) {
	if !strings.HasPrefix(token, prefix) || len(token) > 100 {
		return "", ErrInvalid
	}
	record, err := t.Store.UseActionToken(ctx, hash(token), t.now())
	if err != nil {
		return "", ErrInvalid
	}
	var allowed []string
	if json.Unmarshal([]byte(record.Actions), &allowed) != nil || !slices.Contains(allowed, action) {
		return "", ErrInvalid
	}
	return record.Item, nil
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
