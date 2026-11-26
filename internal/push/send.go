// Sending one notice to every subscribed browser.
//
// webpush-go does the RFC 8291 encryption (aes128gcm) and the RFC 8292 VAPID header; this file
// adds what a long-running server needs around it: TTL and Urgency from the notice, a Topic
// derived from the tag (so the push service also collapses a pending notice about the same thing),
// pruning a subscription the push service says is gone (404 or 410), and logs that name the push
// service's host and the status, never the endpoint (its path is the device's push capability)
// and never the payload.

package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/jclement/crowsnest/internal/redact"
	"github.com/jclement/crowsnest/internal/store"
)

// maxConcurrentSends bounds the POSTs in flight for one notice.
const maxConcurrentSends = 4

// Result counts what happened to one notice.
type Result struct {
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
	Pruned int `json:"pruned"` // subscriptions removed because the push service said they are gone
}

// payload is what the service worker receives (web/public/sw.js reads these fields).
type payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
	URL   string `json:"url"`
	Kind  Kind   `json:"kind"`
	// Actions and ActionToken are a notification's buttons and the single-use token the service
	// worker sends back with the one pressed (DESIGN.md 8.7). Absent on every other notice.
	Actions     []NoticeAction `json:"actions,omitempty"`
	ActionToken string         `json:"action_token,omitempty"`
}

// Deliver pushes notice to every subscription, or only to the one for endpoint when it is not
// empty (a test from one device). The notice is cleaned first (Notice.Clean). Delivery to one
// device failing never stops the others.
func (s *Service) Deliver(ctx context.Context, notice Notice, endpoint string) (Result, error) {
	if s.opts.Name != "" && notice.Title != "" {
		notice.Title = s.opts.Name + ": " + notice.Title
	}
	notice = notice.Clean()
	body, err := json.Marshal(payload{Title: notice.Title, Body: notice.Body, Tag: notice.Tag, URL: notice.URL, Kind: notice.Kind,
		Actions: notice.Actions, ActionToken: notice.ActionToken})
	if err != nil {
		return Result{}, fmt.Errorf("encoding the notice: %w", err)
	}
	subs, err := s.opts.Store.ListPushSubscriptions(ctx)
	if err != nil {
		return Result{}, err
	}
	var (
		result Result
		mu     sync.Mutex
		wg     sync.WaitGroup
		slots  = make(chan struct{}, maxConcurrentSends)
	)
	for _, sub := range subs {
		if endpoint != "" && sub.Endpoint != endpoint {
			continue
		}
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			outcome := s.sendOne(ctx, sub, body, notice)
			mu.Lock()
			defer mu.Unlock()
			switch outcome {
			case outcomeSent:
				result.Sent++
			case outcomePruned:
				result.Pruned++
			default:
				result.Failed++
			}
		})
	}
	wg.Wait()
	return result, nil
}

type outcome int

const (
	outcomeSent outcome = iota
	outcomeFailed
	outcomePruned
)

// sendOne posts to one subscription and records the result.
func (s *Service) sendOne(ctx context.Context, sub store.PushSubscription, body []byte, notice Notice) outcome {
	host := endpointHost(sub.Endpoint)
	options := &webpush.Options{
		HTTPClient: s.client, Subscriber: subscriber, Topic: topicFor(notice.Tag), TTL: notice.ttl(), Urgency: notice.webpushUrgency(),
		VAPIDPublicKey: s.keys.Public, VAPIDPrivateKey: s.keys.Private,
	}
	subscription := &webpush.Subscription{Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth}}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	response, err := webpush.SendNotificationWithContext(ctx, body, subscription, options)
	if err != nil {
		s.opts.Logger.Warn("Web Push send failed", "device", sub.ID, "service", host, "err", sanitizeError(err))
		s.mark(ctx, sub.ID, false)
		return outcomeFailed
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		// The browser unsubscribed or the subscription expired: it will never work again.
		if _, err := s.opts.Store.DeletePushSubscription(ctx, sub.ID); err != nil {
			s.opts.Logger.Warn("removing a dead Web Push subscription failed", "device", sub.ID, "err", err)
		}
		s.opts.Logger.Info("removed a Web Push subscription the push service says is gone", "device", sub.ID, "service", host, "status", response.StatusCode)
		return outcomePruned
	case response.StatusCode >= 200 && response.StatusCode < 300:
		s.mark(ctx, sub.ID, true)
		return outcomeSent
	default:
		// 400 (bad request), 403 (VAPID refused), 413 (too large), 429 (rate limited), 5xx.
		s.opts.Logger.Warn("Web Push send refused", "device", sub.ID, "service", host, "status", response.StatusCode)
		s.mark(ctx, sub.ID, false)
		return outcomeFailed
	}
}

func (s *Service) mark(ctx context.Context, id int64, ok bool) {
	if err := s.opts.Store.MarkPushResult(context.WithoutCancel(ctx), id, ok, s.opts.Now()); err != nil {
		s.opts.Logger.Warn("recording a Web Push result failed", "device", id, "err", err)
	}
}

// sanitizeError drops the URL from a transport error (*url.Error's text is `Post "<endpoint>": …`)
// and redacts what is left.
func sanitizeError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return redact.String(err.Error())
}

// topicFor makes an RFC 8030 Topic (at most 32 characters of the URL-safe base64 alphabet) from a
// tag: a hash, so tags of any shape (dialog:w1:p1) are valid and nothing readable is sent.
func topicFor(tag string) string {
	if tag == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tag))
	return hex.EncodeToString(sum[:16])
}
