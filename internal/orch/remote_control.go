// Remote Control on a standing agent (DESIGN.md 7.10): after each start the watch looks for
// Claude's "/remote-control is active" banner and the session URL it carries, and keeps the
// result in the status (remote_control_note, remote_control_url). The URL is persisted in kv
// beside the session id, so a crowsnest restart that adopts the still-running agent keeps it, and
// it is dropped whenever the conversation changes. Nothing here ever stops or restarts Claude:
// it starts without Remote Control when the login cannot have it, so this only reports.

package orch

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// remoteControlCheck is how long after a start the watch reads the pane on every tick.
	remoteControlCheck = 45 * time.Second
	// remoteControlRelookEvery and remoteControlRelooks bound the looks after that window, while
	// the URL is still unknown (a slow connection, a start-up prompt answered late): one every
	// two minutes for an hour, each a pane read and at most one transcript scan.
	remoteControlRelookEvery = 2 * time.Minute
	remoteControlRelooks     = 30
	// remoteControlBanner is what Claude Code 2.1.332 prints when Remote Control connected
	// (verified by remote_control_integration_test.go).
	remoteControlBanner = "remote-control is active"
	// claudeHeader starts the banner Claude prints at every start; only the text after the last
	// one is read, so a previous conversation's banner left in the pane is never taken.
	claudeHeader = "Claude Code v"
	// RemoteControlActive and RemoteControlUnconfirmed are the notes the status carries.
	RemoteControlActive      = "active"
	RemoteControlUnconfirmed = "Claude did not report Remote Control active. It turns it off by itself when the login cannot use it " +
		"(an API key, a plan without it, or an organisation policy); the agent runs without it."
	// kvRemoteControl holds the record below, under the agent's kv prefix.
	kvRemoteControl = "orchestrator.remote_control"
)

// DemoRemoteControl is the demo's made-up Remote Control: a name and a link of the real shape.
type DemoRemoteControl struct {
	Name string
	URL  string
}

// showDemo puts the demo's name and link in the status; false when this is not the demo.
func (o *Orchestrator) showDemo() bool {
	demo := o.opts.DemoRemoteControl
	if demo == nil {
		return false
	}
	o.update(func(s *Status) {
		s.RemoteControl, s.RemoteControlNote, s.RemoteControlURL = demo.Name, RemoteControlActive, demo.URL
	})
	return true
}

// rcLook schedules the looks: every tick until until, then every remoteControlRelookEvery while
// left lasts. The zero value looks no more.
type rcLook struct {
	until, next time.Time
	left        int
}

func (l rcLook) armed() bool { return !l.until.IsZero() }

// after is the schedule following a look at now.
func (l rcLook) after(now time.Time, found bool) rcLook {
	switch {
	case found:
		return rcLook{}
	case !now.After(l.until):
		return l
	case l.left <= 0:
		return rcLook{}
	}
	l.left--
	l.next = now.Add(remoteControlRelookEvery)
	return l
}

// rcRecord is what kv keeps: the conversation the name and URL belong to.
type rcRecord struct {
	Session string `json:"session"`
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
}

func (o *Orchestrator) armLook() {
	now := o.opts.Now()
	o.mu.Lock()
	o.rcLook = rcLook{until: now.Add(remoteControlCheck), next: now, left: remoteControlRelooks}
	o.mu.Unlock()
}

// startedRemoteControl records a start with Remote Control name ("" when off) on sessionID and
// begins looking for its URL. start has already cleared the status.
func (o *Orchestrator) startedRemoteControl(ctx context.Context, name, sessionID string) {
	if o.showDemo() {
		return
	}
	if name == "" {
		o.forgetRemoteControl(ctx)
		return
	}
	o.update(func(s *Status) { s.RemoteControl = name })
	o.saveRemoteControl(ctx, rcRecord{Session: sessionID, Name: name})
	o.armLook()
}

// restoreRemoteControl is the adoption of a running agent: the kv record for its conversation
// gives back the name and URL. Without one (or for another conversation) the name the launch
// settings would use now is assumed, and the URL is looked for again.
func (o *Orchestrator) restoreRemoteControl(ctx context.Context, sessionID string) {
	if o.showDemo() {
		return
	}
	record, ok := o.loadRemoteControl(ctx)
	if ok && record.Session != "" && record.Session == sessionID && record.Name != "" {
		o.update(func(s *Status) {
			s.RemoteControl, s.RemoteControlURL = record.Name, record.URL
			if record.URL != "" {
				s.RemoteControlNote = RemoteControlActive
			}
		})
		if record.URL == "" {
			o.armLook()
		}
		return
	}
	o.forgetRemoteControl(ctx)
	launch := o.launchSettings()
	if launch.Engine != EngineClaude || launch.RemoteControlName == "" {
		return
	}
	o.update(func(s *Status) {
		s.RemoteControl, s.RemoteControlNote, s.RemoteControlURL = launch.RemoteControlName, "", ""
	})
	o.saveRemoteControl(ctx, rcRecord{Session: sessionID, Name: launch.RemoteControlName})
	o.armLook()
}

