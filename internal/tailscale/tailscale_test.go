// A fake `tailscale` on PATH (a shell script) in each state: running with HTTPS and a serve
// config, logged out, hanging, absent. The real CLI runner is used, so the timeout and the
// not-installed path are tested as they run.

package tailscale

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const runningStatus = `{"BackendState":"Running","Self":{"DNSName":"Quark-2.tail1234.ts.net.","TailscaleIPs":["100.101.102.103"]},"CertDomains":["quark-2.tail1234.ts.net"]}`

const serveStatus = `{"TCP":{"443":{"HTTPS":true}},"Web":{"quark-2.tail1234.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:2769"}}}}}`

// fakeTailscale puts a script named tailscale first on PATH. status and serve are what it prints
// for `status --json` and `serve status --json`; hang makes it sleep instead.
func fakeTailscale(t *testing.T, status, serve string, hang bool) {
	t.Helper()
	noAppBundle(t)
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if hang {
		script += "sleep 10\n"
	}
	script += `case "$1 $2" in
  "status --json") cat <<'EOF'
` + status + `
EOF
  ;;
  "serve status") cat <<'EOF'
` + serve + `
EOF
  ;;
  *) echo "unexpected: $*" >&2; exit 3 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte(script), 0o755); err != nil { //nolint:gosec // an executable test script
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
}

func TestStatusRunning(t *testing.T) {
	fakeTailscale(t, runningStatus, serveStatus, false)
	info := Status(context.Background(), CLI)
	if !info.Installed || !info.Running || info.DNSName != "quark-2.tail1234.ts.net" || !info.HTTPS || info.Error != "" {
		t.Fatalf("%+v", info)
	}
	serves, err := ServesPort(context.Background(), CLI, 2769)
	if err != nil || len(serves) != 1 || serves[0].URL != "https://quark-2.tail1234.ts.net" {
		t.Fatalf("serves %+v %v", serves, err)
	}
	if other, _ := ServesPort(context.Background(), CLI, 8080); len(other) != 0 {
		t.Fatalf("port 8080: %+v", other)
	}
}

func TestStatusLoggedOut(t *testing.T) {
	fakeTailscale(t, `{"BackendState":"NeedsLogin","Self":{"DNSName":""},"CertDomains":null}`, `{}`, false)
	info := Status(context.Background(), CLI)
	if !info.Installed || info.Running || info.DNSName != "" || info.State != "NeedsLogin" {
		t.Fatalf("%+v", info)
	}
	if serves, err := ServesPort(context.Background(), CLI, 2769); err != nil || len(serves) != 0 {
		t.Fatalf("an empty serve config: %+v %v", serves, err)
	}
}

func TestStatusHangingIsCutShort(t *testing.T) {
	fakeTailscale(t, runningStatus, serveStatus, true)
	started := time.Now()
	info := Status(context.Background(), CLI)
	if elapsed := time.Since(started); elapsed > Timeout+time.Second {
		t.Fatalf("took %s", elapsed)
	}
	if info.Running || !strings.Contains(info.Error, "did not answer") {
		t.Fatalf("%+v", info)
	}
}

// noAppBundle turns off the macOS app-bundle fallback for one test: on a developer's Mac the
// real app is there, and these tests must only ever see the fake.
func noAppBundle(t *testing.T) {
	t.Helper()
	saved := MacAppBinary
	MacAppBinary = ""
	t.Cleanup(func() { MacAppBinary = saved })
}

func TestStatusAbsent(t *testing.T) {
	noAppBundle(t)
	t.Setenv("PATH", t.TempDir())
	if info := Status(context.Background(), CLI); info.Installed || info.Error != "" {
		t.Fatalf("%+v", info)
	}
}

func TestServeCommand(t *testing.T) {
	if got := ServeCommand(2769); got != "tailscale serve --bg 2769" {
		t.Fatal(got)
	}
}

func TestStatusMagicDNS(t *testing.T) {
	fakeTailscale(t, `{"BackendState":"Running","Self":{"DNSName":"q.t.ts.net."},"CurrentTailnet":{"MagicDNSEnabled":false}}`, `{}`, false)
	info := Status(context.Background(), CLI)
	if !info.MagicDNSKnown || info.MagicDNS || info.HTTPS {
		t.Fatalf("%+v", info)
	}
}

// A foreground `tailscale serve` session's handlers sit under Foreground; they count as serving
// the port and as ports in use, exactly like a background config.
func TestServeConfigForeground(t *testing.T) {
	raw := `{"TCP":{"443":{"HTTPS":true}},"Web":{"q.t.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}},
	"Foreground":{"abc":{"TCP":{"8443":{"HTTPS":true}},"Web":{"q.t.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:2769"}}}}}}}`
	config, err := ParseServeConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	used := config.PortsInUse()
	if !used[443] || !used[8443] || used[10000] {
		t.Fatalf("ports %v", used)
	}
	serves := config.ServesPort(2769)
	if len(serves) != 1 || serves[0].URL != "https://q.t.ts.net:8443" || !serves[0].Foreground {
		t.Fatalf("serves %+v", serves)
	}
	if empty, err := ParseServeConfig([]byte("  ")); err != nil || len(empty.Handlers()) != 0 {
		t.Fatalf("%+v %v", empty, err)
	}
}

type fakeInfo struct {
	os.FileInfo
	mode os.FileMode
}

func (f fakeInfo) IsDir() bool       { return false }
func (f fakeInfo) Mode() os.FileMode { return f.mode }

func TestFindBinary(t *testing.T) {
	missing := func(string) (string, error) { return "", os.ErrNotExist }
	onPath := func(string) (string, error) { return "/usr/local/bin/tailscale", nil }
	app := func(string) (os.FileInfo, error) { return fakeInfo{mode: 0o755}, nil }
	none := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if path, err := findBinary("darwin", onPath, app); err != nil || path != "/usr/local/bin/tailscale" {
		t.Fatalf("PATH first: %q %v", path, err)
	}
	if path, err := findBinary("darwin", missing, app); err != nil || path != MacAppBinary {
		t.Fatalf("app bundle: %q %v", path, err)
	}
	if _, err := findBinary("linux", missing, app); err == nil {
		t.Fatal("the app bundle is a macOS fallback only")
	}
	if _, err := findBinary("darwin", missing, none); err == nil {
		t.Fatal("neither: not installed")
	}
}
