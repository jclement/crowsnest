package agenttrust

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// R9-C9: the JSON value null is valid JSON but not an object; the write is refused with a
// sentence, the file is left as it was, and nothing panics.
func TestClaudeTrustRefusesANullConfig(t *testing.T) {
	for _, body := range []string{"null", " null\n"} {
		path := filepath.Join(t.TempDir(), ".claude.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		result := Trust(Paths{ClaudeConfig: path}, []string{"/work/app"}, false)[0]
		if result.Status != StatusFailed || !strings.Contains(result.Detail, "null") {
			t.Errorf("%q: %+v", body, result)
		}
		if data, _ := os.ReadFile(path); string(data) != body {
			t.Errorf("%q was rewritten to %q", body, data)
		}
	}
}

// R9-C8: a write another program makes between crowsnest's read and its rename is not lost. The
// first computation of the change writes a new key behind crowsnest's back (as a running Claude
// would); the update must notice before renaming, start over, and keep both.
func TestTrustKeepsAConcurrentWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"projects": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	wrote, err := updateFile(path, func(original []byte) ([]byte, bool, error) {
		if calls.Add(1) == 1 {
			if err := os.WriteFile(path, []byte(`{"projects": {}, "mcpApproved": ["claude-wrote-this"]}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return claudeWithTrust(original, []string{"/work/app"})
	})
	if err != nil || !wrote {
		t.Fatalf("wrote %v, err %v", wrote, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "claude-wrote-this") || !strings.Contains(string(data), `"hasTrustDialogAccepted": true`) {
		t.Fatalf("a write was lost:\n%s", data)
	}
	if calls.Load() != 2 {
		t.Errorf("the change was computed %d times, want 2 (once on the stale read, once on the new one)", calls.Load())
	}
}

// A file that never stops changing is given up on, loudly, with nothing written.
func TestTrustGivesUpOnAFileThatKeepsChanging(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	_, err := updateFile(path, func(original []byte) ([]byte, bool, error) {
		_ = os.WriteFile(path, []byte(`{"n": `+strings.Repeat("1", int(n.Add(1)))+`}`), 0o600)
		return claudeWithTrust(original, []string{"/work/app"})
	})
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "hasTrustDialogAccepted") {
		t.Errorf("wrote over a changing file: %s", data)
	}
}

// Claude Code's own lock (a fresh <file>.lock directory) is waited for; a stale one is taken
// over; one held past the wait is a failure with nothing written.
func TestTrustHonoursClaudesLock(t *testing.T) {
	defer func(stale, wait time.Duration) { lockStale, lockWait = stale, wait }(lockStale, lockWait)
	lockStale, lockWait = 2*time.Second, 600*time.Millisecond

	t.Run("waits_for_a_live_holder", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".claude.json")
		lock := path + ".lock"
		if err := os.Mkdir(lock, 0o700); err != nil {
			t.Fatal(err)
		}
		released := make(chan time.Time, 1)
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = os.Remove(lock)
			released <- time.Now()
		}()
		result := Trust(Paths{ClaudeConfig: path}, []string{"/work/app"}, false)[0]
		finished := time.Now()
		if result.Status != StatusTrusted {
			t.Fatalf("%+v", result)
		}
		if at := <-released; finished.Before(at) {
			t.Errorf("wrote while the lock was held")
		}
	})
	t.Run("takes_over_a_stale_lock", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".claude.json")
		lock := path + ".lock"
		if err := os.Mkdir(lock, 0o700); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		_ = os.Chtimes(lock, old, old)
		if result := Trust(Paths{ClaudeConfig: path}, []string{"/work/app"}, false)[0]; result.Status != StatusTrusted {
			t.Fatalf("%+v", result)
		}
		if _, err := os.Stat(lock); err == nil {
			t.Error("the lock was not released")
		}
	})
	t.Run("gives_up_on_a_held_lock", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".claude.json")
		if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path+".lock", 0o700); err != nil {
			t.Fatal(err)
		}
		result := Trust(Paths{ClaudeConfig: path}, []string{"/work/app"}, false)[0]
		if result.Status != StatusFailed || !strings.Contains(result.Detail, "nothing was written") {
			t.Fatalf("%+v", result)
		}
		if data, _ := os.ReadFile(path); string(data) != "{}" {
			t.Errorf("wrote under someone else's lock: %s", data)
		}
	})
}

// R9-S8: a symlinked ~/.claude.json (stow, chezmoi) stays a link; its target gets the trust.
func TestTrustWritesThroughALink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "claude.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"keep": 1}`), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".claude.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if result := Trust(Paths{ClaudeConfig: link}, []string{"/work/app"}, false)[0]; result.Status != StatusTrusted {
		t.Fatalf("%+v", result)
	}
	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a plain file")
	}
	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), `"keep": 1`) || !strings.Contains(string(data), "hasTrustDialogAccepted") {
		t.Errorf("target: %s", data)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want the target's 0640", info.Mode().Perm())
	}
}

// R9-S8: a FIFO where the config should be fails at once instead of hanging the request.
func TestTrustDoesNotBlockOnAFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	done := make(chan Result, 1)
	go func() { done <- Trust(Paths{ClaudeConfig: path}, []string{"/work/app"}, false)[0] }()
	select {
	case result := <-done:
		if result.Status != StatusFailed {
			t.Errorf("%+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the trust write blocked on a FIFO")
	}
}
