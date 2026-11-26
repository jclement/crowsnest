// The background check behind /api/v1/health's update_available and the footer: one look at
// GitHub shortly after start, then once a day, never blocking anything and silent on failure.

package update

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// recheckInterval is how often a long-running server looks again. Releases are rare; GitHub's
// unauthenticated limit is 60 requests an hour per IP, shared with everything else on the box.
const recheckInterval = 24 * time.Hour

// Checker remembers the newest release seen that is newer than the running version.
type Checker struct {
	client  *Client
	current string
	logger  *slog.Logger

	mu     sync.Mutex
	latest *Release
}

// NewChecker returns a Checker for currentVersion. Start begins checking.
func NewChecker(client *Client, currentVersion string, logger *slog.Logger) *Checker {
	return &Checker{client: client, current: currentVersion, logger: logger}
}

// Start checks now and then daily until ctx ends, in the background. It does nothing at all for
// a development build or inside a container (Unsupported), so those never call GitHub.
func (c *Checker) Start(ctx context.Context) {
	if reason := Unsupported(c.current); reason != nil {
		c.logger.Debug("update check off", "reason", reason)
		return
	}
	go func() {
		for {
			c.CheckOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(recheckInterval):
			}
		}
	}()
}

// CheckOnce asks GitHub once (bounded by CheckTimeout) and records a newer release. A failure is
// logged at debug level and changes nothing: being offline is not an error worth a warning.
func (c *Checker) CheckOnce(ctx context.Context) {
	rel, err := c.client.Latest(ctx)
	if err != nil {
		c.logger.Debug("update check failed", "err", err)
		return
	}
	if !Newer(rel.Version, c.current) {
		return
	}
	c.mu.Lock()
	first := c.latest == nil || c.latest.Version != rel.Version
	c.latest = &rel
	c.mu.Unlock()
	if first {
		c.logger.Info("update available", "current", c.current, "latest", rel.TagName, "url", rel.URL)
	}
}

// Available returns the newer release, if one has been seen. A nil Checker has none.
func (c *Checker) Available() (Release, bool) {
	if c == nil {
		return Release{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest == nil {
		return Release{}, false
	}
	return *c.latest, true
}
