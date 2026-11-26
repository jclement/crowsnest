// Tests for the push service against a fake push service (httptest): the RFC 8291 encryption
// decrypted with the browser's private key, the VAPID header verified with the public key,
// TTL/Urgency/Topic, pruning on 404/410, failures logged without the endpoint, the cap, the
// settings, and the key pair kept across restarts and never logged.

package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// ---- Helpers ---------------------------------------------------------------------

// browser is a fake browser's push key pair, as PushManager would make one.
type browser struct {
	private *ecdh.PrivateKey
	auth    []byte
}

func newBrowser(t *testing.T) browser {
	t.Helper()
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return browser{private: private, auth: auth}
}

func (b browser) subscription(endpoint string) Subscription {
	var sub Subscription
	sub.Endpoint = endpoint
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(b.private.PublicKey().Bytes())
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(b.auth)
	return sub
}

// decrypt undoes RFC 8291 (Web Push message encryption over RFC 8188 aes128gcm) with the
// browser's private key: what a real browser does before the service worker sees the payload.
func (b browser) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body too short: %d bytes", len(body))
	}
	salt, idLength := body[:16], int(body[20])
	serverPublicBytes := body[21 : 21+idLength]
	ciphertext := body[21+idLength:]
	serverPublic, err := ecdh.P256().NewPublicKey(serverPublicBytes)
	if err != nil {
		t.Fatalf("key id is not a P-256 point: %v", err)
	}
	shared, err := b.private.ECDH(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	keyInfo := append(append([]byte("WebPush: info\x00"), b.private.PublicKey().Bytes()...), serverPublicBytes...)
	prkKey, _ := hkdf.Extract(sha256.New, shared, b.auth)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypting the push body: %v", err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatal("missing the last-record padding delimiter")
	}
	_ = binary.BigEndian // the record size (body[16:20]) is not needed for one record
	return plain[:len(plain)-1]
}

// received is one POST the fake push service got.
type received struct {
	path   string
	header http.Header
	body   []byte
}

// fakePushService records POSTs and answers with status.
type fakePushService struct {
	mu       sync.Mutex
	requests []received
	status   int
	server   *httptest.Server
}

