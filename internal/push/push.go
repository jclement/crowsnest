// Package push is Web Push (DESIGN.md 8.5): the second sink, beside --notify-url, for the notices
// that tell Jeff something needs him while no crowsnest tab is open.
//
// The pieces:
//
//   - push.go      the Service: the VAPID key pair (made on first start, kept in kv, the private
//     half never leaves the server), the subscriptions (one per browser, capped), the settings
//     (which kinds of notice are pushed).
//   - endpoint.go  what a subscription from the browser must look like, and the SSRF guard: the
//     server POSTs to a URL the browser chose, so it refuses non-https URLs and any address that is
//     not public, checked again at dial time so DNS cannot point a public name inside.
//   - send.go      encrypting and posting one notice to every subscription (webpush-go), TTL and
//     Urgency from the notice, pruning subscriptions the push service says are gone.
//   - notices.go   the notice itself and how each event becomes one: a question, a terminal
//     dialog, a job stuck, failed or done, the orchestrator needing the terminal. Redaction and
//     the rate limits are --notify-url's (internal/questions/notify.go).
//   - watcher.go   the loop that reads the hub's events and the question hook and pushes.
//
// Nothing here is a secret the browser may see: the API shows a device by id, user agent and the
// push service's host, never its endpoint URL (which is itself a capability to push to it).
package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
)

const (
	// kvVAPID holds the key pair as JSON {public_key, private_key}, both base64url.
	kvVAPID = "push.vapid"
	// kvSettings holds Settings as JSON.
	kvSettings = "push.settings"
	// MaxSubscriptions caps how many browsers may be subscribed. One person has a handful of
	// devices; the cap stops a buggy or hostile page from filling the table, and every notice
	// costs one POST per subscription.
	MaxSubscriptions = 20
	// maxUserAgent bounds the stored user agent (shown to tell devices apart).
	maxUserAgent = 200
	// subscriber is the VAPID "sub" claim: a contact URL for push services. A project URL rather
	// than anyone's address (Apple refuses a localhost or malformed subject).
	subscriber = "https://github.com/jclement/crowsnest"
)

// ErrTooMany is returned by Subscribe when MaxSubscriptions browsers are already subscribed.
var ErrTooMany = fmt.Errorf("already %d devices get notifications; remove one in Settings first", MaxSubscriptions)

// Store is what the service keeps in the database (store.Store).
type Store interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	UpsertPushSubscription(ctx context.Context, sub store.PushSubscription) (int64, bool, error)
	ListPushSubscriptions(ctx context.Context) ([]store.PushSubscription, error)
	CountPushSubscriptions(ctx context.Context) (int, error)
	DeletePushSubscription(ctx context.Context, id int64) (bool, error)
	DeletePushSubscriptionByEndpoint(ctx context.Context, endpoint string) (bool, error)
	MarkPushResult(ctx context.Context, id int64, ok bool, at time.Time) error
}

// Options configure a Service. Store is required.
type Options struct {
	Store  Store
	Logger *slog.Logger
	// HTTPClient posts to push services. Nil: the SSRF-guarded client (GuardedClient). Tests
	// pass an ordinary client to reach an httptest server on loopback.
	HTTPClient *http.Client
	// AllowLoopbackHTTP accepts http:// endpoints on loopback addresses, and lets the guarded
	// client dial loopback. Only --demo sets it, so the hermetic browser suite can run a fake
	// push service; a demo instance has no real shell to protect.
	AllowLoopbackHTTP bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Name is this instance's name (--name): notification titles start with it, so two
	// crowsnests on two machines can be told apart on one phone. Empty: titles as they are.
	Name string
}

// Service holds the keys, subscriptions and settings, and sends notices.
type Service struct {
	opts   Options
	keys   vapidKeys
	client *http.Client

	settingsMu sync.Mutex
}

// vapidKeys is the application server's key pair (RFC 8292), base64url without padding.
type vapidKeys struct {
	Public  string `json:"public_key"`
	Private string `json:"private_key"`
}

