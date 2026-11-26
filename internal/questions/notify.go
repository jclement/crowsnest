// --notify-url: one plain-text line POSTed for each blocking question (DESIGN.md 8.3), in the
// style of ntfy. Only the title travels, scrubbed of token-shaped strings; the body, options and
// files never leave the machine this way.

package questions

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/redact"
)

const (
	notifyTimeout = 10 * time.Second
	// notifyMaxRunes keeps the notice to one phone notification.
	notifyMaxRunes = 200
)

// NotifyLine is the text POSTed for a question.
func NotifyLine(q Question) string {
	return capRunes(redact.String(fmt.Sprintf("crowsnest: %s needs you: %s", q.ID, clean(q.Title))), notifyMaxRunes)
}

// NewURLNotifier returns an OnAsked hook that POSTs NotifyLine to url for blocking questions,
// in the background. client nil means a default client with a timeout.
func NewURLNotifier(url string, client *http.Client, logger *slog.Logger) func(Question) {
	if client == nil {
		client = &http.Client{Timeout: notifyTimeout}
	}
	return func(q Question) {
		if q.Urgency != UrgencyBlocking {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			if err := postNotice(ctx, client, url, NotifyLine(q)); err != nil {
				// The URL may itself hold a topic secret, so it is not logged.
				logger.Warn("posting to --notify-url failed", "question", q.ID, "err", redact.String(err.Error()))
			}
		}()
	}
}

func postNotice(ctx context.Context, client *http.Client, url, line string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(line))
	if err != nil {
		return fmt.Errorf("building the notice request: %w", err)
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("Title", "crowsnest")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("the notify URL answered HTTP %d", response.StatusCode)
	}
	return nil
}