func newFakePushService(t *testing.T, status int) *fakePushService {
	t.Helper()
	fake := &fakePushService{status: status}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fake.mu.Lock()
		fake.requests = append(fake.requests, received{path: r.URL.Path, header: r.Header.Clone(), body: body})
		code := fake.status
		fake.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakePushService) got() []received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]received(nil), f.requests...)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newTestService(t *testing.T, db *store.Store, client *http.Client) (*Service, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	service, err := New(context.Background(), Options{
		Store: db, Logger: slog.New(slog.NewTextHandler(logs, nil)), HTTPClient: client, AllowLoopbackHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, logs
}

func memoryStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---- Keys --------------------------------------------------------------------------

func TestKeysAreMadeOnceKeptAndNeverLogged(t *testing.T) {
	db := memoryStore(t)
	first, logs := newTestService(t, db, nil)
	second, _ := newTestService(t, db, nil)
	if first.PublicKey() == "" || first.PublicKey() != second.PublicKey() {
		t.Fatalf("the key pair must be made once and reused: %q vs %q", first.PublicKey(), second.PublicKey())
	}
	public, err := base64.RawURLEncoding.DecodeString(first.PublicKey())
	if err != nil || len(public) != 65 || public[0] != 0x04 {
		t.Fatalf("public key is not an uncompressed P-256 point: %d bytes, %v", len(public), err)
	}
	if strings.Contains(logs.String(), first.keys.Private) {
		t.Fatal("the private key reached the log")
	}
}

// ---- Delivery ----------------------------------------------------------------------

func TestDeliverEncryptsForTheBrowserAndSignsWithVAPID(t *testing.T) {
	fake := newFakePushService(t, http.StatusCreated)
	service, _ := newTestService(t, memoryStore(t), fake.server.Client())
	browser := newBrowser(t)
	ctx := context.Background()
	if _, err := service.Subscribe(ctx, browser.subscription(fake.server.URL+"/push/secret-token-abc"), "Mozilla/5.0 test"); err != nil {
		t.Fatal(err)
	}
	notice := Notice{Kind: KindQuestion, Title: "An agent is blocked on you", Body: "Ship it?", Tag: "q_7", URL: "/inbox?q=q_7", Urgency: UrgencyHigh, Source: "pane:w1:p1"}
	result, err := service.Deliver(ctx, notice, "")
	if err != nil || result.Sent != 1 || result.Failed != 0 {
		t.Fatalf("result %+v, err %v", result, err)
	}
	requests := fake.got()
	if len(requests) != 1 {
		t.Fatalf("got %d requests", len(requests))
	}
	request := requests[0]
	var got payload
	if err := json.Unmarshal(browser.decrypt(t, request.body), &got); err != nil {
		t.Fatal(err)
	}
	want := payload{Title: notice.Title, Body: notice.Body, Tag: "q_7", URL: "/inbox?q=q_7", Kind: KindQuestion}
	if got != want {
		t.Fatalf("decrypted payload %+v, want %+v", got, want)
	}
	for header, value := range map[string]string{"Content-Encoding": "aes128gcm", "Urgency": "high", "TTL": "86400", "Topic": topicFor("q_7")} {
		if request.header.Get(header) != value {
			t.Errorf("%s = %q, want %q", header, request.header.Get(header), value)
		}
	}
	verifyVAPID(t, request.header.Get("Authorization"), service.PublicKey(), fake.server.URL)
}

// verifyVAPID checks the RFC 8292 header: an ES256 JWT for the push service's origin, signed by
// the key it names, and that key is the one the browser was given.
func verifyVAPID(t *testing.T, header, publicKey, origin string) {
	t.Helper()
	var token, key string
	if _, err := fmt.Sscanf(strings.ReplaceAll(header, ",", ""), "vapid t=%s k=%s", &token, &key); err != nil {
		t.Fatalf("Authorization %q: %v", header, err)
	}
	if key != publicKey {
		t.Fatalf("VAPID k=%q, want the served public key %q", key, publicKey)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	claimsJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Aud string `json:"aud"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	_ = json.Unmarshal(claimsJSON, &claims)
	if claims.Aud != origin || claims.Sub != subscriber || claims.Exp <= time.Now().Unix() {
		t.Fatalf("claims %+v, want aud %q and sub %q", claims, origin, subscriber)
	}
	keyBytes, _ := base64.RawURLEncoding.DecodeString(key)
	x, y := elliptic.Unmarshal(elliptic.P256(), keyBytes) //nolint:staticcheck // test-only check of a P-256 point
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, digest[:], r, s) {
		t.Fatal("the VAPID JWT is not signed by the served key")
	}
}

func TestUrgencyMapsToTTLAndHeader(t *testing.T) {
	cases := []struct {
		urgency Urgency
		header  string
		ttl     string
	}{{UrgencyHigh, "high", "86400"}, {UrgencyNormal, "normal", "43200"}, {UrgencyLow, "low", "14400"}, {"", "normal", "43200"}}
	for _, c := range cases {
		t.Run(string(c.urgency), func(t *testing.T) {
			fake := newFakePushService(t, http.StatusCreated)
			service, _ := newTestService(t, memoryStore(t), fake.server.Client())
			_, _ = service.Subscribe(context.Background(), newBrowser(t).subscription(fake.server.URL+"/p"), "")
			_, _ = service.Deliver(context.Background(), Notice{Kind: KindJob, Title: "t", Urgency: c.urgency}, "")
			request := fake.got()[0]
			if request.header.Get("Urgency") != c.header || request.header.Get("TTL") != c.ttl {
				t.Fatalf("Urgency %q TTL %q, want %q %q", request.header.Get("Urgency"), request.header.Get("TTL"), c.header, c.ttl)
			}
		})
	}
}

func TestGoneSubscriptionsArePrunedAndFailuresKept(t *testing.T) {
	for _, c := range []struct {
		status int
		pruned bool
	}{{http.StatusGone, true}, {http.StatusNotFound, true}, {http.StatusInternalServerError, false}, {http.StatusTooManyRequests, false}} {
		t.Run(fmt.Sprint(c.status), func(t *testing.T) {
			fake := newFakePushService(t, c.status)
			db := memoryStore(t)
			service, logs := newTestService(t, db, fake.server.Client())
			ctx := context.Background()
			_, _ = service.Subscribe(ctx, newBrowser(t).subscription(fake.server.URL+"/push/very-secret-device-token"), "")
			result, _ := service.Deliver(ctx, Notice{Kind: KindJob, Title: "t"}, "")
			count, _ := db.CountPushSubscriptions(ctx)
			if c.pruned != (count == 0) || c.pruned != (result.Pruned == 1) {
				t.Fatalf("status %d: result %+v, %d left", c.status, result, count)
			}
			if !c.pruned {
				subs, _ := db.ListPushSubscriptions(ctx)
				if subs[0].Failures != 1 || subs[0].LastFailureAt.IsZero() {
					t.Fatalf("failure not recorded: %+v", subs[0])
				}
			}
			if strings.Contains(logs.String(), "very-secret-device-token") {
				t.Fatal("the endpoint's secret path reached the log")
			}
		})
	}
}

func TestTransportErrorsAreLoggedWithoutTheEndpoint(t *testing.T) {
	fake := newFakePushService(t, http.StatusCreated)
	db := memoryStore(t)
	service, logs := newTestService(t, db, fake.server.Client())
	ctx := context.Background()
	_, _ = service.Subscribe(ctx, newBrowser(t).subscription(fake.server.URL+"/push/very-secret-device-token"), "")
	fake.server.Close() // connection refused from now on
	result, _ := service.Deliver(ctx, Notice{Kind: KindJob, Title: "t"}, "")
	if result.Failed != 1 {
		t.Fatalf("result %+v", result)
	}
	if !strings.Contains(logs.String(), "Web Push send failed") || strings.Contains(logs.String(), "very-secret-device-token") {
		t.Fatalf("log must report the failure without the endpoint:\n%s", logs.String())
	}
}

func TestDeliverToOneEndpointOnly(t *testing.T) {
	fake := newFakePushService(t, http.StatusCreated)
	service, _ := newTestService(t, memoryStore(t), fake.server.Client())
	ctx := context.Background()
	_, _ = service.Subscribe(ctx, newBrowser(t).subscription(fake.server.URL+"/a"), "")
	_, _ = service.Subscribe(ctx, newBrowser(t).subscription(fake.server.URL+"/b"), "")
	result, _ := service.Deliver(ctx, TestNotice(), fake.server.URL+"/b")
	if result.Sent != 1 || len(fake.got()) != 1 || fake.got()[0].path != "/b" {
		t.Fatalf("result %+v, requests %+v", result, fake.got())
	}
}

// ---- Subscriptions and settings ----------------------------------------------------

func TestSubscribeCapsDevicesAndUpdatesAKnownEndpoint(t *testing.T) {
	service, _ := newTestService(t, memoryStore(t), nil)
	ctx := context.Background()
	for i := range MaxSubscriptions {
		if _, err := service.Subscribe(ctx, newBrowser(t).subscription(fmt.Sprintf("https://push.example.com/d/%d", i)), ""); err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
	}
	if _, err := service.Subscribe(ctx, newBrowser(t).subscription("https://push.example.com/d/one-too-many"), ""); err != ErrTooMany {
		t.Fatalf("past the cap: %v, want ErrTooMany", err)
	}
	// A browser resubscribing with the same endpoint (new keys) is not a new device.
	device, err := service.Subscribe(ctx, newBrowser(t).subscription("https://push.example.com/d/3"), "Firefox")
	if err != nil || device.UserAgent != "Firefox" || device.Service != "push.example.com" {
		t.Fatalf("resubscribe: %+v, %v", device, err)
	}
	if device.EndpointHash != EndpointHash("https://push.example.com/d/3") {
		t.Fatal("the device must carry its endpoint hash")
	}
	devices, _ := service.Devices(ctx)
	if len(devices) != MaxSubscriptions {
		t.Fatalf("%d devices", len(devices))
	}
	encoded, _ := json.Marshal(devices)
	if strings.Contains(string(encoded), "push.example.com/d/") {
		t.Fatal("a device listing must never carry an endpoint URL")
	}
}

func TestSettingsDefaultUpdateAndRefuseUnknownKinds(t *testing.T) {
	service, _ := newTestService(t, memoryStore(t), nil)
	ctx := context.Background()
	settings, _ := service.Settings(ctx)
	for _, kind := range AllKinds {
		if !settings.Kinds[kind] {
			t.Fatalf("%s must default to on", kind)
		}
	}
	if _, err := service.UpdateSettings(ctx, map[Kind]bool{KindDone: false}); err != nil {
		t.Fatal(err)
	}
	if service.Enabled(ctx, KindDone) || !service.Enabled(ctx, KindQuestion) {
		t.Fatal("done must be off and question still on")
	}
	if _, err := service.UpdateSettings(ctx, map[Kind]bool{"spam": true}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
}
