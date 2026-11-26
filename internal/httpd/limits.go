// Limits that keep one unauthenticated client from exhausting the server (R1 L3): a cap on open
// connections, a small header limit (set in Run) and a deadline on reading the login body.
//
// The connection cap cannot be a per-request timeout: an SSE stream and a terminal WebSocket are
// meant to stay open for hours. So the cap counts connections instead. Past it, Accept waits for
// one to close (new clients queue in the kernel's backlog) rather than refusing, so a burst
// delays new connections without breaking the ones already open, and the header and body
// deadlines make sure a slow or silent connection gives its slot back within seconds.
//
// On its own that cap let one peer take every slot with idle connections before ever logging in
// (R1b N4). So each remote address may also hold at most maxConnectionsPerPeer; a connection past
// that is closed at once, and the slot it briefly held goes back. Loopback peers are exempt: a
// local reverse proxy (tailscale serve, a tunnel sharing the network namespace) carries every
// real user through one loopback address, and anything already on the machine has easier ways
// to make trouble than this.

package httpd

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	// maxConnections caps open connections across all listeners. One user's browsers need a
	// handful each (up to six HTTP/1.1 connections, one event stream, one per open terminal,
	// itself capped at maxTerminals), so this is generous for real use while keeping a flood
	// well inside a default file-descriptor limit.
	maxConnections = 128
	// maxConnectionsPerPeer caps the connections one non-loopback address may hold: a phone and
	// a laptop behind one NAT, each with a few tabs and terminals, fit with room to spare.
	maxConnectionsPerPeer = 32
	// maxHeaderBytes caps request headers. crowsnest's own requests carry one small cookie;
	// net/http's default is a megabyte.
	maxHeaderBytes = 32 << 10
	// loginBodyTimeout bounds reading a login body (at most maxLoginBody bytes), so a client
	// cannot hold a connection by sending its body a byte at a time.
	loginBodyTimeout = 5 * time.Second
)

// connLimiter is shared by every listener: a semaphore for the global cap and a count per peer.
type connLimiter struct {
	slots   chan struct{}
	perPeer int
	// exempt says which peers the per-peer cap skips (loopback; tests replace it).
	exempt func(netip.Addr) bool

	mu    sync.Mutex
	peers map[netip.Addr]int
}

func newConnLimiter(limit, perPeer int) *connLimiter {
	return &connLimiter{slots: make(chan struct{}, limit), perPeer: perPeer, exempt: netip.Addr.IsLoopback, peers: map[netip.Addr]int{}}
}

// wrap returns listener with Accept limited by the shared caps.
func (l *connLimiter) wrap(listener net.Listener) net.Listener {
	return &limitedListener{Listener: listener, limiter: l, done: make(chan struct{})}
}

// admit counts a new connection from peer, or reports false when peer is at its cap.
func (l *connLimiter) admit(peer netip.Addr) bool {
	if !peer.IsValid() || l.exempt(peer) {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peers[peer] >= l.perPeer {
		return false
	}
	l.peers[peer]++
	return true
}

func (l *connLimiter) leave(peer netip.Addr) {
	if !peer.IsValid() || l.exempt(peer) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peers[peer]--; l.peers[peer] <= 0 {
		delete(l.peers, peer)
	}
}

// peerOf is the remote IP of conn (unmapped, so ::ffff:1.2.3.4 and 1.2.3.4 count together), or
// the zero Addr when it has none.
func peerOf(conn net.Conn) netip.Addr {
	if tcp, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return tcp.AddrPort().Addr().Unmap()
	}
	return netip.Addr{}
}

// limitedListener holds a slot for each accepted connection until it is closed.
type limitedListener struct {
	net.Listener
	limiter   *connLimiter
	done      chan struct{}
	closeOnce sync.Once
}

// Accept waits for a free slot, then for a connection from a peer under its own cap.
func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		select {
		case l.limiter.slots <- struct{}{}:
		case <-l.done:
			return nil, net.ErrClosed
		}
		conn, err := l.Listener.Accept()
		if err != nil {
			<-l.limiter.slots
			return nil, err
		}
		peer := peerOf(conn)
		if !l.limiter.admit(peer) {
			_ = conn.Close() // refused, not queued: this peer already holds its share
			<-l.limiter.slots
			continue
		}
		return &limitedConn{Conn: conn, release: func() { l.limiter.leave(peer); <-l.limiter.slots }}, nil
	}
}

// Close stops a waiting Accept as well as the listener.
func (l *limitedListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// limitedConn gives its slot back exactly once, on the first Close.
type limitedConn struct {
	net.Conn
	releaseOnce sync.Once
	release     func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.releaseOnce.Do(c.release)
	return err
}
