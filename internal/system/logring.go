// LogRing keeps the last few warnings and errors the server logged, redacted, for the System
// page's "Last errors" and `crowsnest doctor` against a running instance. It wraps the real
// slog handler: every record still goes to the log as before.

package system

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/redact"
)

// logRingSize is how many warnings and errors are kept.
const logRingSize = 20

// LogRing is a bounded list of recent warnings and errors.
type LogRing struct {
	mu    sync.Mutex
	lines []doctor.LogLine
	home  string
}

// NewLogRing returns an empty ring; home is shortened to ~ in what it keeps.
func NewLogRing(home string) *LogRing { return &LogRing{home: home} }

// Lines returns the kept lines, newest last.
func (r *LogRing) Lines() []doctor.LogLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]doctor.LogLine(nil), r.lines...)
}

func (r *LogRing) add(line doctor.LogLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	if len(r.lines) > logRingSize {
		r.lines = r.lines[len(r.lines)-logRingSize:]
	}
}

// Handler wraps next so warnings and errors are also kept in r.
func (r *LogRing) Handler(next slog.Handler) slog.Handler { return &ringHandler{next: next, ring: r} }

type ringHandler struct {
	next  slog.Handler
	ring  *LogRing
	attrs []slog.Attr
}

func (h *ringHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn || h.next.Enabled(ctx, level)
}

func (h *ringHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= slog.LevelWarn {
		var b strings.Builder
		b.WriteString(record.Message)
		for _, attr := range h.attrs {
			fmt.Fprintf(&b, " %s=%v", attr.Key, attr.Value)
		}
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key != "config" { // the start-up config dump is long and not an error
				fmt.Fprintf(&b, " %s=%v", attr.Key, attr.Value)
			}
			return true
		})
		message := doctor.RedactText(b.String(), h.ring.home)
		if len(message) > 400 {
			message = message[:400] + "…"
		}
		when := record.Time
		if when.IsZero() {
			when = time.Now()
		}
		h.ring.add(doctor.LogLine{Time: when, Level: strings.ToLower(record.Level.String()), Message: redact.String(message)})
	}
	if !h.next.Enabled(ctx, record.Level) {
		return nil
	}
	return h.next.Handle(ctx, record)
}

func (h *ringHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ringHandler{next: h.next.WithAttrs(attrs), ring: h.ring, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	return &ringHandler{next: h.next.WithGroup(name), ring: h.ring, attrs: h.attrs}
}
