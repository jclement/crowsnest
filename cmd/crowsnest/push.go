// Wiring for Web Push (DESIGN.md 8.5): the push service (VAPID keys in the database, the
// subscriptions, the settings) and the watcher that turns hub events and newly asked questions
// into notices. Under --demo the service also accepts http:// endpoints on loopback, so the
// hermetic browser suite can stand up a fake push service; a demo instance has no shell to guard.

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/push"
	"github.com/jclement/crowsnest/internal/store"
)

// startPush builds the push service and starts its watcher, which runs until ctx ends.
func startPush(ctx context.Context, cfg config.Config, db *store.Store, events *hub.Hub, logger *slog.Logger) (*push.Service, *push.Watcher, error) {
	service, err := push.New(ctx, push.Options{Store: db, Logger: logger, AllowLoopbackHTTP: cfg.Demo, Name: cfg.Name})
	if err != nil {
		return nil, nil, fmt.Errorf("starting Web Push: %w", err)
	}
	watcher := push.NewWatcher(push.WatcherOptions{Sender: service, Hub: events, Logger: logger})
	go watcher.Run(ctx)
	return service, watcher, nil
}
