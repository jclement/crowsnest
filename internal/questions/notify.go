// --notify-url: one plain-text line POSTed for each blocking question (DESIGN.md 8.3), in the
// style of ntfy. Only the title travels, scrubbed of token-shaped strings; the body, options and
// files never leave the machine this way.
//
// The URL itself is never logged: an ntfy topic is a secret of sorts, and a failed POST's error
// (*url.Error) names the URL, so only the cause underneath it is logged (R1b N5). Notices are
// rate limited per asking pane (unidentified askers share one bucket), so a looping or hijacked
// agent cannot flood the human's phone.

package questions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/ratelimit"
	"github.com/jclement/crowsnest/internal/redact"
	"github.com/jclement/crowsnest/internal/termtext"
)

const (
	notifyTimeout = 10 * time.Second
	// notifyMaxRunes keeps the notice to one phone notification.
	notifyMaxRunes = 200
	// notifyBurst notices per asking pane at once, then one more every notifyRefill.
	notifyBurst  = 3
	notifyRefill = time.Minute
)

// NotifyLine is the text POSTed for a question.
func NotifyLine(q Question) string {
	return termtext.Cap(redact.String(fmt.Sprintf("crowsnest: %s needs you: %s", q.ID, termtext.Clean(q.Title))), notifyMaxRunes)
}

// NewURLNotifier returns an OnAsked hook that POSTs NotifyLine to endpoint for blocking questions,
// in the background. client nil means a default client with a timeout.
func NewURLNotifier(endpoint string, client *http.Client, logger *slog.Logger) func(Question) {
	if client == nil {
		client = &http.Client{Timeout: notifyTimeout}
	}
	if logger == nil {
		logger = slog.Default()
	}
	limiter := ratelimit.New(notifyBurst, notifyRefill, nil)
	return func(q Question) {
		if q.Urgency != UrgencyBlocking {
			return
		}
		key := q.SourcePane
		if key == "" {
			key = "unidentified"
		}
		if ok, _ := limiter.Allow(key); !ok {
			logger.Info("not notifying --notify-url: too many blocking questions from one asker", "question", q.ID, "pane", q.SourcePane)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			if err := postNotice(ctx, client, endpoint, NotifyLine(q)); err != nil {
				logger.Warn("posting to --notify-url failed", "question", q.ID, "err", redact.String(err.Error()))
			}
		}()
	}
}

// postNotice POSTs line to endpoint. Its errors never contain the endpoint.
func postNotice(ctx context.Context, client *http.Client, endpoint, line string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(line))
	if err != nil {
		return errors.New("building the notice request: the notify URL is not a valid URL")
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("Title", "crowsnest")
	response, err := client.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err // *url.Error's text is `Post "<the URL>": …`
		}
		return fmt.Errorf("sending the notice: %w", err)
	}
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("the notify URL answered HTTP %d", response.StatusCode)
	}
	return nil
}

// NewTextNotifier posts arbitrary one-line notices (the job supervisor's "tell Jeff") to url,
// redacted and capped like question notices, in the background.
func NewTextNotifier(url string, client *http.Client, logger *slog.Logger) func(string) {
	if client == nil {
		client = &http.Client{Timeout: notifyTimeout}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return func(text string) {
		line := redact.String(strings.Join(strings.Fields(text), " "))
		if runes := []rune(line); len(runes) > 200 {
			line = string(runes[:199]) + "…"
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			if err := postNotice(ctx, client, url, line); err != nil {
				logger.Warn("posting to --notify-url failed", "err", redact.String(err.Error()))
			}
		}()
	}
}
