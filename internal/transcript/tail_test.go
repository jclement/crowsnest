// Tests for locating, tailing and paging: partial lines, truncation, rotation, cursors, and the
// manager following a session id change and falling back to pane text.

package transcript

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func userLine(id, text string) string {
	return `{"type":"user","uuid":"` + id + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func texts(msgs []Message) string {
	out := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, msg.Text)
	}
	return strings.Join(out, ",")
}

func TestLocate(t *testing.T) {
	config := t.TempDir()
	cwd := "/Users/someone/.herdr/worktrees/crow nest/wip-m4"
	if got := ProjectDirName(cwd); got != "-Users-someone--herdr-worktrees-crow-nest-wip-m4" {
		t.Fatalf("ProjectDirName = %q", got)
	}
	if _, err := Locate(config, cwd, "abc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing transcript: %v", err)
	}
	exact := Path(config, cwd, "abc")
	_ = os.MkdirAll(filepath.Dir(exact), 0o700)
	appendFile(t, exact, "")
	if got, err := Locate(config, cwd, "abc"); err != nil || got != exact {
		t.Fatalf("Locate = %q, %v", got, err)
	}
	// A drifted cwd still finds the session by id.
	if got, err := Locate(config, "/somewhere/else", "abc"); err != nil || got != exact {
		t.Fatalf("Locate by id = %q, %v", got, err)
	}
	for _, bad := range []string{"", "../etc/passwd", "a/b", "*"} {
		if _, err := Locate(config, cwd, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Locate(%q) = %v, want ErrNotFound", bad, err)
		}
	}
}

func TestConfigDir(t *testing.T) {
	dir, err := ConfigDir(func(string) string { return "/custom" })
	if err != nil || dir != "/custom" {
		t.Fatalf("ConfigDir with env = %q, %v", dir, err)
	}
	dir, err = ConfigDir(func(string) string { return "" })
	if err != nil || !strings.HasSuffix(dir, string(filepath.Separator)+".claude") {
		t.Fatalf("ConfigDir default = %q, %v", dir, err)
	}
}

func TestTailerAppendsAndPartialLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendFile(t, path, userLine("1", "one"))
	conv := NewConversation(SourceTranscript, "s")
	tailer := NewTailer(path, "s", conv)
	if changed, err := tailer.Poll(); !changed || err != nil {
		t.Fatalf("first poll = %v, %v", changed, err)
	}
	line := userLine("2", "two")
	appendFile(t, path, line[:20]) // half a line: nothing yet
	if changed, _ := tailer.Poll(); changed || conv.Len() != 1 {
		t.Fatalf("partial line produced a message (len %d)", conv.Len())
	}
	appendFile(t, path, line[20:])
	if changed, _ := tailer.Poll(); !changed || texts(conv.Page("", 0).Messages) != "one,two" {
		t.Fatalf("after completing the line: %s", texts(conv.Page("", 0).Messages))
	}
	if changed, _ := tailer.Poll(); changed {
		t.Fatal("a poll with nothing new reported a change")
	}
}

func TestTailerTruncationAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendFile(t, path, userLine("1", "one")+userLine("2", "two"))
	conv := NewConversation(SourceTranscript, "s")
	tailer := NewTailer(path, "s", conv)
	_, _ = tailer.Poll()
	before := conv.Cursor()

	if err := os.WriteFile(path, []byte(userLine("3", "three")), 0o600); err != nil { // shorter: truncated
		t.Fatal(err)
	}
	if changed, _ := tailer.Poll(); !changed || texts(conv.Page("", 0).Messages) != "three" {
		t.Fatalf("after truncation: %s", texts(conv.Page("", 0).Messages))
	}
	if page := conv.Page(before, 0); !page.Reset {
		t.Fatal("a cursor from before the truncation did not get a reset page")
	}

	replacement := path + ".new"
	appendFile(t, replacement, userLine("4", "four")+userLine("5", "five")+userLine("6", "six"))
	if err := os.Rename(replacement, path); err != nil { // replaced: a different file, longer
		t.Fatal(err)
	}
	if changed, _ := tailer.Poll(); !changed || texts(conv.Page("", 0).Messages) != "four,five,six" {
		t.Fatalf("after rotation: %s", texts(conv.Page("", 0).Messages))
	}
}

func TestTailerSkipsOverlongLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	conv := NewConversation(SourceTranscript, "s")
	tailer := NewTailer(path, "s", conv)
	appendFile(t, path, `{"type":"attachment","x":"`+strings.Repeat("a", maxLineBytes+10))
	if _, err := tailer.Poll(); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, `"}`+"\n"+userLine("1", "after"))
	_, _ = tailer.Poll()
	if texts(conv.Page("", 0).Messages) != "after" {
		t.Fatalf("messages = %s", texts(conv.Page("", 0).Messages))
	}
}

func TestConversationPaging(t *testing.T) {
	conv := NewConversation(SourceTranscript, "s")
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		conv.Apply([]Message{{ID: id, Kind: KindUser, Text: id}})
	}
	first := conv.Page("", 3)
	if !first.Reset || !first.Earlier || texts(first.Messages) != "c,d,e" {
		t.Fatalf("first page = %+v", first)
	}
	if page := conv.Page(first.Cursor, 10); page.Reset || len(page.Messages) != 0 {
		t.Fatalf("nothing new = %+v", page)
	}

	// A tool card updated in place comes back with its original index and a new seq.
	conv.Apply([]Message{{ID: "t", Kind: KindTool, Tool: &ToolCall{ID: "t", Name: "Bash"}}})
	conv.Apply([]Message{{ID: "t", Kind: KindTool, Tool: &ToolCall{ID: "t", Name: "Bash", Done: true, Result: "ok"}}})
	conv.Apply([]Message{{ID: "f", Kind: KindUser, Text: "f"}})
	page := conv.Page(first.Cursor, 10)
	if page.Reset || len(page.Messages) != 2 || page.Messages[0].ID != "t" || page.Messages[0].Index != 5 || !page.Messages[0].Tool.Done {
		t.Fatalf("changes page = %+v", page)
	}

	// An identical upsert is not a change.
	if conv.Apply([]Message{{ID: "f", Kind: KindUser, Text: "f"}}) {
		t.Fatal("identical upsert counted as a change")
	}

	// Limits page through changes with More.
	small := conv.Page(first.Cursor, 1)
	if !small.More || len(small.Messages) != 1 {
		t.Fatalf("limited page = %+v", small)
	}
	rest := conv.Page(small.Cursor, 1)
	if rest.More || len(rest.Messages) != 1 || rest.Messages[0].ID != "f" {
		t.Fatalf("second limited page = %+v", rest)
	}

	for _, bad := range []string{"junk", "1", "x.y", "999999.1", first.Cursor[:len(first.Cursor)-1] + "99"} {
		if page := conv.Page(bad, 0); !page.Reset {
			t.Errorf("cursor %q did not reset", bad)
		}
	}
}

// fakeOrigins is a LookupFunc whose answers a test can change.
type fakeOrigins struct {
	mu      sync.Mutex
	origins map[string]Origin
}

func (f *fakeOrigins) set(target string, origin Origin) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.origins[target] = origin
	f.origins[origin.PaneID] = origin
}

func (f *fakeOrigins) lookup(_ context.Context, target string) (Origin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if origin, ok := f.origins[target]; ok {
		return origin, nil
	}
	return Origin{}, ErrNoAgent
}

