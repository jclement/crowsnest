package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/taglines"
)

func TestVersionOutput(t *testing.T) {
	var out bytes.Buffer
	if err := printVersion(&out, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "crowsnest dev (unknown, unknown)") || !strings.HasPrefix(lines[1], "go") {
		t.Fatalf("version output:\n%s", out.String())
	}
	found := false
	for _, tagline := range taglines.All() {
		found = found || tagline == lines[2]
	}
	if !found {
		t.Fatalf("third line %q is not a tagline", lines[2])
	}
	out.Reset()
	if err := printVersion(&out, true); err != nil {
		t.Fatal(err)
	}
	var info VersionInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil || info.Version != "dev" || info.Platform == "" {
		t.Fatalf("json version: %s %v", out.String(), err)
	}
}

func TestRootVersionFlagAndUsageErrors(t *testing.T) {
	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil || out.String() != "crowsnest dev (unknown, unknown)\n" {
		t.Fatalf("--version: %q %v", out.String(), err)
	}
	root = newRootCommand()
	root.SetArgs([]string{"--no-such-flag"})
	var usage usageError
	if err := root.Execute(); !errors.As(err, &usage) {
		t.Fatalf("an unknown flag should be a usage error, got %v", err)
	}
}

func TestHealthURL(t *testing.T) {
	tests := []struct {
		addr string
		tls  bool
		want string
	}{
		{"127.0.0.1:2769", false, "http://127.0.0.1:2769/api/v1/health"},
		{"0.0.0.0:2769", false, "http://127.0.0.1:2769/api/v1/health"},
		{"[::]:2769", true, "https://[::1]:2769/api/v1/health"},
		{"100.64.1.2:80", false, "http://100.64.1.2:80/api/v1/health"},
	}
	for _, tc := range tests {
		cfg := config.Config{Listeners: []config.Listener{{Addr: netip.MustParseAddrPort(tc.addr)}}}
		if tc.tls {
			cfg.TLSCert, cfg.TLSKey = "c", "k"
		}
		if got := healthURL(cfg); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.addr, got, tc.want)
		}
	}
}

func TestCheckHealth(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"ok","version":"v1.2.3","update_available":false}}`))
	}))
	defer healthy.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	cfgFor := func(url string) config.Config {
		addr := netip.MustParseAddrPort(strings.TrimPrefix(url, "http://"))
		return config.Config{Listeners: []config.Listener{{Addr: addr}}}
	}
	var out bytes.Buffer
	if err := checkHealth(context.Background(), &out, cfgFor(healthy.URL), false); err != nil || !strings.HasPrefix(out.String(), "ok v1.2.3") {
		t.Fatalf("healthy: %q %v", out.String(), err)
	}
	if err := checkHealth(context.Background(), &out, cfgFor(broken.URL), false); err == nil {
		t.Fatal("a 500 should be unhealthy")
	}
	closed := cfgFor(broken.URL)
	broken.Close()
	if err := checkHealth(context.Background(), &out, closed, false); err == nil || !strings.Contains(err.Error(), "not answering") {
		t.Fatalf("closed port: %v", err)
	}
}

// TestDemoUsesThrowawayState is R1 L2: --demo never opens the real database or state directory.
func TestDemoUsesThrowawayState(t *testing.T) {
	real := config.Config{Session: "crowsnest", DataDir: "/home/crow/.local/share/crowsnest", StateDir: "/home/crow/.local/state/crowsnest/crowsnest", Dir: "/home/crow/.local/share/crowsnest/orchestrator"}
	if same, cleanup, err := demoState(real); err != nil || same.DataDir != real.DataDir || same.StateDir != real.StateDir || same.Dir != real.Dir {
		t.Fatalf("a non-demo config was changed: %+v, %v", same, err)
	} else {
		cleanup()
	}
	demo := real
	demo.Demo = true
	got, cleanup, err := demoState(demo)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(got.DataDir)
	for _, path := range []string{got.DataDir, got.StateDir, got.Dir, got.DatabasePath()} {
		if !strings.HasPrefix(path, root+string(filepath.Separator)) || strings.HasPrefix(path, "/home/crow") {
			t.Errorf("demo path %s is not under its temporary directory %s", path, root)
		}
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("temporary directory: %v", err)
	}
	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the demo's temporary directory survived cleanup: %v", err)
	}
}

// TestDemoServeLeavesTheRealDirectoriesAlone runs `serve --demo` itself, pointed at a data
// directory and XDG state home of the test's own, and checks neither was written (R1 L2).
func TestDemoServeLeavesTheRealDirectoriesAlone(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	stateHome := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CROWSNEST_DATA_DIR", dataDir)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := newRootCommand()
	root.SetArgs([]string{"serve", "--demo", "--no-auth", "--web-dev", "--port", fmt.Sprint(port)})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	health := fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := http.Get(health) //nolint:noctx // a test poll
		if err == nil {
			_ = response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve --demo never answered: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // let the orchestrator start and write its files
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve --demo: %v", err)
	}
	for _, dir := range []string{dataDir, stateHome} {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			t.Errorf("--demo wrote into %s: %v", dir, entries)
		}
	}
}

// TestNoAuthWarningNamesTailnetPeers is R1 I7.
func TestNoAuthWarningNamesTailnetPeers(t *testing.T) {
	local := config.Config{NoAuth: true, Listeners: []config.Listener{{Addr: netip.MustParseAddrPort("127.0.0.1:2769")}}}
	if warning := noAuthWarning(local); !strings.Contains(warning, "AUTHENTICATION IS OFF") || strings.Contains(warning, "tailnet") {
		t.Fatalf("localhost warning: %q", warning)
	}
	tailnet := config.Config{NoAuth: true, Listeners: []config.Listener{{Addr: netip.MustParseAddrPort("100.101.102.103:2769")}}}
	if warning := noAuthWarning(tailnet); !strings.Contains(warning, "shared into your tailnet") {
		t.Fatalf("tailnet warning does not say who can reach it: %q", warning)
	}
}

