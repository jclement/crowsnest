package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.2.4", "v1.2.3", true},
		{"v1.3.0", "v1.2.9", true},
		{"2.0.0", "v1.99.99", true},
		{"1.2.3", "v1.2.3", false},
		{"1.2.2", "v1.2.3", false},
		{"1.2.3", "v1.2.3-rc.1", true}, // the release beats its own prerelease
		{"1.2.3-rc.2", "v1.2.3-rc.1", false},
		{"1.2.3", "dev", false},
		{"1.2.3", "16fad2e-dirty", false},
		{"1.2.3", "v1.2.2-4-gabc1234", false}, // git describe between tags is not a release
		{"1.2.3", "0.0.1-snapshot-35c63d4", false},
		{"garbage", "v1.0.0", false},
	}
	for _, tc := range cases {
		if got := Newer(tc.candidate, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestUnsupportedDevBuild(t *testing.T) {
	for _, version := range []string{"dev", "16fad2e", "v1.2.3-dirty", ""} {
		if err := Unsupported(version); !errors.Is(err, ErrDevBuild) {
			t.Errorf("Unsupported(%q) = %v, want ErrDevBuild", version, err)
		}
	}
}

func TestSignerPatternMatchesOnlyTheReleaseWorkflowOnATag(t *testing.T) {
	pattern := regexp.MustCompile(SignerPattern())
	good := "https://github.com/jclement/crowsnest/.github/workflows/release.yml@refs/tags/v1.2.3"
	if !pattern.MatchString(good) {
		t.Errorf("%s does not match %s", SignerPattern(), good)
	}
	for _, bad := range []string{
		"https://github.com/jclement/crowsnest/.github/workflows/ci.yml@refs/tags/v1.2.3",
		"https://github.com/jclement/crowsnest/.github/workflows/release.yml@refs/heads/main",
		"https://github.com/someone/crowsnest/.github/workflows/release.yml@refs/tags/v1.2.3",
		"https://github.com/jclement/crowsnestx/.github/workflows/release.yml@refs/tags/v1.2.3",
	} {
		if pattern.MatchString(bad) {
			t.Errorf("signer pattern accepts %s", bad)
		}
	}
}

func TestReleaseForRefusesAnUnsignedRelease(t *testing.T) {
	raw := githubRelease{TagName: "v1.2.3"}
	raw.Assets = append(raw.Assets,
		asset("crowsnest_1.2.3_linux_amd64.tar.gz"), asset("checksums.txt"))
	if _, err := releaseFor(raw, "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("want a refusal for a release without a bundle, got %v", err)
	}
	raw.Assets = append(raw.Assets, asset("checksums.txt.sigstore.json"))
	rel, err := releaseFor(raw, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if rel.AssetName != "crowsnest_1.2.3_linux_amd64.tar.gz" || rel.Version != "1.2.3" {
		t.Fatalf("picked %+v", rel)
	}
	if _, err := releaseFor(raw, "darwin", "arm64"); err == nil {
		t.Fatal("want an error when there is no archive for the platform")
	}
}

func asset(name string) struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
} {
	return struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}{name, "https://example.invalid/" + name}
}

func TestManagedByHomebrew(t *testing.T) {
	if !managedByHomebrew("/opt/homebrew/Caskroom/crowsnest/1.2.3/crowsnest") {
		t.Error("a cask's binary must count as Homebrew's")
	}
	if managedByHomebrew("/Users/jeff/.local/bin/crowsnest") {
		t.Error("~/.local/bin is not Homebrew's")
	}
}

// ---- Apply against a fake GitHub ------------------------------------------------------------

// fakeRelease serves a release for this platform whose archive holds newBinary.
type fakeRelease struct {
	server    *httptest.Server
	archive   []byte
	checksums []byte
}

func newFakeRelease(t *testing.T, newBinary []byte, tamper func(*fakeRelease)) *fakeRelease {
	t.Helper()
	assetName := fmt.Sprintf("crowsnest_1.2.3_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	fake := &fakeRelease{archive: tarGz(t, map[string][]byte{"README.md": []byte("hi"), "crowsnest": newBinary})}
	sum := sha256.Sum256(fake.archive)
	fake.checksums = fmt.Appendf(nil, "%s  %s\n%s  other.tar.gz\n", hex.EncodeToString(sum[:]), assetName, strings.Repeat("0", 64))
	if tamper != nil {
		tamper(fake)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/jclement/crowsnest/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		base := fake.server.URL
		_, _ = fmt.Fprintf(w, `{"tag_name":"v1.2.3","html_url":"%s/release","assets":[
			{"name":%q,"browser_download_url":"%s/asset"},
			{"name":"checksums.txt","browser_download_url":"%s/checksums"},
			{"name":"checksums.txt.sigstore.json","browser_download_url":"%s/bundle"}]}`,
			base, assetName, base, base, base)
	})
	mux.HandleFunc("GET /asset", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fake.archive) })
	mux.HandleFunc("GET /checksums", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fake.checksums) })
	mux.HandleFunc("GET /bundle", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("bundle")) })
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeRelease) client(verify Verifier) *Client {
	return &Client{API: f.server.URL, HTTP: f.server.Client(), Verify: verify, UserAgent: "test"}
}

