// Web Push routes (DESIGN.md 8.5 and 10), all behind the login and the usual Origin and
// X-Crowsnest checks:
//
//	GET    /push/key                 {public_key}: the VAPID key for pushManager.subscribe
//	GET    /push/settings            {kinds, devices}: what is pushed, and every subscribed browser
//	PUT    /push/settings            {kinds:{question:true,…}}: switch kinds on or off
//	POST   /push/subscriptions       a PushSubscription's JSON → 201 and the device
//	DELETE /push/subscriptions       {endpoint}: this browser turning notifications off
//	DELETE /push/subscriptions/{id}  remove a device from the list
//	POST   /push/test                {endpoint?}: a test notification to this browser, or to all
//
// A device never comes back with its endpoint (a capability to push to it), only an id, the push
// service's host, the user agent and an endpoint hash the browser can match its own against.

package httpd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/push"
)

const (
	// maxPushBody covers a subscription (an endpoint of at most 2 KB plus two short keys).
	maxPushBody = 8 << 10
	// testBurst test notifications at once, then one more every testRefill: a test reaches every
	// device, so a held-down button should not buzz the phone twenty times.
	testBurst  = 5
	testRefill = time.Minute
)

func (s *Server) registerPushRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/push/key", s.requireAuth(http.HandlerFunc(s.handlePushKey)))
	mux.Handle("GET /api/v1/push/settings", s.requireAuth(http.HandlerFunc(s.handlePushSettings)))
	mux.Handle("PUT /api/v1/push/settings", s.requireAuth(http.HandlerFunc(s.handlePushSettingsUpdate)))
	mux.Handle("POST /api/v1/push/subscriptions", s.requireAuth(http.HandlerFunc(s.handlePushSubscribe)))
	mux.Handle("DELETE /api/v1/push/subscriptions", s.requireAuth(http.HandlerFunc(s.handlePushUnsubscribe)))
	mux.Handle("DELETE /api/v1/push/subscriptions/{id}", s.requireAuth(http.HandlerFunc(s.handlePushRemove)))
	mux.Handle("POST /api/v1/push/test", s.requireAuth(http.HandlerFunc(s.handlePushTest)))
}

// pushService returns the service, or answers 503 and returns nil.
func (s *Server) pushService(w http.ResponseWriter) *push.Service {
	if s.deps.Push == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Web Push is not available in this server.")
	}
	return s.deps.Push
}

func (s *Server) handlePushKey(w http.ResponseWriter, _ *http.Request) {
	service := s.pushService(w)
	if service == nil {
		return
	}
	writeData(w, http.StatusOK, map[string]string{"public_key": service.PublicKey()})
}

// PushSettingsResponse is GET and PUT /push/settings.
type PushSettingsResponse struct {
	Kinds      map[push.Kind]bool `json:"kinds"`
	Order      []push.Kind        `json:"order"` // the kinds in the order to show them
	Devices    []push.Device      `json:"devices"`
	MaxDevices int                `json:"max_devices"`
}

func (s *Server) handlePushSettings(w http.ResponseWriter, r *http.Request) {
	service := s.pushService(w)
	if service == nil {
		return
	}
	settings, err := service.Settings(r.Context())
	if err != nil {
		s.internalError(w, r, "reading the notification settings", err)
		return
	}
	s.writePushSettings(w, r, service, settings)
}

func (s *Server) writePushSettings(w http.ResponseWriter, r *http.Request, service *push.Service, settings push.Settings) {
	devices, err := service.Devices(r.Context())
	if err != nil {
		s.internalError(w, r, "listing the notification devices", err)
		return
	}
	writeData(w, http.StatusOK, PushSettingsResponse{Kinds: settings.Kinds, Order: push.AllKinds, Devices: devices, MaxDevices: push.MaxSubscriptions})
}

func (s *Server) handlePushSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	service := s.pushService(w)
	if service == nil {
		return
	}
	var body struct {
		Kinds map[push.Kind]bool `json:"kinds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send {\"kinds\": {\"question\": true, …}}.")
		return
	}
	settings, err := service.UpdateSettings(r.Context(), body.Kinds)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Unknown notification kind: use question, dialog, job, done or orchestrator.")
		return
	}
	s.writePushSettings(w, r, service, settings)
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	service := s.pushService(w)
	if service == nil {
		return
	}
	var subscription push.Subscription
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&subscription); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send the browser's PushSubscription as JSON: {endpoint, keys: {p256dh, auth}}.")
		return
	}
	device, err := service.Subscribe(r.Context(), subscription, r.UserAgent())
	var invalid *push.ValidationError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, codeValidation, "This subscription was refused: "+invalid.Reason+".")
	case errors.Is(err, push.ErrTooMany):
		writeError(w, http.StatusConflict, codeConflict, "Too many devices get notifications already; remove one from the list first.")
	case err != nil:
		s.internalError(w, r, "storing a push subscription", err)
	default:
		// A session holder could subscribe a push endpoint of their own and keep receiving notices
		// (and their action tokens) after the session is gone (R5-I4): every subscription is on
		// the record, with the device's name, so a stranger's shows up.
		s.deps.Journal.Add(r.Context(), journal.KindSettings, "A device subscribed to notifications: "+device.UserAgent+" via "+device.Service,
			map[string]any{"device": device.ID})
		writeData(w, http.StatusCreated, device)
	}
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	service := s.pushService(w)
	if service == nil {
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&body); err != nil || body.Endpoint == "" {
		writeError(w, http.StatusBadRequest, codeValidation, "Send {\"endpoint\": …}, the subscription to remove.")
		return
	}
	removed, err := service.Unsubscribe(r.Context(), body.Endpoint)
	if err != nil {
		s.internalError(w, r, "removing a push subscription", err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"removed": removed})
}

func (s *Server) handlePushRemove(w http.ResponseWriter, r *http.Request) {
	service := s.pushService(w)
	if service == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, codeValidation, "A device id is a positive whole number.")
		return
	}
	removed, err := service.Remove(r.Context(), id)
	if err != nil {
		s.internalError(w, r, "removing a push device", err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, codeNotFound, "No such device; it may have been removed already.")
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"removed": true})
}

func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	service := s.pushService(w)
	if service == nil {
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, codeValidation, "Send {} for every device, or {\"endpoint\": …} for this one.")
			return
		}
	}
	if ok, retry := s.pushTests.Allow("test"); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, codeRateLimited, "Too many test notifications; wait a minute.")
		return
	}
	if body.Endpoint != "" {
		known, err := service.HasEndpoint(r.Context(), body.Endpoint)
		if err != nil {
			s.internalError(w, r, "finding a push subscription", err)
			return
		}
		if !known {
			writeError(w, http.StatusNotFound, codeNotFound, "This browser is not subscribed; turn notifications on first.")
			return
		}
	}
	result, err := service.Deliver(r.Context(), push.TestNotice(), body.Endpoint)
	if err != nil {
		s.internalError(w, r, "sending a test notification", err)
		return
	}
	writeData(w, http.StatusOK, result)
}
