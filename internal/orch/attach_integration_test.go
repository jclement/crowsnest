//go:build integration

// The real-Claude check for attachments (DESIGN.md 8.6): a PNG with a word drawn in it is
// uploaded through the HTTP API and sent with a Chat prompt, exactly as a pasted screenshot is;
// crowsnest copies it into the orchestrator's inbox under the state directory and types
// "Attached: <path> (image/png, …)"; Claude reads the file and says the word. It runs under the
// `default` permission mode (CROWSNEST_E2E_PERMISSION_MODE overrides), where reading outside the
// working directory needs permission, so it also proves the Read allow rule in --settings: a
// dialog would fail the test. Costs a few cents (haiku); same safety rules as integration_test.go.

package orch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

// wordImage draws word in a 5×7 block font, 12 px per dot, black on white: a stand-in for a
// screenshot with a word in it that the model cannot guess.
func wordImage(t *testing.T, word string) []byte {
	t.Helper()
	glyphs := map[rune][7]string{
		'P': {"1111.", "1...1", "1...1", "1111.", "1....", "1....", "1...."},
		'E': {"11111", "1....", "1....", "1111.", "1....", "1....", "11111"},
		'L': {"1....", "1....", "1....", "1....", "1....", "1....", "11111"},
		'I': {"11111", "..1..", "..1..", "..1..", "..1..", "..1..", "11111"},
		'C': {".1111", "1....", "1....", "1....", "1....", "1....", ".1111"},
		'A': {".111.", "1...1", "1...1", "11111", "1...1", "1...1", "1...1"},
		'N': {"1...1", "11..1", "1.1.1", "1..11", "1...1", "1...1", "1...1"},
	}
	const scale = 12
	picture := image.NewRGBA(image.Rect(0, 0, (len(word)*6+1)*scale, 9*scale))
	for y := range picture.Bounds().Dy() {
		for x := range picture.Bounds().Dx() {
			picture.Set(x, y, color.White)
		}
	}
	for i, r := range word {
		glyph := glyphs[r]
		for row := range 7 {
			for col := range 5 {
				if glyph[row][col] != '1' {
					continue
				}
				for dy := range scale {
					for dx := range scale {
						picture.Set((1+i*6+col)*scale+dx, (1+row)*scale+dy, color.Black)
					}
				}
			}
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestIntegrationClaudeReadsAPastedImage(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	mode := os.Getenv("CROWSNEST_E2E_PERMISSION_MODE")
	if mode == "" {
		mode = "default"
	}
	session := startSession(t)
	dir, err := filepath.Abs("e2e-attach-" + randomSuffix(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	claudeDir, err := transcript.ConfigDir(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stateDir := t.TempDir() // outside the working directory, as the real state directory is

	o := orch.New(orch.Options{
		Client: session.Client, KV: db, Session: session.Name, Dir: dir, StateDir: stateDir,
		Model: "haiku", PermissionMode: mode, Version: "integration", ClaudeConfigDir: claudeDir,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	status, err := o.Ensure(ctx)
	if err != nil || status.State == orch.StateNeedsTerminal {
		t.Fatalf("Ensure: %v (status %+v)", err, status)
	}

	files, err := attach.New(attach.Options{Store: db, Dir: filepath.Join(t.TempDir(), "attachments")})
	if err != nil {
		t.Fatal(err)
	}
	authService, err := auth.New(ctx, auth.Options{NoAuth: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	manager := transcript.NewManager(transcript.ManagerOptions{
		ConfigDir: claudeDir, Lookup: orch.TranscriptLookup(session.Client, o), Fallback: orch.PaneFallback(session.Client),
	})
	server, err := httpd.New(httpd.Deps{
		Config: config.Config{Session: "crowsnest-test", NoAuth: true, WebDev: true, HostNames: []string{"127.0.0.1", "localhost"}},
		Auth:   authService, Hub: hub.New(), Logger: slog.New(slog.DiscardHandler), Version: "integration",
		Herdr: session.Client, Conversations: manager, Orchestrator: o,
		Attachments: files, AttachLayout: attach.Layout{StateDir: stateDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	// Upload as the web app does: the raw bytes, the name in X-Filename.
	request, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/attachments", bytes.NewReader(wordImage(t, "PELICAN")))
	request.Header.Set("Origin", ts.URL)
	request.Header.Set("X-Crowsnest", "1")
	request.Header.Set("Content-Type", "image/png")
	request.Header.Set(httpd.HeaderFilename, "Screen%20Shot.png")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	var uploaded struct{ Data attach.Attachment }
	if response.StatusCode != http.StatusCreated || json.Unmarshal(raw, &uploaded) != nil {
		t.Fatalf("upload = %d %s", response.StatusCode, raw)
	}

	var before transcript.Page
	call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages", nil, &before)
	var result orch.PromptResult
	prompt := map[string]any{
		"text":        "What single word is written in the attached image? Read the image file, then reply with only that word, in capitals.",
		"attachments": []string{uploaded.Data.ID},
	}
	sent := time.Now()
	if code := call(t, ts, http.MethodPost, "/api/v1/agents/orchestrator/prompt", prompt, &result); code != http.StatusOK || !result.Started {
		t.Fatalf("POST prompt = %d %+v", code, result)
	}
	copyPath := filepath.Join(stateDir, "orchestrator", "attachments", uploaded.Data.ID+"-Screen-Shot.png")
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("the orchestrator's copy: %v", err)
	}

	cursor, reply, typed := before.Cursor, "", false
	deadline := time.Now().Add(3 * time.Minute)
	for reply == "" && time.Now().Before(deadline) {
		if current := o.Status(); current.State == orch.StateBlocked {
			screen, _ := session.Client.PaneRead(ctx, current.PaneID, herdr.ReadOptions{Source: herdr.SourceVisible})
			t.Fatalf("the orchestrator stopped at a dialog (a permission prompt for the attachment?):\n%s", screen.Text)
		}
		var page transcript.Page
		call(t, ts, http.MethodGet, "/api/v1/agents/orchestrator/messages?after="+cursor, nil, &page)
		for _, message := range page.Messages {
			t.Logf("message kind=%s text=%q", message.Kind, truncate(message.Text, 160))
			if message.Kind == transcript.KindUser && strings.Contains(message.Text, "Attached: "+copyPath+" (image/png, ") {
				typed = true
			}
			if message.Kind == transcript.KindAssistant && strings.Contains(strings.ToUpper(message.Text), "PELICAN") {
				reply = message.Text
			}
		}
		cursor = page.Cursor
		time.Sleep(time.Second)
	}
	if !typed {
		t.Error("the prompt in the transcript does not name the attachment's copy")
	}
	if reply == "" {
		t.Fatal("no reply naming the word in the image within 3 minutes")
	}
	t.Logf("permission mode %s: Claude read the pasted image and replied %q after %s", mode, reply, time.Since(sent).Round(time.Millisecond))
}
