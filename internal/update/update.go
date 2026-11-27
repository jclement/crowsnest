// Package update is crowsnest's self-update (DESIGN.md section 16.1): it asks GitHub whether a
// newer release exists, and installs one only after proving it came from this repository's
// release workflow.
//
// The chain of trust, in the order Apply checks it, so the update path can never be the attack
// path:
//
//  1. checksums.txt.sigstore.json is a Sigstore bundle over checksums.txt, made by keyless cosign
//     in .github/workflows/release.yml. It is verified with sigstore-go against the public-good
//     TUF root: a Fulcio certificate issued to GitHub Actions' OIDC issuer for this repository's
//     release workflow on a v* tag, its Rekor transparency-log entry, and its signed timestamps.
//  2. Only then is checksums.txt believed, and the archive's SHA-256 must match its line.
//  3. Only then is the crowsnest binary taken out of the archive and moved over the running one
//     (a temporary file in the same directory, then one rename).
//
// A release without a bundle is refused, never installed unverified. Inside Docker, and for a
// development build, nothing is checked or installed: an image is updated by pulling a new one.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Repository coordinates. The signer identity below is derived from them.
const (
	Owner = "jclement"
	Repo  = "crowsnest"
)

const (
	// CheckTimeout bounds the startup check: it must never delay or hang anything.
	CheckTimeout = 10 * time.Second
	// downloadTimeout bounds each download of an update (archives are ~15 MB).
	downloadTimeout = 2 * time.Minute
	// maxDownloadBytes caps what one download may read, so a hostile or broken server cannot
	// fill memory. The archives are far smaller.
	maxDownloadBytes = 200 << 20
	// binaryName is the file inside each archive (.goreleaser.yml builds.binary).
	binaryName = "crowsnest"
	// githubAPI is the API base; tests point a Client at an httptest server instead.
	githubAPI = "https://api.github.com"
)

// Refusal is a reason not to update that a person can act on, worded to be shown as is (the
// CLI prints it; POST /admin/update returns it). Anything else Apply returns is a failure whose
// detail belongs in a log.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

// ErrDocker is returned when crowsnest runs in a container: update by pulling a new image.
var ErrDocker = &Refusal{"crowsnest is running in a container; pull a new image (docker compose pull && docker compose up -d) instead of updating in place"}

// ErrDevBuild is returned for a build without a release version (`dev`, `git describe` output).
var ErrDevBuild = &Refusal{"this is a development build, not a release; rebuild it from source (git pull && mise run build)"}

// Release is the newest published release and the files Apply needs for this platform.
type Release struct {
	Version     string `json:"version"`  // "1.2.3", no "v"
	TagName     string `json:"tag_name"` // "v1.2.3"
	URL         string `json:"url"`      // the release page, for people
	AssetName   string `json:"-"`        // crowsnest_1.2.3_darwin_arm64.tar.gz
	AssetURL    string `json:"-"`
	ChecksumURL string `json:"-"` // checksums.txt
	BundleURL   string `json:"-"` // checksums.txt.sigstore.json
}

// Verifier proves that bundle is a valid signature over artifact by the expected signer, the
// release workflow at tag. SigstoreVerifier is the real one; tests substitute their own.
type Verifier func(artifact, bundle []byte, tag string) error

// Client talks to GitHub. The zero value is not usable; use NewClient.
type Client struct {
	// API is the GitHub API base URL (default https://api.github.com).
	API string
	// HTTP is used for every request; NewClient gives it no timeout, each call sets its own.
	HTTP *http.Client
	// Verify checks the checksums bundle (default SigstoreVerifier).
	Verify Verifier
	// UserAgent identifies the caller to GitHub, which requires one.
	UserAgent string
}

// NewClient returns a Client for the real GitHub and Sigstore, identifying as currentVersion.
func NewClient(currentVersion string) *Client {
	return &Client{
		API:       githubAPI,
		HTTP:      &http.Client{},
		Verify:    SigstoreVerifier,
		UserAgent: "crowsnest/" + currentVersion,
	}
}

