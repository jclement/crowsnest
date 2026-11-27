// Security middleware (DESIGN.md section 4): the host check that stops DNS rebinding, the origin
// check and X-Crowsnest header that stop CSRF and cross-site WebSocket hijacking, security
// headers, session authentication, and a request log that never records secrets.

package httpd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
)

// contentSecurityPolicy is DESIGN.md 4.7 verbatim. 'unsafe-inline' styles are for xterm.js and
// mermaid; there is no inline script anywhere, which is why the theme bootstrap is a file.
// worker-src and manifest-src name what default-src already allows, so a later loosening of
// default-src cannot widen where the service worker (/sw.js, Web Push) may come from.
const contentSecurityPolicy = "default-src 'self'; connect-src 'self'; img-src 'self' data: blob:; " +
	"media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; worker-src 'self'; " +
	"manifest-src 'self'; frame-ancestors 'none'"

// csrfHeader must be on every state-changing request. A cross-site HTML form cannot set custom
// headers, and a cross-site fetch that sets one triggers a CORS preflight that crowsnest never
// approves.
const csrfHeader = "X-Crowsnest"

// ---- Host check --------------------------------------------------------------

// hostSet is the lowercase set of names the Host header may carry.
type hostSet map[string]struct{}

func newHostSet(names []string) hostSet {
	set := hostSet{}
	for _, name := range names {
		set[strings.ToLower(name)] = struct{}{}
	}
	return set
}

// allows reports whether a hostname (no port, no brackets) is acceptable: any loopback IP
// literal, or a name in the set.
func (h hostSet) allows(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	if addr, err := netip.ParseAddr(hostname); err == nil && addr.Unmap().IsLoopback() {
		return true
	}
	_, ok := h[hostname]
	return ok
}

// hostAllowed is the host check: the names fixed at start, or one added since (AllowHost).
func (s *Server) hostAllowed(hostname string) bool {
	if s.hosts.allows(hostname) {
		return true
	}
	_, added := s.addedHosts.Load(strings.ToLower(strings.TrimSuffix(hostname, ".")))
	return added
}

// AllowHost adds a name to the host check while the server runs. Publishing to the tailnet
// (internal/tsserve) uses it for the machine's MagicDNS name when start could not detect it
// (Tailscale came up later); --no-tailscale-detect is honoured by the caller, which then does
// not add it. Names are only ever added, never removed, for the life of the process.
func (s *Server) AllowHost(name string) {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name != "" {
		s.addedHosts.Store(name, struct{}{})
	}
}

// HostAllowed reports whether the host check accepts name now.
func (s *Server) HostAllowed(name string) bool { return s.hostAllowed(name) }

// splitHost returns the hostname part of a Host header value, without port or IPv6 brackets.
func splitHost(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
}

// checkHost rejects requests whose Host header names something other than this machine. That is
// what defeats DNS rebinding: the attacker's page can point its own name at 127.0.0.1, but the
// browser still sends that name as Host. It applies with --no-auth too.
func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(splitHost(r.Host)) {
			s.deps.Logger.Warn("rejected request for unknown host", "host", r.Host, "client", clientIP(r))
			writeError(w, http.StatusForbidden, codeForbidden,
				"This host name is not allowed. Restart crowsnest with --allowed-host "+splitHost(r.Host)+" if it should be.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- Origin check ------------------------------------------------------------

// checkOrigin guards every non-GET request and every WebSocket upgrade: Origin must be present,
// its host must pass the host check, and it must name the same host and port as Host.
// State-changing requests must also carry X-Crowsnest: 1.
func (s *Server) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safe := r.Method == http.MethodGet || r.Method == http.MethodHead
		upgrade := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		if safe && !upgrade {
			next.ServeHTTP(w, r)
			return
		}
		if reason := s.originProblem(r); reason != "" {
			s.deps.Logger.Warn("rejected cross-origin request", "reason", reason, "method", r.Method, "path", r.URL.Path, "client", clientIP(r))
			writeError(w, http.StatusForbidden, codeForbidden, reason)
			return
		}
		if !safe && r.Header.Get(csrfHeader) != "1" {
			writeError(w, http.StatusForbidden, codeForbidden, "Missing X-Crowsnest: 1 header.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originProblem returns why the Origin header is unacceptable, or "" when it is fine.
func (s *Server) originProblem(r *http.Request) string {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return "Missing Origin header."
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "Malformed Origin header."
	}
	if !s.hostAllowed(parsed.Hostname()) {
		return "Origin is not an allowed host."
	}
	if normalizeAuthority(parsed.Host, parsed.Scheme) != normalizeAuthority(r.Host, parsed.Scheme) {
		return "Origin does not match Host."
	}
	return ""
}

// normalizeAuthority lowercases host[:port] and drops the scheme's default port, so
// "Example:443" over https equals "example".
func normalizeAuthority(authority, scheme string) string {
	authority = strings.ToLower(authority)
	defaultPort := map[string]string{"http": ":80", "https": ":443"}[scheme]
	return strings.TrimSuffix(authority, defaultPort)
}

// ---- Security headers --------------------------------------------------------

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "same-origin")
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// ---- Authentication ----------------------------------------------------------

// sessionContextKey carries the auth.Session for the request.
type sessionContextKey struct{}

// requireAuth admits requests with a valid session cookie (or everything with --no-auth). A
// sliding refresh re-sends the cookie with its new lifetime; a dead session clears it.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.deps.Auth.Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		token := sessionToken(r)
		session, err := s.deps.Auth.Authenticate(r.Context(), token, sessionHost(r))
		if err == nil && !s.tailscaleSessionValid(r, session) {
			err = auth.ErrNoSession // signed in with Tailscale, and that no longer holds (DESIGN.md 4.20)
		}
		if errors.Is(err, auth.ErrNoSession) {
			if tailnet, ok := s.signInWithTailscale(w, r); ok {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, tailnet)))
				return
			}
			if token != "" {
				clearSessionCookie(w, r)
			}
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "Sign in first.")
			return
		}
		if err != nil {
			s.internalError(w, r, "authenticating", err)
			return
		}
		if session.Refreshed {
			setSessionCookie(w, r, token, session.ExpiresAt)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, session)))
	})
}

// ---- Request log -------------------------------------------------------------

// statusRecorder captures the status code. Unwrap lets http.ResponseController reach the real
// writer, which the SSE handler needs for Flush.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// logRequests records method, path, status, duration and client IP, and nothing else
// (DESIGN.md 4.9): no query string, no headers, no bodies, so no cookie or password can land in
// a log line.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		s.deps.Logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(started).Milliseconds(),
			"client", clientIP(r))
	})
}

// clientIP is the client's address: the TCP peer, or, for a request through a --trusted-proxy,
// the client that proxy names (proxy.go, R2-L2). Forwarding headers from anyone else are
// ignored: they are client-supplied, and trusting them would let anyone pick their own
// rate-limit bucket. Without a trusted proxy every client behind a local proxy shares the
// proxy's bucket, which fails safe.
func clientIP(r *http.Request) string {
	if info, ok := r.Context().Value(clientKey{}).(clientInfo); ok {
		return info.ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
