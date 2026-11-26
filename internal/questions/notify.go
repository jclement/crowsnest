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
	"regexp"
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

// Job notices (R2-L3): per job, the same burst and refill as a question asker; across all of
// them, textBurst then one every textRefill, so many jobs at once still cannot flood the phone.
const (
	textBurst  = 10
	textRefill = time.Minute
)

// noticeJob finds the job a supervisor notice is about ("crowsnest: job j_7 …"), its rate-limit
// key.
var noticeJob = regexp.MustCompile(`\bjob (j_[0-9]+)\b`)

// NewTextNotifier posts arbitrary one-line notices (the job supervisor's "tell Jeff") to url,
// cleaned (internal/termtext), redacted and capped like question notices, in the background.
// They are rate limited per job and in total (R2-L3): a job agent can make the supervisor send
// notices (a hook, then job_update, and again), and the phone must not be its megaphone.
func NewTextNotifier(url string, client *http.Client, logger *slog.Logger) func(string) {
	if client == nil {
		client = &http.Client{Timeout: notifyTimeout}
	}
	if logger == nil {
		logger = slog.Default()
	}
	perJob := ratelimit.New(notifyBurst, notifyRefill, nil)
	overall := ratelimit.New(textBurst, textRefill, nil)
	return func(text string) {
		line := termtext.Cap(redact.String(termtext.Clean(text)), notifyMaxRunes)
		key := "crowsnest"
		if match := noticeJob.FindStringSubmatch(line); match != nil {
			key = match[1]
		}
		if ok, _ := perJob.Allow(key); !ok {
			logger.Info("not notifying --notify-url: too many notices about one job", "job", key)
			return
		}
		if ok, _ := overall.Allow("all"); !ok {
			logger.Info("not notifying --notify-url: too many notices at once", "job", key)
			return
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
