// Package machines is the small multi-machine version (DESIGN.md 4.21): one crowsnest per
// machine, each with its own herdr, and a "Machines" list in each that shows the others side by
// side. It holds two things:
//
//   - Scoped API tokens (cnk_…), minted in the app, kept only as a SHA-256, revocable. The one
//     scope in this round is `read`, which opens exactly GET /api/v1/summary and nothing else:
//     no terminal, no job, no question, no setting. A token is shown once, when it is minted.
//   - The list of other instances: a name, a URL and the read token minted there. Their
//     summaries are fetched server-side (so the browser needs no cross-origin access and never
//     sees a token), with a short timeout, no redirects and a size cap.
//
// There is no remote control of another instance: the list links to it, and the person signs
// in there.
package machines

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

const (
	// TokenPrefix marks a scoped API token, recognisable in a leak (internal/redact's prefixed
	// rule matches cn?_).
	TokenPrefix = "cnk_"
	// ScopeRead opens GET /api/v1/summary only.
	ScopeRead = "read"
	// fetchTimeout bounds one remote summary; maxSummaryBytes its size.
	fetchTimeout    = 5 * time.Second
	maxSummaryBytes = 64 << 10
	maxNameRunes    = 40
	maxMachines     = 20
)

// Token is a token as the app lists it.
type Token struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	Hint       string     `json:"hint"` // the last four characters
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Service mints and checks tokens and keeps the machine list.
type Service struct {
	Store  *store.Store
	Client *http.Client // nil: a client with the timeout and no redirects
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ErrValidation wraps a message meant for the person.
var ErrValidation = errors.New("invalid")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// ---- Tokens -----------------------------------------------------------------------

// Mint creates a read token named name and returns it (shown once) with its listing.
func (s *Service) Mint(ctx context.Context, name string) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxNameRunes {
		return "", Token{}, invalid("give the token a name of 1 to %d characters, such as the machine that will use it", maxNameRunes)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Token{}, err
	}
	secret := TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	record := store.APIToken{Name: name, Scope: ScopeRead, Hint: secret[len(secret)-4:], CreatedAt: s.now()}
	id, err := s.Store.InsertAPIToken(ctx, record, hashToken(secret))
	if err != nil {
		return "", Token{}, err
	}
	record.ID = id
	return secret, tokenView(record), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func tokenView(record store.APIToken) Token {
	view := Token{ID: record.ID, Name: record.Name, Scope: record.Scope, Hint: record.Hint, CreatedAt: record.CreatedAt.UTC()}
	if !record.LastUsedAt.IsZero() {
		at := record.LastUsedAt.UTC()
		view.LastUsedAt = &at
	}
	if !record.RevokedAt.IsZero() {
		at := record.RevokedAt.UTC()
		view.RevokedAt = &at
	}
	return view
}

// Tokens lists every token, revoked ones included.
func (s *Service) Tokens(ctx context.Context) ([]Token, error) {
	records, err := s.Store.ListAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Token, 0, len(records))
	for _, record := range records {
		out = append(out, tokenView(record))
	}
	return out, nil
}

// Revoke revokes a token; false when no live token had that id.
func (s *Service) Revoke(ctx context.Context, id int64) (bool, error) {
	return s.Store.RevokeAPIToken(ctx, id, s.now())
}

// Check reports whether bearer is a live token with scope. It never says why it is not.
func (s *Service) Check(ctx context.Context, bearer, scope string) bool {
	if !strings.HasPrefix(bearer, TokenPrefix) || len(bearer) > 100 {
		return false
	}
	record, err := s.Store.APITokenByHash(ctx, hashToken(bearer), s.now())
	return err == nil && record.Scope == scope
}

// ---- Machines -----------------------------------------------------------------------

