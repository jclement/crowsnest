// Trusted proxies (R2-L2). The compose example runs cloudflared in crowsnest's network
// namespace, so every internet client arrives from 127.0.0.1: one login bucket for the owner and
// every attacker, and no per-client connection cap (loopback is exempt from it, limits.go).
// With --trusted-proxy, a request whose TCP peer is a trusted proxy is attributed to the client
// the proxy names: CF-Connecting-IP when it holds an IP, else the last X-Forwarded-For hop (the
// one the proxy itself appended). That address keys the login limiter, voice's per-client
// limits and the request log, and requests through the proxy are capped per client at
// maxConnectionsPerPeer in flight (the TCP cap cannot see past the proxy). X-Forwarded-Proto is
// believed from a trusted proxy as well as from loopback, still only to add Secure to the
// cookie.
//
// Without the flag nothing changes: forwarding headers are ignored and every client behind a
// local proxy shares the proxy's bucket, which fails safe. A trusted proxy on loopback is also
// trusted from any local process, which can therefore pick its own bucket; such a process (an
// agent) already runs as the user, so this costs nothing that was not already lost.

package httpd

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

// clientKey carries the attributed client in a request's context.
type clientKey struct{}

// clientInfo is who a request is from.
type clientInfo struct {
	ip       string // the client's address, as clientIP returns it
	viaProxy bool   // the TCP peer is a trusted proxy
}

// peerAddr is the TCP peer of r, unmapped; the zero Addr when RemoteAddr is not an address.
func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

// forwardedClient is the client a trusted proxy names, or the zero Addr.
func forwardedClient(r *http.Request) netip.Addr {
	if addr, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
		return addr.Unmap()
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if addr, err := netip.ParseAddr(strings.TrimSpace(hops[len(hops)-1])); err == nil {
		return addr.Unmap()
	}
	return netip.Addr{}
}

// trusted reports whether peer is one of the configured trusted proxies.
func (s *Server) trusted(peer netip.Addr) bool {
	for _, prefix := range s.deps.Config.TrustedProxies {
		if peer.IsValid() && prefix.Contains(peer) {
			return true
		}
	}
	return false
}

// trustProxies attributes requests from trusted proxies to their real client and caps each such
// client's requests in flight. It is the outermost middleware, so the log and every limiter see
// the attributed client.
func (s *Server) trustProxies(next http.Handler) http.Handler {
	if len(s.deps.Config.TrustedProxies) == 0 {
		return next
	}
	inFlight := &perClient{count: map[string]int{}, limit: maxConnectionsPerPeer}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer := peerAddr(r)
		if !s.trusted(peer) {
			next.ServeHTTP(w, r)
			return
		}
		client := forwardedClient(r)
		if !client.IsValid() {
			client = peer // the proxy named nobody: it is its own client
		}
		ip := client.String()
		if !inFlight.enter(ip) {
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests from your address at once; close some tabs or terminals and try again.")
			return
		}
		defer inFlight.leave(ip)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, clientInfo{ip: ip, viaProxy: true})))
	})
}

// perClient counts requests in flight per client address.
type perClient struct {
	mu    sync.Mutex
	count map[string]int
	limit int
}

func (p *perClient) enter(ip string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.count[ip] >= p.limit {
		return false
	}
	p.count[ip]++
	return true
}

func (p *perClient) leave(ip string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.count[ip]--; p.count[ip] <= 0 {
		delete(p.count, ip)
	}
}
