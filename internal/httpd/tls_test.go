// Serving HTTPS directly with --tls-cert/--tls-key (DESIGN.md 4.13), over a real socket with a
// self-signed pair made by the test (M9: the flags had no test). A client that trusts the pair
// gets the app; a login over HTTPS sets the __Host- prefixed, Secure cookie; plain HTTP on the
// same port gets no app; a client that does not trust the certificate is refused by TLS.

package httpd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
)

// selfSignedPair writes a certificate and key for 127.0.0.1 and localhost, valid for an hour, and
// returns their paths and the certificate for a client's trust pool.
func selfSignedPair(t *testing.T) (certPath, keyPath string, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "crowsnest test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath, cert
}

func TestRunServesHTTPSWithASelfSignedPair(t *testing.T) {
	certPath, keyPath, cert := selfSignedPair(t)
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := auth.New(context.Background(), auth.Options{Username: testUser, Password: testPassword}, db)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(false)
	cfg.TLSCert, cfg.TLSKey = certPath, keyPath
	addr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), freePort(t))
	cfg.Listeners = []config.Listener{{Addr: addr, Source: "test"}}
	server, err := New(Deps{Config: cfg, Auth: service, Hub: hub.New(), Logger: slog.New(slog.DiscardHandler), Version: "v1", Web: testWeb})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run after cancel: %v", err)
		}
	})

	pool := x509.NewCertPool()
	pool.AddCert(cert)
	trusting := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	base := "https://" + addr.String()
	var response *http.Response
	waitFor(t, func() bool {
		response, err = trusting.Get(base + "/api/v1/health")
		return err == nil
	})
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.TLS == nil {
		t.Fatalf("health over HTTPS: %d, tls %v", response.StatusCode, response.TLS != nil)
	}

	// A login over HTTPS gets the __Host- cookie, Secure, and it works for the next call.
	login, err := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login", strings.NewReader(`{"username":"`+testUser+`","password":"`+testPassword+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Header.Set("Origin", base)
	login.Header.Set("X-Crowsnest", "1")
	login.Header.Set("Content-Type", "application/json")
	response, err = trusting.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	var session *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == SecureSessionCookie {
			session = cookie
		}
	}
	if response.StatusCode != http.StatusOK || session == nil || !session.Secure || !session.HttpOnly || session.Path != "/" || session.Domain != "" {
		t.Fatalf("login over HTTPS: %d, cookie %+v", response.StatusCode, session)
	}
	me, _ := http.NewRequest(http.MethodGet, base+"/api/v1/auth/me", nil)
	me.AddCookie(session)
	response, err = trusting.Do(me)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("me with the __Host- cookie over HTTPS: %d", response.StatusCode)
	}

	// Plain HTTP on the TLS port is not served the app.
	plain, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr.String() + "/api/v1/health")
	if err == nil {
		body, _ := io.ReadAll(plain.Body)
		_ = plain.Body.Close()
		if plain.StatusCode == http.StatusOK || bytes.Contains(body, []byte(`"status":"ok"`)) {
			t.Fatalf("plain HTTP on the TLS port answered %d %s", plain.StatusCode, body)
		}
	}

	// A client that does not trust the pair is refused at the handshake.
	if _, err := (&http.Client{Timeout: 5 * time.Second}).Get(base + "/api/v1/health"); err == nil {
		t.Fatal("an untrusting client got through the handshake")
	}
}