func TestManagerFollowsSessionChanges(t *testing.T) {
	config := t.TempDir()
	cwd := "/work/project"
	origins := &fakeOrigins{origins: map[string]Origin{}}
	origins.set("orchestrator", Origin{PaneID: "w1:p1", Cwd: cwd, SessionID: "first"})
	first := Path(config, cwd, "first")
	_ = os.MkdirAll(filepath.Dir(first), 0o700)
	appendFile(t, first, userLine("1", "hello"))

	var notified []string
	var notifyMu sync.Mutex
	manager := NewManager(ManagerOptions{
		ConfigDir: config, Lookup: origins.lookup,
		Notify: func(paneID, _ string) { notifyMu.Lock(); notified = append(notified, paneID); notifyMu.Unlock() },
	})
	ctx := context.Background()
	page, err := manager.Messages(ctx, "orchestrator", "", 0)
	if err != nil || page.Source != SourceTranscript || texts(page.Messages) != "hello" {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	// The same agent by pane id is the same conversation.
	if byPane, _ := manager.Messages(ctx, "w1:p1", page.Cursor, 0); byPane.Reset {
		t.Fatal("pane id and name gave different conversations")
	}

	appendFile(t, first, userLine("2", "again"))
	manager.PollAll(ctx)
	notifyMu.Lock()
	if len(notified) != 1 || notified[0] != "w1:p1" {
		t.Fatalf("notified = %v", notified)
	}
	notifyMu.Unlock()

	// /clear: herdr now reports a new session id, whose transcript starts empty of the old turns.
	second := Path(config, cwd, "second")
	appendFile(t, second, userLine("3", "fresh start"))
	origins.set("orchestrator", Origin{PaneID: "w1:p1", Cwd: cwd, SessionID: "second"})
	next, _ := manager.Messages(ctx, "orchestrator", page.Cursor, 0)
	if !next.Reset || next.SessionID != "second" || texts(next.Messages) != "fresh start" {
		t.Fatalf("after session change = %+v", next)
	}

	// New conversation: a fresh session id with no transcript yet must not keep showing the
	// old one.
	origins.set("orchestrator", Origin{PaneID: "w1:p1", Cwd: cwd, SessionID: "third"})
	empty, _ := manager.Messages(ctx, "orchestrator", next.Cursor, 0)
	if !empty.Reset || empty.SessionID != "third" || len(empty.Messages) != 0 {
		t.Fatalf("after a session change with no transcript yet = %+v", empty)
	}
	appendFile(t, Path(config, cwd, "third"), userLine("4", "first of the third"))
	if third, _ := manager.Messages(ctx, "orchestrator", empty.Cursor, 0); !third.Reset || texts(third.Messages) != "first of the third" {
		t.Fatalf("once the transcript appears = %+v", third)
	}

	if _, err := manager.Messages(ctx, "nobody", "", 0); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestManagerFallbackAndIdle(t *testing.T) {
	origins := &fakeOrigins{origins: map[string]Origin{}}
	origins.set("w2:p1", Origin{PaneID: "w2:p1", Cwd: "/x"}) // no session id: no transcript
	now := time.Unix(1_000_000, 0)
	screen := "\x1b[1mcodex\x1b[0m> working\n\n"
	manager := NewManager(ManagerOptions{
		ConfigDir: t.TempDir(), Lookup: origins.lookup, IdleTimeout: time.Minute,
		Fallback: func(context.Context, string) (string, error) { return screen, nil },
		Now:      func() time.Time { return now },
	})
	ctx := context.Background()
	page, _ := manager.Messages(ctx, "w2:p1", "", 0)
	if page.Source != SourcePane || len(page.Messages) != 1 || page.Messages[0].Kind != KindPane || page.Messages[0].Text != "codex> working" {
		t.Fatalf("fallback page = %+v", page)
	}
	if again, _ := manager.Messages(ctx, "w2:p1", page.Cursor, 0); len(again.Messages) != 0 {
		t.Fatalf("unchanged pane text produced a revision: %+v", again)
	}
	screen = "codex> done\n"
	if changed, _ := manager.Messages(ctx, "w2:p1", page.Cursor, 0); len(changed.Messages) != 1 || changed.Messages[0].Text != "codex> done" {
		t.Fatalf("changed pane text = %+v", changed)
	}

	_ = manager.Pin(ctx, "w2:p1")
	now = now.Add(time.Hour)
	manager.PollAll(ctx)
	if len(manager.watchers) != 1 {
		t.Fatal("a pinned conversation was dropped as idle")
	}
	manager.watchers["w2:p1"].pinned = false
	manager.PollAll(ctx)
	if len(manager.watchers) != 0 {
		t.Fatal("an idle conversation was kept")
	}
}
