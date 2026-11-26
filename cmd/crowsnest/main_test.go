package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

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
