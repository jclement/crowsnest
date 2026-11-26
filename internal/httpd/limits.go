// Limits that keep one unauthenticated client from exhausting the server (R1 L3): a cap on open
// connections, a small header limit (set in Run) and a deadline on reading the login body.
//
// The connection cap cannot be a per-request timeout: an SSE stream and a terminal WebSocket are
// meant to stay open for hours. So the cap counts connections instead. Past it, Accept waits for
// one to close (new clients queue in the kernel's backlog) rather than refusing, so a burst
// delays new connections without breaking the ones already open, and the header and body
// deadlines make sure a slow or silent connection gives its slot back within seconds.

package httpd

import (
	"net"
	"sync"
	"time"
)

const (
	// maxConnections caps open connections across all listeners. One user's browsers need a
	// handful each (up to six HTTP/1.1 connections, one event stream, one per open terminal,
	// itself capped at maxTerminals), so this is generous for real use while keeping a flood
	// well inside a default file-descriptor limit.
	maxConnections = 128
	// maxHeaderBytes caps request headers. crowsnest's own requests carry one small cookie;
	// net/http's default is a megabyte.
	maxHeaderBytes = 32 << 10
	// loginBodyTimeout bounds reading a login body (at most maxLoginBody bytes), so a client
	// cannot hold a connection by sending its body a byte at a time.
	loginBodyTimeout = 5 * time.Second
)

// connLimiter is a semaphore shared by every listener.
type connLimiter chan struct{}

func newConnLimiter(limit int) connLimiter { return make(connLimiter, limit) }

// wrap returns listener with Accept limited by the shared semaphore.
func (l connLimiter) wrap(listener net.Listener) net.Listener {
	return &limitedListener{Listener: listener, slots: l, done: make(chan struct{})}
}

// limitedListener holds a slot for each accepted connection until it is closed.
type limitedListener struct {
	net.Listener
	slots     connLimiter
	done      chan struct{}
	closeOnce sync.Once
}

// Accept waits for a free slot, then for a connection.
func (l *limitedListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitedConn{Conn: conn, release: func() { <-l.slots }}, nil
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