// ---- Should we check at all ---------------------------------------------------------------

// Unsupported says why self-update does not apply to this process (ErrDevBuild, ErrDocker),
// or nil when it does.
func Unsupported(currentVersion string) *Refusal {
	if _, ok := parseVersion(currentVersion); !ok {
		return ErrDevBuild
	}
	if InContainer() {
		return ErrDocker
	}
	return nil
}

// InContainer reports whether this process runs in a Docker or Podman container, by the marker
// files both runtimes create at the root of the container's filesystem.
func InContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

// ---- Checking ------------------------------------------------------------------------------

// githubRelease is the part of GitHub's release JSON we read.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Latest asks GitHub for the newest non-prerelease release and the assets for this OS and
// architecture. It returns an error if any of the three files Apply needs is missing.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(c.API, "/"), Owner, Repo)
	body, err := c.get(ctx, url, "application/vnd.github+json")
	var status *statusError
	if errors.As(err, &status) && status.code == http.StatusNotFound {
		return Release{}, fmt.Errorf("%s/%s has no published release yet", Owner, Repo)
	}
	if err != nil {
		return Release{}, fmt.Errorf("asking GitHub for the latest release: %w", err)
	}
	var raw githubRelease
	if err := json.Unmarshal(body, &raw); err != nil {
		return Release{}, fmt.Errorf("reading GitHub's release JSON: %w", err)
	}
	return releaseFor(raw, runtime.GOOS, runtime.GOARCH)
}

// releaseFor picks the archive for goos/goarch and the signed checksums out of a release.
func releaseFor(raw githubRelease, goos, goarch string) (Release, error) {
	rel := Release{TagName: raw.TagName, Version: strings.TrimPrefix(raw.TagName, "v"), URL: raw.HTMLURL}
	if _, ok := parseVersion(rel.Version); !ok {
		return Release{}, fmt.Errorf("latest release tag %q is not a version", raw.TagName)
	}
	want := fmt.Sprintf("%s_%s_%s_%s.tar.gz", binaryName, rel.Version, goos, goarch)
	for _, asset := range raw.Assets {
		switch asset.Name {
		case want:
			rel.AssetName, rel.AssetURL = asset.Name, asset.URL
		case "checksums.txt":
			rel.ChecksumURL = asset.URL
		case "checksums.txt.sigstore.json":
			rel.BundleURL = asset.URL
		}
	}
	switch {
	case rel.AssetURL == "":
		return Release{}, fmt.Errorf("release %s has no archive for %s/%s", rel.TagName, goos, goarch)
	case rel.ChecksumURL == "" || rel.BundleURL == "":
		return Release{}, fmt.Errorf("release %s is not signed (no checksums.txt or its .sigstore.json); refusing to install it", rel.TagName)
	}
	return rel, nil
}

// Newer reports whether candidate is a later version than current. Both may carry a leading
// "v"; a prerelease (1.2.0-rc.1) sorts before its release (1.2.0). An unparseable version is
// never newer and never older.
func Newer(candidate, current string) bool {
	next, ok1 := parseVersion(candidate)
	have, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range next.numbers {
		if next.numbers[i] != have.numbers[i] {
			return next.numbers[i] > have.numbers[i]
		}
	}
	// Same numbers: a release beats a prerelease of itself. Two prereleases are not compared;
	// GitHub's "latest" is never a prerelease anyway.
	return next.prerelease == "" && have.prerelease != ""
}

// version is a parsed semantic version.
type version struct {
	numbers    [3]int
	prerelease string
}

