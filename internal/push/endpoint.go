// What a subscription from the browser must look like, and the SSRF guard.
//
// The endpoint URL is chosen by the browser (its push service picks it), and the server will
// POST to it. A logged-in page, or anyone holding a session, could hand in any URL, so the server
// would become a way to reach inside the network it runs in: the router, a cloud metadata
// address, another service on localhost. Two fences:
//
//  1. At subscribe time: https only, a host name or a public IP, no credentials, a sane length.
//  2. At send time: the guarded client's dialer refuses every address that is not public
//     unicast, after DNS. A public name that resolves to 10.0.0.1 (or is re-pointed there later,
//     DNS rebinding) is refused when the connection is made. Redirects are not followed, and
//     no proxy from the environment is used (the dial would then be to the proxy, not the
//     endpoint, and the guard would be checking the wrong address).
//
// The check is an address check, not an allowlist of push services: browsers use FCM, Mozilla's
// autopush, Apple and Microsoft today, and others (self-hosted autopush, UnifiedPush) are
// legitimate. The cost is that the server can be made to POST an encrypted blob to any public
// https URL, which a session holder (who has a shell anyway) could do more simply.

package push

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	maxEndpointLength = 2048
	sendTimeout       = 15 * time.Second
	// p256PointLength is an uncompressed P-256 public key: 0x04, then X and Y of 32 bytes each.
	p256PointLength = 65
	// authSecretLength is the browser's auth secret (RFC 8291 section 3.2).
	authSecretLength = 16
)

// Subscription is what the browser's PushSubscription.toJSON() gives, as posted to
// POST /push/subscriptions.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// ValidationError is a subscription the server will not store; its text is for the browser.
type ValidationError struct{ Reason string }

func (e *ValidationError) Error() string { return e.Reason }

func invalid(format string, args ...any) error {
	return &ValidationError{Reason: fmt.Sprintf(format, args...)}
}

// ValidateSubscription checks a subscription from the browser. allowLoopbackHTTP (--demo only)
// also accepts http:// to a loopback address, for the hermetic suite's fake push service.
func ValidateSubscription(sub Subscription, allowLoopbackHTTP bool) error {
	if err := ValidateEndpoint(sub.Endpoint, allowLoopbackHTTP); err != nil {
		return err
	}
	if err := checkKey(sub.Keys.P256dh, p256PointLength, "p256dh"); err != nil {
		return err
	}
	point, _ := decodeBase64URL(sub.Keys.P256dh)
	if point[0] != 0x04 {
		return invalid("keys.p256dh must be an uncompressed P-256 point")
	}
	return checkKey(sub.Keys.Auth, authSecretLength, "auth")
}

func checkKey(value string, length int, name string) error {
	decoded, err := decodeBase64URL(value)
	if err != nil || len(decoded) != length {
		return invalid("keys.%s must be %d bytes, base64url encoded", name, length)
	}
	return nil
}

// decodeBase64URL accepts base64url with or without padding (browsers send it without).
func decodeBase64URL(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
}

// ValidateEndpoint checks the URL the server will POST to.
func ValidateEndpoint(endpoint string, allowLoopbackHTTP bool) error {
	if endpoint == "" || len(endpoint) > maxEndpointLength {
		return invalid("endpoint must be a URL of at most %d characters", maxEndpointLength)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.Opaque != "" {
		return invalid("endpoint must be an absolute https URL")
	}
	if parsed.User != nil {
		return invalid("endpoint must not carry a user name or password")
	}
	if parsed.Fragment != "" {
		return invalid("endpoint must not have a fragment")
	}
	host := parsed.Hostname()
	loopbackHTTP := allowLoopbackHTTP && parsed.Scheme == "http" && isLoopbackHost(host)
	if parsed.Scheme != "https" && !loopbackHTTP {
		return invalid("endpoint must be an https URL (push services are)")
	}
	if loopbackHTTP {
		return nil
	}
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return invalid("endpoint must be a public push service, not this machine")
	}
	if addr, err := netip.ParseAddr(host); err == nil && !publicAddress(addr) {
		return invalid("endpoint must be a public push service, not a private or local address")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

// sharedAddressSpace is 100.64.0.0/10 (RFC 6598): carrier-grade NAT, and the tailnet's own
// addresses. Not "private" to netip, but never a push service.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// nat64 is the well-known NAT64 prefix (RFC 6052).
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// publicAddress reports whether addr is a global unicast address outside every private, local,
// shared and special-purpose range a push service could not be on.
func publicAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	switch {
	case !addr.IsValid(), !addr.IsGlobalUnicast(), addr.IsPrivate(), addr.IsLoopback(),
		addr.IsLinkLocalUnicast(), addr.IsMulticast(), addr.IsUnspecified():
		return false
	case addr.Is4() && sharedAddressSpace.Contains(addr):
		return false
	case addr.Is6() && nat64.Contains(addr):
		// NAT64 can embed any IPv4 address, private ones included.
		return false
	}
	return true
}

// errBlockedAddress is the dial refusal; its text names the address class, never the URL.
var errBlockedAddress = errors.New("refused: the push endpoint resolved to an address that is not public")

// GuardedClient is the client that posts to push services: every connection's address is checked
// after DNS (publicAddress), redirects are not followed, no proxy is used. allowLoopback (--demo)
// lets it dial loopback too.
func GuardedClient(allowLoopback bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			return dialAllowed(address, allowLoopback)
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          8,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   sendTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// dialAllowed is the guard's decision for one connection: address is the resolved ip:port the
// dialer is about to connect to.
func dialAllowed(address string, allowLoopback bool) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return errBlockedAddress
	}
	addr := addrPort.Addr().Unmap()
	if allowLoopback && addr.IsLoopback() {
		return nil
	}
	if !publicAddress(addr) {
		return errBlockedAddress
	}
	return nil
}

// endpointHost is the push service's host, what logs and the device list show: the path of an
// endpoint is the per-device token that grants the right to push to it.
func endpointHost(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "unknown"
	}
	return parsed.Host
}