// sessionMoved drops a URL that belonged to another conversation (a /clear, or a resume herdr
// reported late) and looks again.
func (o *Orchestrator) sessionMoved(ctx context.Context, sessionID string) {
	status := o.Status()
	if status.RemoteControl == "" || o.opts.DemoRemoteControl != nil {
		return
	}
	if record, ok := o.loadRemoteControl(ctx); ok && record.Session == sessionID {
		return
	}
	o.update(func(s *Status) { s.RemoteControlNote, s.RemoteControlURL = "", "" })
	o.saveRemoteControl(ctx, rcRecord{Session: sessionID, Name: status.RemoteControl})
	o.armLook()
}

// checkRemoteControl is the watch's look: the pane's text since the last start (and, once the
// first window has passed or the banner shows without a readable URL, the transcript) for the
// banner and the URL, as often as rcLook allows.
func (o *Orchestrator) checkRemoteControl(ctx context.Context, paneID string) {
	o.mu.Lock()
	look, status := o.rcLook, o.status
	o.mu.Unlock()
	now := o.opts.Now()
	if status.RemoteControl == "" || status.RemoteControlURL != "" || !look.armed() || paneID == "" || now.Before(look.next) {
		return
	}
	url, banner := o.lookForRemoteControl(ctx, paneID, status.SessionID, now.After(look.until))
	note := status.RemoteControlNote
	switch {
	case url != "" || banner:
		note = RemoteControlActive
	case now.After(look.until) && note == "":
		note = RemoteControlUnconfirmed
	}
	o.mu.Lock()
	o.rcLook = look.after(now, url != "")
	o.mu.Unlock()
	if url == "" && note == status.RemoteControlNote {
		return
	}
	o.update(func(s *Status) { s.RemoteControlNote, s.RemoteControlURL = note, url })
	if url != "" {
		o.saveRemoteControl(ctx, rcRecord{Session: status.SessionID, Name: status.RemoteControl, URL: url})
	}
	o.opts.Logger.Info("remote control", "agent", o.agentName(), "name", status.RemoteControl, "note", note, "url_known", url != "")
}

// lookForRemoteControl reads the pane (and the transcript when the pane is not enough and late
// says the first window has passed) for the session URL and the banner.
func (o *Orchestrator) lookForRemoteControl(ctx context.Context, paneID, sessionID string, late bool) (string, bool) {
	readCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	read, err := o.opts.Client.PaneRead(readCtx, paneID, herdr.ReadOptions{Source: herdr.SourceRecent, Lines: 200})
	cancel()
	url, banner := "", false
	if err == nil {
		text := sinceLastStart(read.Text)
		url, banner = ParseRemoteControlURL(text), strings.Contains(strings.ToLower(text), remoteControlBanner)
	}
	if url == "" && (late || banner) && sessionID != "" && o.opts.ClaudeConfigDir != "" {
		if path, err := transcript.Locate(o.opts.ClaudeConfigDir, o.opts.Dir, sessionID); err == nil {
			url = TranscriptRemoteControlURL(path)
		}
	}
	return url, banner
}

// sinceLastStart is the pane text from the last Claude start-up header on (all of it if none).
func sinceLastStart(text string) string {
	if i := strings.LastIndex(text, claudeHeader); i >= 0 {
		return text[i:]
	}
	return text
}

func (o *Orchestrator) loadRemoteControl(ctx context.Context) (rcRecord, bool) {
	value, err := o.opts.KV.GetKV(ctx, o.kvKey(kvRemoteControl))
	if err != nil || value == "" {
		return rcRecord{}, false
	}
	var record rcRecord
	if json.Unmarshal([]byte(value), &record) != nil || (record.URL != "" && !ValidRemoteControlURL(record.URL)) {
		return rcRecord{}, false
	}
	return record, true
}

// saveRemoteControl persists record; a failure costs a look after the next crowsnest restart.
func (o *Orchestrator) saveRemoteControl(ctx context.Context, record rcRecord) {
	body, _ := json.Marshal(record)
	if err := o.opts.KV.SetKV(ctx, o.kvKey(kvRemoteControl), string(body)); err != nil {
		o.opts.Logger.Warn("storing the remote control link", "agent", o.agentName(), "err", err)
	}
}

func (o *Orchestrator) forgetRemoteControl(ctx context.Context) {
	if err := o.opts.KV.DeleteKV(ctx, o.kvKey(kvRemoteControl)); err != nil {
		o.opts.Logger.Warn("forgetting the remote control link", "agent", o.agentName(), "err", err)
	}
}