// parseVersion accepts "1.2.3", "v1.2.3" and "v1.2.3-rc.1". It refuses `dev`, `git describe`
// output (`16fad2e-dirty`, `v1.2.3-4-gabc1234`) and snapshot versions, which are not releases.
func parseVersion(text string) (version, bool) {
	text = strings.TrimPrefix(text, "v")
	core, pre, _ := strings.Cut(text, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var parsed version
	for i, part := range parts {
		if part == "" || len(part) > 9 || strings.Trim(part, "0123456789") != "" {
			return version{}, false
		}
		for _, digit := range part {
			parsed.numbers[i] = parsed.numbers[i]*10 + int(digit-'0')
		}
	}
	if strings.Contains(pre, "-g") || strings.Contains(pre, "snapshot") || strings.Contains(pre, "dirty") {
		return version{}, false
	}
	parsed.prerelease = pre
	return parsed, true
}

// ---- Applying ------------------------------------------------------------------------------

// Apply downloads rel, verifies it (signature, then checksum) and replaces the binary at
// execPath. It installs nothing unless every check passes. execPath is normally Executable().
func (c *Client) Apply(ctx context.Context, rel Release, execPath string) error {
	checksums, err := c.download(ctx, rel.ChecksumURL)
	if err != nil {
		return fmt.Errorf("downloading checksums.txt: %w", err)
	}
	bundle, err := c.download(ctx, rel.BundleURL)
	if err != nil {
		return fmt.Errorf("downloading the checksums signature: %w", err)
	}
	if err := c.Verify(checksums, bundle, rel.TagName); err != nil {
		return fmt.Errorf("checksums.txt is not signed by %s's release workflow; refusing to install: %w", Repo, err)
	}
	want, err := checksumFor(checksums, rel.AssetName)
	if err != nil {
		return err
	}
	archive, err := c.download(ctx, rel.AssetURL)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", rel.AssetName, err)
	}
	if got := sha256.Sum256(archive); !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return fmt.Errorf("%s does not match its signed checksum; refusing to install", rel.AssetName)
	}
	binary, err := extractBinary(archive)
	if err != nil {
		return fmt.Errorf("unpacking %s: %w", rel.AssetName, err)
	}
	return replaceFile(execPath, binary)
}

// Executable is the path of the running binary with symlinks resolved, refusing one that a
// package manager owns: replacing a Homebrew-installed file would leave brew's record wrong.
func Executable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if managedByHomebrew(path) {
		return "", &Refusal{path + " was installed by Homebrew; update it with: brew upgrade crowsnest"}
	}
	return path, nil
}

// managedByHomebrew reports whether path lies in a Homebrew cask or formula directory.
func managedByHomebrew(path string) bool {
	return strings.Contains(path, "/Caskroom/") || strings.Contains(path, "/Cellar/")
}

// checksumFor finds name's SHA-256 in a checksums.txt ("<hex>  <name>" per line).
func checksumFor(checksums []byte, name string) (string, error) {
	for line := range strings.SplitSeq(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("the signed checksums.txt does not list %s; refusing to install", name)
}

// extractBinary returns the crowsnest file from a release tar.gz.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s binary in the archive", binaryName)
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == binaryName {
			return io.ReadAll(io.LimitReader(reader, maxDownloadBytes))
		}
	}
}

// replaceFile writes data next to path and renames it over path, so a reader sees either the
// old binary or the new one, never half of one. A running process keeps its old inode.
func replaceFile(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+binaryName+"-update-*")
	if err != nil {
		return fmt.Errorf("writing the new binary beside %s: %w", path, err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }() // a no-op once the rename has happened
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("writing the new binary: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("writing the new binary: %w", err)
	}
	if err := os.Chmod(tempPath, 0o755); err != nil { //nolint:gosec // an executable must be executable
		return fmt.Errorf("making the new binary executable: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// ---- HTTP ----------------------------------------------------------------------------------

// download fetches url with the download timeout and size cap.
func (c *Client) download(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	return c.get(ctx, url, "application/octet-stream")
}

// get fetches url and returns its body, or an error for anything but 200.
func (c *Client) get(ctx context.Context, url, accept string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", c.UserAgent)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, &statusError{url: url, code: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxDownloadBytes {
		return nil, fmt.Errorf("GET %s: larger than %d bytes", url, maxDownloadBytes)
	}
	return body, nil
}

// statusError is a response other than 200.
type statusError struct {
	url  string
	code int
}

func (e *statusError) Error() string { return fmt.Sprintf("GET %s: HTTP %d", e.url, e.code) }