// New loads the VAPID key pair from kv, generating and storing it on first start.
func New(ctx context.Context, opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("push: Store is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	keys, err := loadOrCreateKeys(ctx, opts.Store, opts.Logger)
	if err != nil {
		return nil, err
	}
	client := opts.HTTPClient
	if client == nil {
		client = GuardedClient(opts.AllowLoopbackHTTP)
	}
	return &Service{opts: opts, keys: keys, client: client}, nil
}

// loadOrCreateKeys reads push.vapid, or makes a pair and stores it. The private key is never
// logged; the log says only that a pair was made.
func loadOrCreateKeys(ctx context.Context, db Store, logger *slog.Logger) (vapidKeys, error) {
	raw, err := db.GetKV(ctx, kvVAPID)
	if err == nil {
		var keys vapidKeys
		if json.Unmarshal([]byte(raw), &keys) == nil && keys.Public != "" && keys.Private != "" {
			return keys, nil
		}
		logger.Warn("the stored Web Push key pair is unreadable; making a new one (every device must turn notifications on again)")
	} else if !errors.Is(err, store.ErrNotFound) {
		return vapidKeys{}, fmt.Errorf("reading the Web Push key pair: %w", err)
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return vapidKeys{}, fmt.Errorf("making a Web Push key pair: %w", err)
	}
	keys := vapidKeys{Public: public, Private: private}
	encoded, err := json.Marshal(keys) //nolint:gosec // G117: the private key is stored in the 0600 database on purpose, never sent or logged
	if err != nil {
		return vapidKeys{}, fmt.Errorf("encoding the Web Push key pair: %w", err)
	}
	if err := db.SetKV(ctx, kvVAPID, string(encoded)); err != nil {
		return vapidKeys{}, fmt.Errorf("storing the Web Push key pair: %w", err)
	}
	logger.Info("made a Web Push key pair (stored in the database)")
	return keys, nil
}

// PublicKey is the VAPID public key the browser passes to pushManager.subscribe as
// applicationServerKey: an uncompressed P-256 point, base64url.
func (s *Service) PublicKey() string { return s.keys.Public }

// ---- Subscriptions -------------------------------------------------------------

// Device is a subscription as the browser sees it: never the endpoint URL, which is a capability.
type Device struct {
	ID            int64      `json:"id"`
	UserAgent     string     `json:"user_agent"`
	Service       string     `json:"service"` // the push service's host, e.g. fcm.googleapis.com
	CreatedAt     time.Time  `json:"created_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastFailureAt *time.Time `json:"last_failure_at"`
	Failures      int        `json:"failures"`
	// EndpointHash is the first 16 bytes of SHA-256 over the endpoint, hex: the browser hashes
	// its own endpoint the same way to find itself in the list, without the endpoint (a push
	// capability) ever coming back from the server.
	EndpointHash string `json:"endpoint_hash"`
}

// Subscribe stores a subscription from the browser after validating it (ValidateSubscription).
// A known endpoint is updated in place; a new one is refused with ErrTooMany past the cap.
func (s *Service) Subscribe(ctx context.Context, sub Subscription, userAgent string) (Device, error) {
	if err := ValidateSubscription(sub, s.opts.AllowLoopbackHTTP); err != nil {
		return Device{}, err
	}
	known, err := s.hasEndpoint(ctx, sub.Endpoint)
	if err != nil {
		return Device{}, err
	}
	if !known {
		count, err := s.opts.Store.CountPushSubscriptions(ctx)
		if err != nil {
			return Device{}, err
		}
		if count >= MaxSubscriptions {
			return Device{}, ErrTooMany
		}
	}
	record := store.PushSubscription{
		Endpoint: sub.Endpoint, P256dh: sub.Keys.P256dh, Auth: sub.Keys.Auth,
		UserAgent: termtext.Cap(termtext.Clean(userAgent), maxUserAgent), CreatedAt: s.opts.Now(),
	}
	id, _, err := s.opts.Store.UpsertPushSubscription(ctx, record)
	if err != nil {
		return Device{}, err
	}
	devices, err := s.Devices(ctx)
	if err != nil {
		return Device{}, err
	}
	for _, device := range devices {
		if device.ID == id {
			return device, nil
		}
	}
	return Device{}, errors.New("push: the subscription vanished right after it was stored")
}

func (s *Service) hasEndpoint(ctx context.Context, endpoint string) (bool, error) {
	subs, err := s.opts.Store.ListPushSubscriptions(ctx)
	if err != nil {
		return false, err
	}
	for _, sub := range subs {
		if sub.Endpoint == endpoint {
			return true, nil
		}
	}
	return false, nil
}

// Unsubscribe removes the subscription for endpoint (this browser turning notifications off).
func (s *Service) Unsubscribe(ctx context.Context, endpoint string) (bool, error) {
	return s.opts.Store.DeletePushSubscriptionByEndpoint(ctx, endpoint)
}

// Remove removes a device by id (the device list's Remove).
func (s *Service) Remove(ctx context.Context, id int64) (bool, error) {
	return s.opts.Store.DeletePushSubscription(ctx, id)
}

// Devices lists every subscription as a Device, oldest first.
func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	subs, err := s.opts.Store.ListPushSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(subs))
	for _, sub := range subs {
		devices = append(devices, Device{
			ID: sub.ID, UserAgent: sub.UserAgent, Service: endpointHost(sub.Endpoint), CreatedAt: sub.CreatedAt,
			LastSuccessAt: timeOrNil(sub.LastSuccessAt), LastFailureAt: timeOrNil(sub.LastFailureAt), Failures: sub.Failures,
			EndpointHash: EndpointHash(sub.Endpoint),
		})
	}
	return devices, nil
}

// EndpointHash is how a device is matched to the browser asking (Device.EndpointHash).
func EndpointHash(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:16])
}

// HasEndpoint reports whether a subscription for endpoint exists.
func (s *Service) HasEndpoint(ctx context.Context, endpoint string) (bool, error) {
	return s.hasEndpoint(ctx, endpoint)
}

func timeOrNil(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
}

// ---- Settings -------------------------------------------------------------------

// Settings say which kinds of notice are pushed. They are one set for the whole install (one
// user), not per device: a phone and a laptop both hear what Jeff chose.
type Settings struct {
	Kinds map[Kind]bool `json:"kinds"`
}

// DefaultSettings pushes every kind. A job finishing is the one people turn off most, and it
// is a switch away; but "it stopped and nobody told me" is the problem crowsnest exists for.
func DefaultSettings() Settings {
	kinds := make(map[Kind]bool, len(AllKinds))
	for _, kind := range AllKinds {
		kinds[kind] = true
	}
	return Settings{Kinds: kinds}
}

// Settings returns the stored settings, with any kind not mentioned at its default.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	settings := DefaultSettings()
	raw, err := s.opts.Store.GetKV(ctx, kvSettings)
	if errors.Is(err, store.ErrNotFound) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("reading the notification settings: %w", err)
	}
	var stored Settings
	if decodeErr := json.Unmarshal([]byte(raw), &stored); decodeErr != nil {
		// A corrupt row must not silence every notice: fall back to the defaults (all on).
		s.opts.Logger.Warn("the stored notification settings are unreadable; using the defaults", "err", decodeErr)
		return settings, nil //nolint:nilerr // deliberate fallback, logged above
	}
	for kind, on := range stored.Kinds {
		if _, known := settings.Kinds[kind]; known {
			settings.Kinds[kind] = on
		}
	}
	return settings, nil
}

// UpdateSettings changes the kinds named in change (unknown kinds are an error) and returns the
// result.
func (s *Service) UpdateSettings(ctx context.Context, change map[Kind]bool) (Settings, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings, err := s.Settings(ctx)
	if err != nil {
		return settings, err
	}
	for kind, on := range change {
		if _, known := settings.Kinds[kind]; !known {
			return settings, fmt.Errorf("unknown notification kind %q", kind)
		}
		settings.Kinds[kind] = on
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return settings, fmt.Errorf("encoding the notification settings: %w", err)
	}
	if err := s.opts.Store.SetKV(ctx, kvSettings, string(encoded)); err != nil {
		return settings, fmt.Errorf("storing the notification settings: %w", err)
	}
	return settings, nil
}

// Enabled reports whether notices of kind are pushed; on a read error it says yes (a notice too
// many beats a silent stop).
func (s *Service) Enabled(ctx context.Context, kind Kind) bool {
	settings, err := s.Settings(ctx)
	if err != nil {
		s.opts.Logger.Warn("reading the notification settings failed; pushing anyway", "err", err)
		return true
	}
	return settings.Kinds[kind]
}
