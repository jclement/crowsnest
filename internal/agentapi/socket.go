// The unix socket itself: where it lives, how it is made safe, and serving on it.

package agentapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// maxSocketPath is the longest unix socket path the platforms crowsnest runs on accept (macOS:
// 104 bytes including the terminating NUL).
const maxSocketPath = 103

// socketProbeTimeout bounds the connection attempt that tells a live socket from a stale one.
// A timeout is not taken as "stale" (see removeStale); it only stops start-up from hanging.
const socketProbeTimeout = 2 * time.Second

// SocketPath is where the agent socket lives (DESIGN.md section 11): under $XDG_RUNTIME_DIR when
// it is set (crowsnest/<session>.sock), else in the session's state directory (agent.sock). When
// that path would be too long for a unix socket (a deep XDG_STATE_HOME, such as macOS's per-user
// temporary directories), it falls back to crowsnest-<uid>/<hash of the state dir>.sock under the
// OS temporary directory, or under /tmp when that is too deep as well (R10-B4); Listen checks
// that directory belongs to this user.
func SocketPath(runtimeDir, stateDir, session string) string {
	return socketPath(runtimeDir, stateDir, session, os.TempDir(), os.Getuid())
}

func socketPath(runtimeDir, stateDir, session, tempDir string, uid int) string {
	if runtimeDir != "" {
		if path := filepath.Join(runtimeDir, "crowsnest", session+".sock"); len(path) <= maxSocketPath {
			return path
		}
	}
	if path := filepath.Join(stateDir, "agent.sock"); len(path) <= maxSocketPath {
		return path
	}
	sum := sha256.Sum256([]byte(stateDir))
	name := filepath.Join(fmt.Sprintf("crowsnest-%d", uid), hex.EncodeToString(sum[:6])+".sock")
	if path := filepath.Join(tempDir, name); len(path) <= maxSocketPath {
		return path
	}
	// A temporary directory this deep (a TMPDIR set inside a scratch folder, R10-B4) would make
	// --demo refuse to start, and a demo is the first thing a new user runs. /tmp exists on every
	// unix crowsnest supports and is short; Listen still checks the directory is this user's.
	return filepath.Join(shortTempDir, name)
}

// shortTempDir is the last resort for the socket's directory when TMPDIR is too deep.
const shortTempDir = "/tmp"

// Listen prepares the socket's directory (created 0700, and tightened to 0700 if it exists),
// removes a stale socket left by a crowsnest that died, refuses to take over one that a live
// crowsnest still answers on, and listens with the socket at 0600.
func Listen(ctx context.Context, path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("the agent socket path %s is longer than %d bytes, which unix sockets do not allow; start crowsnest with a shorter XDG_RUNTIME_DIR, XDG_STATE_HOME or TMPDIR", path, maxSocketPath)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the agent socket directory %s: %w", dir, err)
	}
	if err := checkOwnDirectory(dir); err != nil {
		return nil, err
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

// checkOwnDirectory refuses a socket directory that is a symlink or belongs to another user: in a
// shared temporary directory someone else could have created it first to catch the socket.
func checkOwnDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("checking the agent socket directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the agent socket directory %s is not a plain directory (a symlink?); remove it", dir)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("the agent socket directory %s belongs to another user; remove it or set XDG_RUNTIME_DIR", dir)
	}
	return nil
}

// removeStale deletes path only when it is provably a stale socket: the probe connection was
// refused, which means no process holds a listener on it. Any other probe failure (the start-up
// was cancelled, the probe timed out against a saturated backlog, permission denied) says
// nothing about whether another crowsnest is serving there, so the socket is left alone and the
// start refused (R1b-C02): unlinking a live instance's socket would leave it running but
// unreachable to every agent. The file is re-checked just before removal, so a socket replaced
// during the probe is not the one deleted. Anything else at the path (a live socket, a regular
// file) is left alone and reported.
func removeStale(ctx context.Context, path string) error {
	before, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking the agent socket path %s: %w", path, err)
	}
	if before.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf("%s exists and is not a socket; move it away so crowsnest can create its agent socket there", path)
	}
	dialer := net.Dialer{Timeout: socketProbeTimeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	switch {
	case err == nil:
		_ = conn.Close()
		return fmt.Errorf("another crowsnest is already serving the agent socket %s (same --session?); stop it first, or give this one another --session", path)
	case errors.Is(err, fs.ErrNotExist):
		return nil // removed by its owner while we looked
	case !errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("could not tell whether another crowsnest is serving the agent socket %s (%v), so it was left alone; "+
			"if no crowsnest is running for this --session, remove that file and start again", path, err)
	}
	after, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || !os.SameFile(before, after) {
		return fmt.Errorf("the agent socket %s changed while crowsnest was checking it; is another crowsnest starting? Try again", path)
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