// acceptSignature stands in for Sigstore and records what it was asked to verify.
func acceptSignature(seen *[]byte) Verifier {
	return func(artifact, bundle []byte, _ string) error {
		*seen = artifact
		if string(bundle) != "bundle" {
			return errors.New("wrong bundle")
		}
		return nil
	}
}

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	for name, data := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// installed returns a path holding an "old" binary to be replaced.
func installed(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crowsnest")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil { //nolint:gosec // test binary
		t.Fatal(err)
	}
	return path
}

func TestApplyInstallsAVerifiedRelease(t *testing.T) {
	fake := newFakeRelease(t, []byte("new binary"), nil)
	var seen []byte
	client := fake.client(acceptSignature(&seen))
	rel, err := client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := installed(t)
	if err := client.Apply(context.Background(), rel, path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new binary" {
		t.Fatalf("binary is %q, want the new one", got)
	}
	if !bytes.Equal(seen, fake.checksums) {
		t.Fatal("the signature was not checked over checksums.txt")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("new binary is not executable: %v", info.Mode())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temporary files left beside the binary: %v", entries)
	}
}

func TestApplyRefusesABadSignature(t *testing.T) {
	fake := newFakeRelease(t, []byte("evil"), nil)
	client := fake.client(func(_, _ []byte, _ string) error { return errors.New("certificate identity mismatch") })
	rel, err := client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := installed(t)
	err = client.Apply(context.Background(), rel, path)
	if err == nil || !strings.Contains(err.Error(), "refusing to install") {
		t.Fatalf("want a refusal, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatal("the binary was replaced despite a bad signature")
	}
}

func TestApplyRefusesAChecksumMismatch(t *testing.T) {
	fake := newFakeRelease(t, []byte("new"), func(f *fakeRelease) {
		f.archive = tarGz(t, map[string][]byte{"crowsnest": []byte("swapped after signing")})
	})
	var seen []byte
	client := fake.client(acceptSignature(&seen))
	rel, err := client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := installed(t)
	err = client.Apply(context.Background(), rel, path)
	if err == nil || !strings.Contains(err.Error(), "does not match its signed checksum") {
		t.Fatalf("want a checksum refusal, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatal("the binary was replaced despite a checksum mismatch")
	}
}

func TestApplyRefusesAnArchiveNotInTheChecksums(t *testing.T) {
	fake := newFakeRelease(t, []byte("new"), func(f *fakeRelease) {
		f.checksums = []byte(strings.Repeat("0", 64) + "  something-else.tar.gz\n")
	})
	var seen []byte
	client := fake.client(acceptSignature(&seen))
	rel, _ := client.Latest(context.Background())
	if err := client.Apply(context.Background(), rel, installed(t)); err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Fatalf("want a refusal, got %v", err)
	}
}

func TestCheckerRecordsANewerRelease(t *testing.T) {
	fake := newFakeRelease(t, []byte("new"), nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	older := NewChecker(fake.client(nil), "v1.2.0", logger)
	older.CheckOnce(context.Background())
	rel, ok := older.Available()
	if !ok || rel.TagName != "v1.2.3" || rel.URL == "" {
		t.Fatalf("Available() = %+v, %v; want v1.2.3", rel, ok)
	}

	same := NewChecker(fake.client(nil), "v1.2.3", logger)
	same.CheckOnce(context.Background())
	if _, ok := same.Available(); ok {
		t.Fatal("the running version was reported as an update")
	}

	var none *Checker
	if _, ok := none.Available(); ok {
		t.Fatal("a nil checker reported an update")
	}
}

func TestCheckerIgnoresAnUnreachableGitHub(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden) // rate limited
	}))
	defer server.Close()
	checker := NewChecker(&Client{API: server.URL, HTTP: server.Client(), UserAgent: "test"}, "v1.0.0",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	checker.CheckOnce(context.Background())
	if _, ok := checker.Available(); ok {
		t.Fatal("an error became an update")
	}
}

// TestSignerPatternForBindsTheTag is R5-S10: a signature is accepted only for the tag being
// installed, not any v* tag.
func TestSignerPatternForBindsTheTag(t *testing.T) {
	pattern := regexp.MustCompile(SignerPatternFor("v1.2.3"))
	if !pattern.MatchString("https://github.com/jclement/crowsnest/.github/workflows/release.yml@refs/tags/v1.2.3") {
		t.Fatal("the release's own tag is refused")
	}
	for _, other := range []string{"v1.2.4", "v1.2.3-evil", "v1.2.30", "v0.0.1"} {
		if pattern.MatchString("https://github.com/jclement/crowsnest/.github/workflows/release.yml@refs/tags/" + other) {
			t.Errorf("a signature for %s vouches for v1.2.3", other)
		}
	}
}
