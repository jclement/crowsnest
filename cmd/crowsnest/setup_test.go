package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/store"
)

func init() { auth.SetArgon2Params(auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}) }

// isolate points every directory crowsnest uses at the test's own, and clears the login
// variables a developer's shell might export. It returns the data directory.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	short, err := os.MkdirTemp("/tmp", "cnst-") // the agent socket's path must stay under 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", short)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("CROWSNEST_DATA_DIR", filepath.Join(home, "data"))
	for _, name := range []string{"CROWSNEST_USERNAME", "CROWSNEST_PASSWORD", "CROWSNEST_PASSWORD_FILE", "CROWSNEST_NO_AUTH", "SUPPRESS_AUTH", "CROWSNEST_NAME"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	return filepath.Join(home, "data")
}

// loadServeConfig resolves a config from serve's arguments the way the command does.
func loadServeConfig(t *testing.T, args ...string) config.Config {
	t.Helper()
	root := newRootCommand()
	if err := root.PersistentFlags().Parse(args); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadForServe(root.PersistentFlags(), config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func openDB(t *testing.T, cfg config.Config) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func storeOwner(t *testing.T, db *store.Store) {
	t.Helper()
	service, err := auth.New(context.Background(), auth.Options{}, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteSetup(context.Background(), "jeff", "a stored password", map[string]string{store.InstanceNameKey: "stored"}); err != nil {
		t.Fatal(err)
	}
}

// TestLoginDecisionTable is every combination of a configured login (flag or environment), a
// stored owner and --no-auth.
func TestLoginDecisionTable(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		env    []string
		stored bool
		want   loginMode
	}{
		{name: "nothing", want: loginSetup},
		{name: "stored owner", stored: true, want: loginDatabase},
		{name: "flag username, env password", args: []string{"--username", "u"}, env: []string{"CROWSNEST_PASSWORD", "p"}, want: loginConfig},
		{name: "flag login beats a stored owner", args: []string{"--username", "u"}, env: []string{"CROWSNEST_PASSWORD", "p"}, stored: true, want: loginConfig},
		{name: "env login", env: []string{"CROWSNEST_USERNAME", "u", "CROWSNEST_PASSWORD", "p"}, want: loginConfig},
		{name: "env login beats a stored owner", env: []string{"CROWSNEST_USERNAME", "u", "CROWSNEST_PASSWORD", "p"}, stored: true, want: loginConfig},
		{name: "--no-auth", args: []string{"--no-auth"}, want: loginNone},
		{name: "--no-auth beats a stored owner", args: []string{"--no-auth"}, stored: true, want: loginNone},
		{name: "CROWSNEST_NO_AUTH", env: []string{"CROWSNEST_NO_AUTH", "true"}, want: loginNone},
		{name: "SUPPRESS_AUTH on localhost", env: []string{"SUPPRESS_AUTH", "true"}, stored: true, want: loginNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			for i := 0; i+1 < len(tc.env); i += 2 {
				t.Setenv(tc.env[i], tc.env[i+1])
			}
			cfg := loadServeConfig(t, tc.args...)
			db := openDB(t, cfg)
			if tc.stored {
				storeOwner(t, db)
			}
			got, err := decideLogin(context.Background(), cfg, db)
			if err != nil || got != tc.want {
				t.Fatalf("decideLogin = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestStoredNamePrecedence(t *testing.T) {
	isolate(t)
	cfg := loadServeConfig(t)
	db := openDB(t, cfg)
	if got := storedName(context.Background(), cfg, db); got.Name != cfg.Name {
		t.Fatalf("with nothing stored the name changed to %q", got.Name)
	}
	storeOwner(t, db)
	if got := storedName(context.Background(), cfg, db); got.Name != "stored" {
		t.Fatalf("stored name not applied: %q", got.Name)
	}
	t.Setenv("CROWSNEST_NAME", "fromenv")
	cfg = loadServeConfig(t)
	if got := storedName(context.Background(), cfg, db); got.Name != "fromenv" {
		t.Fatalf("CROWSNEST_NAME lost to the stored name: %q", got.Name)
	}
}

func TestBrowsableBase(t *testing.T) {
	for in, want := range map[string]string{
		"http://0.0.0.0:2769":    "http://localhost:2769",
		"http://[::]:2769":       "http://localhost:2769",
		"http://127.0.0.1:2769":  "http://127.0.0.1:2769",
		"https://100.64.1.2:443": "https://100.64.1.2:443",
	} {
		if got := browsableBase(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// TestServeLocksUntilSetupThenStarts runs serve with no login: nothing reaches herdr, the agent
// socket does not exist and the API is refused until setup completes through HTTP; then herdr
// is connected, and the browser that completed setup is signed in.
func TestServeLocksUntilSetupThenStarts(t *testing.T) {
	isolate(t)
	port := freeTCPPort(t)
	cfg := loadServeConfig(t, "--bind", "127.0.0.1", "--port", fmt.Sprint(port), "--session", "crowsnest-test-setup", "--web-dev", "--no-orchestrator")

	var connected atomic.Int32
	links := make(chan [2]string, 4)
	hooks := serveHooks{
		connect: func(context.Context, config.Config, *slog.Logger) (herdr.Client, string, error) {
			connected.Add(1)
			return fake.NewDemo(), fake.Version, nil
		},
		setupURL: func(link, token string) { links <- [2]string{link, token} },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &lockedWriter{w: &bytes.Buffer{}}
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, slog.New(slog.NewJSONHandler(logs, nil)), hooks) }()

	var link, token string
	select {
	case got := <-links:
		link, token = got[0], got[1]
	case err := <-done:
		t.Fatalf("serve returned before setup: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("no setup link")
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if link != base+"/setup#token="+token || len(token) != 43 {
		t.Fatalf("link %q", link)
	}

	if connected.Load() != 0 {
		t.Fatal("herdr was connected before setup")
	}
	if _, err := os.Stat(agentSocketPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the agent socket exists before setup: %v", err)
	}
	if code := get(t, base+"/api/v1/fleet", nil); code != http.StatusForbidden {
		t.Fatalf("fleet before setup: %d", code)
	}

	body := `{"token":"` + token + `","username":"jeff","password":"a long owner password","name":"devbox"}`
	request, _ := http.NewRequest(http.MethodPost, base+"/api/v1/setup", strings.NewReader(body))
	request.Header.Set("Origin", base)
	request.Header.Set("X-Crowsnest", "1")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("setup: %d", response.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range response.Cookies() {
		if c.Name == "crowsnest_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie from setup")
	}

	// The full server comes up on the same port: herdr connected, signed in, name applied.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var me struct {
			Data struct {
				Username string `json:"username"`
				Features struct {
					Name string `json:"name"`
				} `json:"features"`
			} `json:"data"`
		}
		if code := getJSON(base+"/api/v1/auth/me", cookie, &me); code == http.StatusOK {
			if me.Data.Username != "jeff" || me.Data.Features.Name != "devbox" {
				t.Fatalf("me %+v", me.Data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the full server never signed the setup browser in\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if connected.Load() != 1 {
		t.Fatalf("herdr connected %d times after setup", connected.Load())
	}
	if strings.Contains(strings.ReplaceAll(logs.String(), `"url":"`+link+`"`, ""), token) {
		t.Fatal("the token appears in the log outside the setup link")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

// lockedWriter serialises the logger's writes; logs is read only after serve has returned or
// on a failure, through String.
type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.String()
}

func get(t *testing.T, url string, cookie *http.Cookie) int {
	t.Helper()
	return getJSON(url, cookie, nil)
}

func getJSON(url string, cookie *http.Cookie, into any) int {
	request, _ := http.NewRequest(http.MethodGet, url, nil)
	if cookie != nil {
		request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0
	}
	defer func() { _ = response.Body.Close() }()
	if into != nil {
		_ = json.NewDecoder(response.Body).Decode(into)
	}
	return response.StatusCode
}

// runCLI runs a crowsnest command with stdin and returns its output.
func runAccountCLI(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestResetPassword(t *testing.T) {
	isolate(t)
	port := fmt.Sprint(freeTCPPort(t))
	if _, err := runAccountCLI(t, "a new long password\n", "reset-password", "--port", port); err == nil || !strings.Contains(err.Error(), "no crowsnest database") {
		t.Fatalf("without a database: %v", err)
	}
	cfg := loadServeConfig(t)
	db := openDB(t, cfg)
	if _, err := runAccountCLI(t, "a new long password\n", "reset-password", "--port", port); err == nil || !strings.Contains(err.Error(), "not been set up") {
		t.Fatalf("without an owner: %v", err)
	}
	storeOwner(t, db)
	service, _ := auth.New(context.Background(), auth.Options{}, db)
	session, _, err := service.Login(context.Background(), "127.0.0.1", "jeff", "a stored password", "ua", "localhost:2769")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runAccountCLI(t, "short\n", "reset-password", "--port", port); err == nil {
		t.Fatal("a short password was accepted")
	}
	out, err := runAccountCLI(t, "a new long password\n", "reset-password", "--port", port)
	if err != nil || !strings.Contains(out, "Password changed for jeff") {
		t.Fatalf("reset from stdin: %q %v", out, err)
	}
	if _, err := service.Authenticate(context.Background(), session, "localhost:2769"); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("a session survived reset-password: %v", err)
	}
	if _, _, err := service.Login(context.Background(), "127.0.0.1", "jeff", "a new long password", "ua", "localhost:2769"); err != nil {
		t.Fatalf("the new password: %v", err)
	}

	file := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(file, []byte("from a file password\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runAccountCLI(t, "", "reset-password", "--port", port, "--password-file", file); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("a world-readable password file: %v", err)
	}
	_ = os.Chmod(file, 0o600)
	if out, err := runAccountCLI(t, "", "reset-password", "--port", port, "--password-file", file); err != nil {
		t.Fatalf("reset from a file: %q %v", out, err)
	}
	if _, _, err := service.Login(context.Background(), "127.0.0.1", "jeff", "from a file password", "ua", "localhost:2769"); err != nil {
		t.Fatalf("the file's password: %v", err)
	}
}

func TestResetSetup(t *testing.T) {
	isolate(t)
	port := freeTCPPort(t)
	cfg := loadServeConfig(t, "--port", fmt.Sprint(port))
	db := openDB(t, cfg)
	storeOwner(t, db)

	if _, err := runAccountCLI(t, "", "reset-setup", "--port", fmt.Sprint(port)); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("without a terminal or --yes: %v", err)
	}

	// A server answering on the configured port counts as running.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") }), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	if _, err := runAccountCLI(t, "", "reset-setup", "--yes", "--port", fmt.Sprint(port), "--bind", "127.0.0.1"); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("while running: %v", err)
	}
	if _, err := auth.LoadOwner(context.Background(), db); err != nil {
		t.Fatal("the refused reset removed the owner")
	}
	out, err := runAccountCLI(t, "", "reset-setup", "--yes", "--force", "--port", fmt.Sprint(port), "--bind", "127.0.0.1")
	if err != nil || !strings.Contains(out, "Restart crowsnest now") {
		t.Fatalf("--force: %q %v", out, err)
	}
	_ = server.Close()
	if _, err := auth.LoadOwner(context.Background(), db); !errors.Is(err, auth.ErrNoOwner) {
		t.Fatalf("owner after reset-setup: %v", err)
	}
	if mode, _ := decideLogin(context.Background(), cfg, db); mode != loginSetup {
		t.Fatalf("next start would be %q", mode)
	}
}
