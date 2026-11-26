// The unix socket itself: where it lives, how it is made safe, and serving on it.

package agentapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// maxSocketPath is the longest unix socket path the platforms crowsnest runs on accept (macOS:
// 104 bytes including the terminating NUL).
const maxSocketPath = 103

// SocketPath is where the agent socket lives (DESIGN.md section 11): under $XDG_RUNTIME_DIR when
// it is set (crowsnest/<session>.sock), else in the session's state directory (agent.sock).
func SocketPath(runtimeDir, stateDir, session string) string {
	if runtimeDir != "" {
		return filepath.Join(runtimeDir, "crowsnest", session+".sock")
	}
	return filepath.Join(stateDir, "agent.sock")
}

// Listen prepares the socket's directory (created 0700, and tightened to 0700 if it exists),
// removes a stale socket left by a crowsnest that died, refuses to take over one that a live
// crowsnest still answers on, and listens with the socket at 0600.
func Listen(ctx context.Context, path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("the agent socket path %s is longer than %d bytes, which unix sockets do not allow; set XDG_RUNTIME_DIR or a shorter XDG_STATE_HOME", path, maxSocketPath)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the agent socket directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory needs x to be entered; 0700 is owner-only
		return nil, fmt.Errorf("restricting the agent socket directory %s to its owner: %w", dir, err)
	}
	if err := removeStale(ctx, path); err != nil {
		return nil, err
	}
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on the agent socket %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("restricting the agent socket %s to its owner: %w", path, err)
	}
	return listener, nil
}

// removeStale deletes path if it is a socket nobody answers on. Anything else at that path (a
// live socket, a regular file) is left alone and reported.
func removeStale(ctx context.Context, path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking the agent socket path %s: %w", path, err)
	}
	if info.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf("%s exists and is not a socket; move it away so crowsnest can create its agent socket there", path)
	}
	dialer := net.Dialer{Timeout: time.Second}
	if conn, err := dialer.DialContext(ctx, "unix", path); err == nil {
		_ = conn.Close()
		return fmt.Errorf("another crowsnest is already serving the agent socket %s (same --session?)", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing the stale agent socket %s: %w", path, err)
	}
	return nil
}

// Serve runs the agent API on listener until ctx ends, then shuts down and removes the socket.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	server := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: a long-poll legitimately holds a response for up to two minutes.
	}
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serving the agent socket: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Long-polls are cut off rather than waited for; their clients poll again.
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
	}
	if addr, ok := listener.Addr().(*net.UnixAddr); ok {
		_ = os.Remove(addr.Name)
	}
	return nil
}