// Machine is another instance as the app shows it: never its token.
type Machine struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
	// Summary is what it answered, or nil with Error saying what went wrong.
	Summary json.RawMessage `json:"summary,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// AddInput is a machine to add.
type AddInput struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

// Add validates and stores a machine.
func (s *Service) Add(ctx context.Context, input AddInput) (Machine, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > maxNameRunes {
		return Machine{}, invalid("give the machine a name of 1 to %d characters", maxNameRunes)
	}
	base, err := NormalizeURL(input.URL)
	if err != nil {
		return Machine{}, err
	}
	token := strings.TrimSpace(input.Token)
	if !strings.HasPrefix(token, TokenPrefix) || len(token) > 100 {
		return Machine{}, invalid("paste the read token minted on that machine (Settings, Machines, Mint a token); it starts with %s", TokenPrefix)
	}
	existing, err := s.Store.ListMachines(ctx)
	if err != nil {
		return Machine{}, err
	}
	if len(existing) >= maxMachines {
		return Machine{}, invalid("at most %d machines", maxMachines)
	}
	record := store.Machine{Name: name, URL: base, Token: token, CreatedAt: s.now()}
	if record.ID, err = s.Store.InsertMachine(ctx, record); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Machine{}, invalid("%s is already in the list", base)
		}
		return Machine{}, err
	}
	return Machine{ID: record.ID, Name: name, URL: base, CreatedAt: record.CreatedAt.UTC()}, nil
}

// Remove deletes a machine.
func (s *Service) Remove(ctx context.Context, id int64) (bool, error) {
	return s.Store.DeleteMachine(ctx, id)
}

// NormalizeURL checks a machine's address and returns it as scheme://host[:port] with no path.
// https to any host; plain http only to a tailnet address, a MagicDNS (*.ts.net) name or
// loopback, where WireGuard or the machine itself keeps the token private in transit.
func NormalizeURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalid("give the address of the other crowsnest, such as https://bedev.tail1234.ts.net")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !plainHTTPAllowed(parsed.Hostname()) {
			return "", invalid("plain http is allowed only to a tailnet address or name (100.x, *.ts.net) or this machine; use https")
		}
	default:
		return "", invalid("the address must start with https:// (or http:// on the tailnet)")
	}
	return parsed.Scheme + "://" + strings.ToLower(parsed.Host), nil
}

// tailnetV4 and tailnetV6 are Tailscale's address ranges.
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

func plainHTTPAllowed(host string) bool {
	host = strings.ToLower(host)
	if strings.HasSuffix(host, ".ts.net") || host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsLoopback() || tailnetV4.Contains(addr) || tailnetV6.Contains(addr)
}

// List returns the machines with their summaries, fetched in parallel.
func (s *Service) List(ctx context.Context) ([]Machine, error) {
	records, err := s.Store.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Machine, len(records))
	var wait sync.WaitGroup
	for i, record := range records {
		out[i] = Machine{ID: record.ID, Name: record.Name, URL: record.URL, CreatedAt: record.CreatedAt.UTC()}
		wait.Add(1)
		go func() {
			defer wait.Done()
			out[i].Summary, out[i].Error = s.fetch(ctx, record)
		}()
	}
	wait.Wait()
	return out, nil
}

func (s *Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{
		Timeout:       fetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: fetchTimeout}).DialContext, TLSHandshakeTimeout: fetchTimeout},
	}
}

// fetch asks one machine for its summary. The error is a sentence for the card, never the
// remote's own text.
func (s *Service) fetch(ctx context.Context, machine store.Machine) (json.RawMessage, string) {
	if machine.Token == "" {
		return nil, "no token (restored from a backup, which never carries one): remove it and add it again with a new token"
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, machine.URL+"/api/v1/summary", nil)
	if err != nil {
		return nil, "the address is not usable"
	}
	request.Header.Set("Authorization", "Bearer "+machine.Token)
	request.Header.Set("Accept", "application/json")
	response, err := s.client().Do(request)
	if err != nil {
		return nil, "could not reach it (is it running, and on the tailnet?)"
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return nil, "it refused the token (revoked, or minted elsewhere): mint a new one there"
	case response.StatusCode != http.StatusOK:
		return nil, fmt.Sprintf("it answered %d; is it a crowsnest that knows /api/v1/summary?", response.StatusCode)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxSummaryBytes)).Decode(&envelope); err != nil || len(envelope.Data) == 0 {
		return nil, "its answer was not a crowsnest summary"
	}
	return envelope.Data, ""
}
