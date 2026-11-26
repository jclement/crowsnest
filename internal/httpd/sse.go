// GET /api/v1/events: the server-sent event stream. One stream per browser tab, fed by the hub.
// The stream opens with a `hello` event, forwards hub events as they arrive, and sends a
// `heartbeat` event when idle. There is no replay: on every (re)connect the client refetches
// the state it shows, so a stream only ever carries "something changed" (DESIGN.md section 9).

package httpd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
)

// sseRetryMillis tells EventSource how soon to reconnect after a drop.
const sseRetryMillis = 3000

// HelloEvent is the first event on every stream.
type HelloEvent struct {
	Version string `json:"version"`
	Session string `json:"session"`
}

// HeartbeatEvent is sent when the stream is otherwise idle.
type HeartbeatEvent struct {
	At time.Time `json:"at"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no") // ask any buffering proxy to pass events straight through
	w.WriteHeader(http.StatusOK)

	subscription := s.deps.Hub.Subscribe(hub.DefaultBuffer)
	defer subscription.Close()

	if _, err := fmt.Fprintf(w, "retry: %d\n\n", sseRetryMillis); err != nil {
		return
	}
	hello := hub.Event{Type: "hello", Data: HelloEvent{Version: s.deps.Version, Session: s.deps.Config.Session}}
	if s.writeEvent(w, controller, hello) != nil {
		return
	}
	s.streamEvents(w, r, controller, subscription)
}

// streamEvents forwards hub events and heartbeats until the client goes away, the server shuts
// down, the hub drops this subscriber, or the session stops being valid.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, controller *http.ResponseController, subscription *hub.Subscription) {
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		var event hub.Event
		select {
		case <-r.Context().Done():
			return
		case <-s.stopping:
			return
		case next, ok := <-subscription.Events():
			if !ok {
				return // overflowed or hub closed: the client reconnects and refetches
			}
			event = next
		case now := <-ticker.C:
			if !s.sessionStillValid(r) {
				return // signed out elsewhere or expired: stop streaming to this tab
			}
			event = hub.Event{Type: "heartbeat", Data: HeartbeatEvent{At: now.UTC()}}
		}
		if s.writeEvent(w, controller, event) != nil {
			return
		}
	}
}

// sessionStillValid re-checks the cookie on each heartbeat, so a logout or password change ends
// open streams within one heartbeat instead of whenever the tab is closed.
func (s *Server) sessionStillValid(r *http.Request) bool {
	if !s.deps.Auth.Enabled() {
		return true
	}
	_, err := s.deps.Auth.Authenticate(r.Context(), sessionToken(r))
	return err == nil
}

// writeEvent writes one SSE frame and flushes it. The event name is checked because a newline
// in it would let one event forge another; the JSON payload cannot contain a raw newline.
func (s *Server) writeEvent(w http.ResponseWriter, controller *http.ResponseController, event hub.Event) error {
	if event.Type == "" || strings.ContainsAny(event.Type, "\r\n") {
		s.deps.Logger.Error("dropping SSE event with an invalid name", "type", event.Type)
		return nil
	}
	payload, err := json.Marshal(event.Data)
	if err != nil {
		s.deps.Logger.Error("dropping SSE event that does not encode", "type", event.Type, "err", err)
		return nil
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, payload); err != nil {
		return err
	}
	return controller.Flush()
}
